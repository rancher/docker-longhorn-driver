package flex

import (
	"os"
	"path/filepath"

	"github.com/Sirupsen/logrus"
	"github.com/mitchellh/mapstructure"
	"github.com/rancher/docker-longhorn-driver/driver"
	"github.com/rancher/docker-longhorn-driver/model"
	"github.com/rancher/docker-longhorn-driver/util"
	flexvol "github.com/rancher/rancher-flexvol"
)

const dontFormat = ".dont-format"

type RancherFlexDriver struct {
	daemonClient *driver.StorageDaemon
}

func New(sd *driver.StorageDaemon) *RancherFlexDriver {
	return &RancherFlexDriver{
		daemonClient: sd,
	}
}

func (r *RancherFlexDriver) Init() error {
	return nil
}

func (r *RancherFlexDriver) Create(options map[string]interface{}) (map[string]interface{}, error) {
	name, _ := options["name"].(string)
	logrus.Infof("Flexvol create: %s %v", name, options)

	v, err := r.daemonClient.Create(&model.Volume{
		Name: name,
		Opts: util.MapInterfaceToMapString(options),
	})

	return util.MapStringToMapInterface(v.Opts), err
}

func (r *RancherFlexDriver) Delete(options map[string]interface{}) error {
	name, _ := options["name"].(string)
	logrus.Infof("Flexvol remove: %s", name)
	return r.daemonClient.Delete(name, false)
}

func (r *RancherFlexDriver) Attach(options map[string]interface{}) (string, error) {
	name, _ := options["name"].(string)
	return r.daemonClient.WaitForDevice(name)
}

func (r *RancherFlexDriver) Detach(string) error {
	return nil
}

func (r *RancherFlexDriver) Mount(mountDir string, device string, options map[string]interface{}) error {
	config, err := toVolumeConfig(options)
	if err != nil {
		return err
	}

	if config.DontFormat {
		f, err := os.Create(filepath.Join(mountDir, dontFormat))
		if err != nil {
			return err
		}
		return f.Close()
	}

	return flexvol.ErrNotSupported
}

func (r *RancherFlexDriver) Unmount(mountDir string) error {
	if _, err := os.Stat(filepath.Join(mountDir, dontFormat)); err == nil {
		return nil
	}
	return flexvol.ErrNotSupported
}

func toVolumeConfig(data map[string]interface{}) (driver.VolumeConfig, error) {
	config := driver.VolumeConfig{}
	err := mapstructure.Decode(data, &config)
	return config, err
}
