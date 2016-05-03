package driver

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/gorilla/mux"

	md "github.com/rancher/go-rancher-metadata/metadata"
	rancherClient "github.com/rancher/go-rancher/client"

	"github.com/rancher/docker-longhorn-driver/model"
	"github.com/rancher/docker-longhorn-driver/util"
	"net/http"
)

const (
	composeVolumeName      = "VOLUME_NAME"
	composeVolumeSize      = "VOLUME_SIZE"
	composeDriverContainer = "LONGHORN_DRIVER_CONTAINER"
	composeLonghornImage   = "LONGHORN_IMAGE"
	devDir                 = "/dev/longhorn/%s"
	root                   = "/var/lib/rancher/longhorn"
	mountsDir              = "mounts"
	localCacheDir          = "localcache"
	mountBin               = "mount"
	umountBin              = "umount"
	rancherMetadataURL     = "http://rancher-metadata/2015-12-19"
	volumeStackPrefix      = "longhorn-vol-"
	defaultVolumeSize      = "10737418240" // 10 gb
)

type VolumeManager interface {
	List() ([]model.Volume, error)
	Get(name string) (model.Volume, error)
	Create(volume model.Volume) (model.Volume, error)
	Delete(name string) error
	Mount(name string) (model.Volume, error)
	Unmount(name string) error
}

func NewStorageDaemon(daemonContainerName, driverName, image string, client *rancherClient.RancherClient) (*StorageDaemon, error) {
	metadata := md.NewClient(rancherMetadataURL)

	if err := os.MkdirAll(filepath.Join(root, localCacheDir), 0744); err != nil {
		return nil, fmt.Errorf("Couldn't create localcache dir. Error: %v", err)
	}

	volumeStore := &volumeStore{
		mutex:    &sync.RWMutex{},
		metadata: metadata,
	}

	sd := &StorageDaemon{
		daemonContainerName: daemonContainerName,
		driverName:          driverName,
		client:              client,
		metadata:            metadata,
		store:               volumeStore,
		image:               image,
	}

	return sd, nil
}

type StorageDaemon struct {
	mutex               *sync.RWMutex
	client              *rancherClient.RancherClient
	metadata            *md.Client
	store               *volumeStore
	daemonContainerName string
	driverName          string
	hostUUID            string
	image               string
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
	vol, moved, err := d.store.get(name)

	if moved {
		vol.Mountpoint = "moved"
	} else {

	}

	return vol, err
}

func (d *StorageDaemon) Create(volume *model.Volume) (*model.Volume, error) {
	logrus.Infof("Creating volume %v", volume)
	d.store.create(volume.Name)

	sizeStr := volume.Opts["size"]
	var size string
	if sizeStr == "" {
		size = defaultVolumeSize
	}
	size, err := util.ParseSize(sizeStr)
	if err != nil {
		logrus.Warnf("Can't parse size %v. Using default %v", sizeStr, defaultVolumeSize)
		size = defaultVolumeSize
	}

	stack := d.Stack(volume.Name, d.driverName, d.daemonContainerName, d.image, size)

	if err := d.doCreateVolume(volume, stack); err != nil {
		stack.Delete()
		return nil, fmt.Errorf("Error creating Rancher stack for volume %v: %v.", volume.Name, err)
	}

	return volume, nil
}

func (d *StorageDaemon) doCreateVolume(volume *model.Volume, stack *Stack) error {
	// Doing find just to see if we are creating versus using an existing stack
	env, err := stack.Find()
	if err != nil {
		return err
	}
	logrus.Infof("Found %v", env)

	// Always run create because it also ensures that things are active
	if _, err := stack.Create(); err != nil {
		return err
	}

	// If env was nil then we created stack so we need to format
	if env == nil {
		dev, _ := getDevice(volume.Name)
		if err := waitForDevice(dev); err != nil {
			return err
		}

		logrus.Infof("Formatting volume %v - %v", volume.Name, dev)
		if _, err := util.Execute("mkfs.ext4", []string{"-F", dev}); err != nil {
			return err
		}
	}

	if err := stack.MoveController(); err != nil {
		logrus.Errorf("Failed to move controller to %v: %v", d.daemonContainerName, err)
		return err
	}

	return nil
}

func (d *StorageDaemon) Delete(name string, removeStack bool) error {
	// This delete is a simple operation that just removes the volume from the local cache
	logrus.Infof("Deleting volume %v", name)
	if removeStack {
		stack := d.Stack(name, d.driverName, d.daemonContainerName, d.image, "0")
		if err := stack.Delete(); err != nil {
			return err
		}
	}
	d.store.delete(name)
	return nil
}

func (d *StorageDaemon) Mount(name string) (*model.Volume, error) {
	logrus.Infof("Mounting volume %v", name)

	vol, moved, err := d.store.get(name)
	if vol == nil {
		return nil, fmt.Errorf("No such volume: %v", name)
	}

	if moved {
		return nil, fmt.Errorf("Volume %v no longer reside on this host and cannot be mounted.", name)
	}

	dev, err := getDevice(vol.Name)
	if err != nil {
		return nil, err
	}

	if err := waitForDevice(dev); err != nil {
		return nil, err
	}

	mountPoint, err := volumeMount(vol)
	if err != nil {
		return nil, err
	}

	vol.Mountpoint = mountPoint
	return vol, nil
}

func (d *StorageDaemon) Unmount(name string) error {
	logrus.Infof("Unmounting volume %v", name)

	vol, moved, err := d.store.get(name)
	if err != nil {
		return fmt.Errorf("Error getting volume %v for unmount: %v.", vol, err)
	}

	// If volume doesn't exist or has been moved, just no-op and return successfully
	if vol == nil || moved {
		logrus.Infof("Umount called on a nonexistent or moved volume %v. No-op.", vol.Name)
		return nil
	}

	mountPoint := mountPoint(vol.Name)
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

func volumeMount(volume *model.Volume) (string, error) {
	dev, err := getDevice(volume.Name)
	if err != nil {
		return "", err
	}

	mountPoint := mountPoint(volume.Name)
	if err := os.MkdirAll(mountPoint, 0744); err != nil {
		return "", err
	}

	if !isMounted(mountPoint) {
		logrus.Infof("Mounting volume %v to %v.", volume.Name, mountPoint)
		_, err = callMount([]string{dev, mountPoint})
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

func mountPoint(volumeName string) string {
	return filepath.Join(root, mountsDir, volumeName)
}

func getDevice(volumeName string) (string, error) {
	return fmt.Sprintf(devDir, volumeName), nil
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

func (d *StorageDaemon) Stack(volumeName, driverName, daemonContainerName, image string, size string) *Stack {
	env := map[string]interface{}{
		composeLonghornImage:   image,
		composeVolumeName:      volumeName,
		composeVolumeSize:      size,
		composeDriverContainer: daemonContainerName,
	}

	return &Stack{
		Client:        d.client,
		Name:          volumeStackPrefix + strings.Replace(volumeName, "_", "-", -1),
		ExternalId:    "system://longhorn?name=" + volumeName,
		Template:      DockerComposeTemplate,
		Environment:   env,
		ContainerName: daemonContainerName,
	}
}

type volumeStore struct {
	mutex    *sync.RWMutex
	metadata *md.Client
	hostUUID string
}

func (s *volumeStore) create(name string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	file := filepath.Join(root, localCacheDir, name)
	if err := ioutil.WriteFile(file, []byte{}, 0644); err != nil {
		return fmt.Errorf("Couldn't write local cache record for %v. Error: %v", name, err)
	}
	return nil
}

func (s *volumeStore) delete(name string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	file := filepath.Join(root, localCacheDir, name)
	if err := os.Remove(file); err != nil {
		return fmt.Errorf("Couldn't remove local cache record for %v. Error: %v", name, err)
	}
	return nil
}

// Return values are the volume, a boolean `moved` whose value is true if the volume has been moved to a different
// host, and an error
func (s *volumeStore) get(name string) (*model.Volume, bool, error) {
	s.mutex.RLock()

	volumes, err := s.getVolumesFromRancher()
	if err != nil {
		s.mutex.RUnlock()
		return nil, false, fmt.Errorf("Couldn't obtain list of volumes from Rancher. Error: %v", err)
	}

	_, inRancher := volumes[name]

	localCache, err := s.getVolumesInLocalCache()
	if err != nil {
		s.mutex.RUnlock()
		return nil, false, fmt.Errorf("Couldn't obtain list of volumes from local cache. Error: %v", err)
	}

	_, inLocalCache := localCache[name]
	s.mutex.RUnlock()

	moved := false
	if !inRancher && !inLocalCache {
		// neither Rancher nor the local cache thinks this volume is on this host. It doesn't exist
		return nil, false, nil
	} else if inRancher && !inLocalCache {
		// Rancher says its on this host, but not in local cache, create entry
		s.create(name)
	} else if !inRancher && inLocalCache {
		// Rancher says its elsewhere, but it's in local cache. The volume has been moved.
		moved = true
	}

	vol := s.constructVolume(name, moved)

	return vol, moved, nil
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
		mp := mountPoint(name)
		if _, err := os.Stat(mp); err == nil {
			vol.Mountpoint = mp
		}
	}

	return vol
}

func (s *volumeStore) getVolumesInLocalCache() (map[string]bool, error) {
	volumes := map[string]bool{}
	files, err := ioutil.ReadDir(filepath.Join(root, localCacheDir))
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

func (s *volumeStore) getVolumesFromRancher() (map[string]bool, error) {
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
	volumes := map[string]bool{}
	for _, stack := range stacks {
		if strings.HasPrefix(stack.Name, volumeStackPrefix) {
			for _, service := range stack.Services {
				if service.Name == "controller" {
					for _, container := range service.Containers {
						if lhmd := service.Metadata["longhorn"]; lhmd != nil && container.HostUUID == s.hostUUID {
							if m, ok := lhmd.(map[string]interface{}); ok {
								if name, ok := m["volume_name"].(string); ok && name != "" {
									volumes[name] = true
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
