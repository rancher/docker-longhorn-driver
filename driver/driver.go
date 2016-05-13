package driver

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/gorilla/mux"
	"github.com/mitchellh/mapstructure"

	md "github.com/rancher/go-rancher-metadata/metadata"
	rancherClient "github.com/rancher/go-rancher/client"

	"github.com/rancher/docker-longhorn-driver/model"
	"github.com/rancher/docker-longhorn-driver/util"
)

const (
	root                = "/var/lib/rancher/longhorn"
	mountsDir           = "mounts"
	fakeMountsDir       = "fake-mounts"
	localCacheDir       = "localcache"
	mountBin            = "mount"
	umountBin           = "umount"
	rancherMetadataURL  = "http://rancher-metadata/2015-12-19"
	volumeStackPrefix   = "volume-"
	defaultVolumeSize   = "0b"
	optSize             = "size"
	optReplicaBaseImage = "base-image"
	optDontFormat       = "dont-format"
)

type VolumeManager interface {
	List() ([]model.Volume, error)
	Get(name string) (model.Volume, error)
	Create(volume model.Volume) (model.Volume, error)
	Delete(name string) error
	Mount(name string) (model.Volume, error)
	Unmount(name string) error
}

func NewStorageDaemon(driverContainerName, driverName, volumeStackImage string, client *rancherClient.RancherClient) (*StorageDaemon, error) {
	metadata := md.NewClient(rancherMetadataURL)

	if err := os.MkdirAll(filepath.Join(root, localCacheDir), 0744); err != nil {
		return nil, fmt.Errorf("Couldn't create localcache dir. Error: %v", err)
	}

	volumeStore := &volumeStore{
		mutex:    &sync.RWMutex{},
		metadata: metadata,
		rootDir:  root,
	}

	sd := &StorageDaemon{
		driverContainerName: driverContainerName,
		driverName:          driverName,
		client:              client,
		metadata:            metadata,
		store:               volumeStore,
		volumeStackImage:    volumeStackImage,
		rootDir:             root,
	}

	return sd, nil
}

type StorageDaemon struct {
	mutex               *sync.RWMutex
	client              *rancherClient.RancherClient
	metadata            *md.Client
	store               *volumeStore
	driverContainerName string
	driverName          string
	hostUUID            string
	volumeStackImage    string
	rootDir             string
}

func (d *StorageDaemon) ListenAndServe() error {
	dh := &deleteHandler{
		daemon: d,
	}
	router := mux.NewRouter().StrictSlash(true)
	router.Methods("DELETE").Path("/v1/volumes/{name}").Handler(dh)
	return http.ListenAndServe(":80", router)
}

type deleteHandler struct {
	daemon *StorageDaemon
}

func (h *deleteHandler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	err := h.daemon.Delete(name, true)
	if err != nil {
		logrus.Errorf("Error deleting volume %v: %v", name, err)
		rw.WriteHeader(http.StatusInternalServerError)
		rw.Write([]byte(err.Error()))
	}
}

func (d *StorageDaemon) List() ([]*model.Volume, error) {
	logrus.Infof("Listing volumes")
	return d.store.list()
}

func (d *StorageDaemon) Get(name string) (*model.Volume, error) {
	logrus.Infof("Getting volume %v", name)
	vol, _, moved, err := d.store.get(name)

	if moved {
		vol.Mountpoint = "moved"
	} else {

	}

	return vol, err
}

func (d *StorageDaemon) Create(volume *model.Volume) (*model.Volume, error) {
	logrus.Infof("Creating volume %v", volume)
	d.store.create(volume.Name)

	sizeStr := volume.Opts[optSize]
	var size string
	if sizeStr == "" {
		sizeStr = defaultVolumeSize
		logrus.Infof("No size option provided. Using default: %v", defaultVolumeSize)
	}
	size, sizeGB, err := util.ConvertSize(sizeStr)
	if err != nil {
		return nil, fmt.Errorf("Can't parse size %v. Error: %v", sizeStr, err)
	}

	dontFormat, _ := strconv.ParseBool(volume.Opts[optDontFormat])
	volConfig := volumeConfig{
		Name:             volume.Name,
		Size:             size,
		SizeGB:           sizeGB,
		ReplicaBaseImage: volume.Opts[optReplicaBaseImage],
		DontFormat:       dontFormat,
	}
	stack := newStack(volume.Name, d.driverContainerName, d.driverName, d.volumeStackImage, volConfig, d.client)

	if err := d.doCreateVolume(volume, stack); err != nil {
		stack.delete()
		return nil, fmt.Errorf("Error creating Rancher stack for volume %v: %v.", volume.Name, err)
	}

	return volume, nil
}

func (d *StorageDaemon) doCreateVolume(volume *model.Volume, stack *stack) error {
	// Doing find just to see if we are creating versus using an existing stack
	env, err := stack.find()
	if err != nil {
		return err
	}

	// Always run create because it also ensures that things are active
	if _, err := stack.create(); err != nil {
		return err
	}

	// If env was nil then we created stack so we need to format
	if env == nil {
		dev := getDevice(volume.Name)
		if err := waitForDevice(dev); err != nil {
			return err
		}

		if stack.volumeConfig.DontFormat {
			logrus.Infof("Skipping formatting for volume %v.", volume.Name)
		} else {
			logrus.Infof("Formatting volume %v - %v", volume.Name, dev)
			if _, err := util.Execute("mkfs.ext4", []string{"-F", dev}); err != nil {
				return err
			}
		}
	}

	if err := stack.moveController(); err != nil {
		logrus.Errorf("Failed to move controller to %v: %v", d.driverContainerName, err)
		return err
	}

	return nil
}

func (d *StorageDaemon) Delete(name string, removeStack bool) error {
	// This delete is a simple operation that just removes the volume from the local cache
	logrus.Infof("Deleting volume %v", name)
	if removeStack {
		stack := newStack(name, d.driverContainerName, d.driverName, d.volumeStackImage, volumeConfig{}, d.client)
		if err := stack.delete(); err != nil {
			return err
		}
	}
	d.store.delete(name)
	return nil
}

func (d *StorageDaemon) Mount(name string) (*model.Volume, error) {
	logrus.Infof("Mounting volume %v", name)

	vol, config, moved, err := d.store.get(name)
	if err != nil {
		return nil, fmt.Errorf("Error getting volume: %v", err)
	}
	if vol == nil {
		return nil, fmt.Errorf("No such volume: %v", name)
	}

	if moved {
		return nil, fmt.Errorf("Volume %v no longer reside on this host and cannot be mounted.", name)
	}

	dev := getDevice(vol.Name)
	if err != nil {
		return nil, err
	}

	if err := waitForDevice(dev); err != nil {
		return nil, err
	}

	var mp string
	var e error
	if config.DontFormat {
		logrus.Infof("Creating fake mount directory for %v because dont-format option was specified.", vol.Name)
		mp = fakeMountPoint(d.rootDir, vol.Name)
		if err := os.MkdirAll(mp, 0744); err != nil {
			return nil, err
		}
	} else {
		mp, e = d.volumeMount(vol)
		if e != nil {
			return nil, e
		}
	}

	vol.Mountpoint = mp
	return vol, nil
}

func (d *StorageDaemon) Unmount(name string) error {
	logrus.Infof("Unmounting volume %v", name)

	vol, config, moved, err := d.store.get(name)
	if err != nil {
		return fmt.Errorf("Error getting volume %v for unmount: %v.", vol, err)
	}

	// If volume doesn't exist or has been moved, just no-op and return successfully
	if vol == nil || moved {
		logrus.Infof("Umount called on a nonexistent or moved volume %v. No-op.", vol.Name)
		return nil
	}

	if config.DontFormat {
		logrus.Infof("Remvoing fake mount dir for %v because dont-format option was specified.", name)
		mp := fakeMountPoint(d.rootDir, name)
		if err := os.Remove(mp); err != nil {
			logrus.Warnf("Cannot cleanup fake mount point directory %v due to %v.", mp, err)
		}
		return nil
	}

	mountPoint := mountPoint(d.rootDir, vol.Name)
	if mountPoint == "" {
		logrus.Infof("Umount called on umounted volume %v.", vol.Name)
		return nil
	}

	if _, err := callUmount([]string{mountPoint}); err != nil {
		return err
	}

	if err := os.Remove(mountPoint); err != nil {
		logrus.Warnf("Cannot cleanup mount point directory %v due to %v.", mountPoint, err)
	}

	return nil
}

func (d *StorageDaemon) volumeMount(volume *model.Volume) (string, error) {
	dev := getDevice(volume.Name)

	mountPoint := mountPoint(d.rootDir, volume.Name)
	if err := os.MkdirAll(mountPoint, 0744); err != nil {
		return "", err
	}

	if !isMounted(mountPoint) {
		logrus.Infof("Mounting volume %v to %v.", volume.Name, mountPoint)
		_, err := callMount([]string{dev, mountPoint})
		if err != nil {
			return "", err
		}
	}
	return mountPoint, nil
}

func callUmount(cmdArgs []string) (string, error) {
	output, err := util.Execute(umountBin, cmdArgs)
	if err != nil {
		return "", err
	}
	return output, nil
}

func callMount(cmdArgs []string) (string, error) {
	output, err := util.Execute(mountBin, cmdArgs)
	if err != nil {
		return "", err
	}
	return output, nil
}

func isMounted(mountPoint string) bool {
	output, err := callMount([]string{})
	if err != nil {
		return false
	}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, mountPoint) {
			return true
		}
	}
	return false
}

func mountPoint(rootDir, volumeName string) string {
	return filepath.Join(rootDir, mountsDir, volumeName)
}

func fakeMountPoint(rootDir, volumeName string) string {
	return filepath.Join(rootDir, fakeMountsDir, volumeName)
}

func getDevice(volumeName string) string {
	return filepath.Join(util.DevDir, volumeName)
}

func waitForDevice(dev string) error {
	err := Backoff(5*time.Minute, fmt.Sprintf("Failed to find %s", dev), func() (bool, error) {
		if _, err := os.Stat(dev); err == nil {
			return true, nil
		}
		return false, nil
	})
	return err
}

type volumeStore struct {
	mutex    *sync.RWMutex
	metadata *md.Client
	hostUUID string
	rootDir  string
}

func (s *volumeStore) create(name string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	file := filepath.Join(s.rootDir, localCacheDir, name)
	if err := ioutil.WriteFile(file, []byte{}, 0644); err != nil {
		return fmt.Errorf("Couldn't write local cache record for %v. Error: %v", name, err)
	}
	return nil
}

func (s *volumeStore) delete(name string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	file := filepath.Join(s.rootDir, localCacheDir, name)
	if err := os.Remove(file); err != nil {
		return fmt.Errorf("Couldn't remove local cache record for %v. Error: %v", name, err)
	}
	return nil
}

// Return values are the volume, a boolean `moved` whose value is true if the volume has been moved to a different
// host, and an error
func (s *volumeStore) get(name string) (*model.Volume, volumeConfig, bool, error) {
	s.mutex.RLock()

	volumes, err := s.getVolumesFromRancher()
	if err != nil {
		s.mutex.RUnlock()
		return nil, volumeConfig{}, false, fmt.Errorf("Couldn't obtain list of volumes from Rancher. Error: %v", err)
	}

	config, inRancher := volumes[name]

	localCache, err := s.getVolumesInLocalCache()
	if err != nil {
		s.mutex.RUnlock()
		return nil, volumeConfig{}, false, fmt.Errorf("Couldn't obtain list of volumes from local cache. Error: %v", err)
	}

	_, inLocalCache := localCache[name]
	s.mutex.RUnlock()

	moved := false
	if !inRancher && !inLocalCache {
		// neither Rancher nor the local cache thinks this volume is on this host. It doesn't exist
		return nil, volumeConfig{}, false, nil
	} else if inRancher && !inLocalCache {
		// Rancher says its on this host, but not in local cache, create entry
		s.create(name)
	} else if !inRancher && inLocalCache {
		// Rancher says its elsewhere, but it's in local cache. The volume has been moved.
		moved = true
	}

	vol := s.constructVolume(name, moved)

	return vol, config, moved, nil
}

func (s *volumeStore) list() ([]*model.Volume, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	inRancher, err := s.getVolumesFromRancher()
	if err != nil {
		return nil, fmt.Errorf("Couldn't obtain list of volumes from Rancher. Error: %v", err)
	}

	inLocalCache, err := s.getVolumesInLocalCache()
	if err != nil {
		return nil, fmt.Errorf("Couldn't obtain list of volumes from local cache. Error: %v", err)
	}

	v := make([]*model.Volume, len(inRancher))
	idx := 0
	for name := range inRancher {
		vol := s.constructVolume(name, false)
		v[idx] = vol
		idx++
	}

	// These volumes are in our local cache but Rancher says they've moved, so we'll report them with a mountpoint of "moved"
	for name := range inLocalCache {
		if _, alsoInRancher := inRancher[name]; !alsoInRancher {
			vol := s.constructVolume(name, true)
			v = append(v, vol)
		}
	}

	return v, nil
}

func (s *volumeStore) constructVolume(name string, moved bool) *model.Volume {
	vol := &model.Volume{
		Name: name,
	}

	if moved {
		vol.Mountpoint = "moved"
	} else {
		mp := mountPoint(s.rootDir, name)
		if _, err := os.Stat(mp); err == nil {
			vol.Mountpoint = mp
		}
	}

	return vol
}

func (s *volumeStore) getVolumesInLocalCache() (map[string]bool, error) {
	volumes := map[string]bool{}
	files, err := ioutil.ReadDir(filepath.Join(s.rootDir, localCacheDir))
	if err != nil {
		return nil, err
	}

	for _, f := range files {
		name := f.Name()
		if _, ok := volumes[name]; !ok {
			volumes[name] = false
		}
	}

	return volumes, nil
}

func (s *volumeStore) getVolumesFromRancher() (map[string]volumeConfig, error) {
	// TODO We could cache the result for 5 or 10 seconds to reduce calls to metadata
	stacks, err := s.metadata.GetStacks()
	if err != nil {
		return nil, err
	}

	if s.hostUUID == "" {
		con, err := s.metadata.GetSelfContainer()
		if err != nil {
			return nil, err
		}
		s.hostUUID = con.HostUUID
	}
	volumes := map[string]volumeConfig{}
	for _, stack := range stacks {
		if strings.HasPrefix(stack.Name, volumeStackPrefix) {
			for _, service := range stack.Services {
				if service.Name == "controller" {
					for _, container := range service.Containers {
						if lhmd := service.Metadata["volume"]; lhmd != nil && container.HostUUID == s.hostUUID {
							if m, ok := lhmd.(map[string]interface{}); ok {
								if name, ok := m["volume_name"].(string); ok && name != "" {
									config, ok := m["volume_config"]
									if !ok {
										logrus.Warnf("Volume %v doesn't have config. Won't list as a volume.", name)
										continue
									}
									m, ok := config.(map[string]interface{})

									if !ok {
										logrus.Warnf("Volume %v's config isn't a map. Won't list as a volume.", name)
										continue
									}

									if len(m) == 0 {
										logrus.Warnf("Volume %v's config is empty. Won't list as a volume.", name)
										continue

									}

									var volumeConfig = volumeConfig{}
									if err := mapstructure.Decode(m, &volumeConfig); err != nil {
										logrus.Errorf("Error unmarshalling volume config for %v: %v. Won't list as a volume", name, err)
										continue
									}

									volumes[name] = volumeConfig
								}
							}
						}
					}
				}
			}
		}
	}
	return volumes, nil
}

type volumeConfig struct {
	Name             string `json:"name,omitempty" mapstructure:"name"`
	Size             string `json:"size,omitempty" mapstructure:"size"`
	SizeGB           string `json:"sizeGB,omitempty" mapstructure:"sizeGB"`
	ReplicaBaseImage string `json:"replicaBaseImage,omitempty" mapstructure:"replicaBaseImage"`
	DontFormat       bool   `json:"dontFormat,omitempty" mapstructure:"dontFormat"`
}

func (v volumeConfig) Json() string {
	j, err := json.Marshal(v)
	if err != nil {
		logrus.Errorf("Error marshalling volume config %v: %v", v, err)
		return ""
	}

	return string(j)
}
