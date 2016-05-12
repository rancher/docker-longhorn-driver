package volumeplugin

import (
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/docker/go-plugins-helpers/volume"
	"github.com/rancher/docker-longhorn-driver/driver"
	"github.com/rancher/docker-longhorn-driver/model"
)

func NewRancherStorageDriver(sd *driver.StorageDaemon) *RancherStorageDriver {
	return &RancherStorageDriver{
		daemonClient: sd,
	}
}

type RancherStorageDriver struct {
	daemonClient *driver.StorageDaemon
}

func (d RancherStorageDriver) Create(request volume.Request) volume.Response {
	logrus.Infof("Docker create request: %v", request)

	v := &model.Volume{
		Name: request.Name,
		Opts: request.Options,
	}

	v, err := d.daemonClient.Create(v)
	if err != nil {
		return errorToResponse(err)
	}
	return volume.Response{}
}

func (d RancherStorageDriver) List(request volume.Request) volume.Response {
	logrus.Infof("Docker List request: %v", request)

	vols, err := d.daemonClient.List()
	if err != nil {
		return errorToResponse(err)
	}

	rVols := make([]*volume.Volume, len(vols))
	for i, v := range vols {
		rVols[i] = transformVolume(v)
	}

	return volume.Response{
		Volumes: rVols,
	}
}

func (d RancherStorageDriver) Get(request volume.Request) volume.Response {
	logrus.Infof("Docker Get request: %v", request)

	vol, err := d.daemonClient.Get(request.Name)
	if err != nil {
		return errorToResponse(err)
	}

	if vol == nil {
		return errorToResponse(fmt.Errorf("No such volume"))
	}

	return volToResponse(vol)
}

func (d RancherStorageDriver) Remove(request volume.Request) volume.Response {
	logrus.Infof("Docker Remove request: %v", request)
	err := d.daemonClient.Delete(request.Name, false)
	if err != nil {
		return errorToResponse(err)
	}

	return volume.Response{}
}

func (d RancherStorageDriver) Mount(request volume.Request) volume.Response {
	logrus.Infof("Docker Mount request: %v", request)
	vol, err := d.daemonClient.Mount(request.Name)
	if err != nil {
		return errorToResponse(err)
	}

	return volume.Response{
		Mountpoint: vol.Mountpoint,
	}
}

func (d RancherStorageDriver) Unmount(request volume.Request) volume.Response {
	logrus.Infof("Docker Unmount request: %v", request)
	err := d.daemonClient.Unmount(request.Name)
	if err != nil {
		return errorToResponse(err)
	}

	return volume.Response{}
}

func (d RancherStorageDriver) Path(request volume.Request) volume.Response {
	logrus.Infof("Docker Path request: %v", request)
	vol, err := d.daemonClient.Get(request.Name)
	if err != nil {
		return errorToResponse(err)
	}

	if vol == nil {
		return errorToResponse(fmt.Errorf("No such volume %v", request.Name))
	}

	// TODO Keep an eye on how docker interacts with this call. Not sure of the proper behavior here.
	if vol.Mountpoint == "moved" {
		vol.Mountpoint = ""
	}

	return volume.Response{
		Mountpoint: vol.Mountpoint,
	}
}

func errorToResponse(err error) volume.Response {
	logrus.Errorf("Error response: %v", err)
	return volume.Response{
		Err: err.Error(),
	}
}

func volToResponse(vol *model.Volume) volume.Response {
	logrus.Infof("Response: %v", vol)
	return volume.Response{
		Volume: transformVolume(vol),
	}
}

func transformVolume(vol *model.Volume) *volume.Volume {
	return &volume.Volume{
		Name:       vol.Name,
		Mountpoint: vol.Mountpoint,
	}
}
