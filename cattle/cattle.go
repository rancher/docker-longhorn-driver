package cattle

import (
	"errors"

	log "github.com/Sirupsen/logrus"
	"github.com/rancher/docker-longhorn-driver/util"
	"github.com/rancher/go-rancher/client"
)

type CattleInterface interface {
	SyncStoragePool(string, []string) error
}

type CattleClient struct {
	rancherClient *client.RancherClient
}

func NewCattleClient(cattleUrl, cattleAccessKey, cattleSecretKey string) (*CattleClient, error) {
	if cattleUrl == "" {
		return nil, errors.New("cattle url is empty")
	}

	apiClient, err := client.NewRancherClient(&client.ClientOpts{
		Url:       cattleUrl,
		AccessKey: cattleAccessKey,
		SecretKey: cattleSecretKey,
	})

	if err != nil {
		return nil, err
	}

	return &CattleClient{
		rancherClient: apiClient,
	}, nil
}

func (c *CattleClient) SyncStoragePool(driver string, hostUuids []string) error {
	log.Debugf("storagepool event %v", hostUuids)
	sp := client.StoragePool{
		Name:             driver,
		ExternalId:       driver,
		DriverName:       driver,
		VolumeAccessMode: "singleHostRW",
		BlockDevicePath:  util.DevDir,
	}
	espe := &client.ExternalStoragePoolEvent{
		EventType:   "storagepool.create",
		HostUuids:   hostUuids,
		ExternalId:  driver,
		StoragePool: sp,
	}
	_, err := c.rancherClient.ExternalStoragePoolEvent.Create(espe)
	return err
}
