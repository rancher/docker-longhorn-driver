package storagepool

import (
	"github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"

	"github.com/rancher/docker-longhorn-driver/cattle"
	"github.com/rancher/docker-longhorn-driver/cattleevents"
	"github.com/rancher/docker-longhorn-driver/util"
)

var Command = cli.Command{
	Name:   "storagepool",
	Usage:  "Start convoy-agent as a storagepool agent",
	Action: start,
}

func start(c *cli.Context) {
	healthCheckInterval := c.GlobalInt("healthcheck-interval")

	cattleURL := c.GlobalString("cattle-url")
	cattleAccessKey := c.GlobalString("cattle-access-key")
	cattleSecretKey := c.GlobalString("cattle-secret-key")

	cattleClient, err := cattle.NewCattleClient(cattleURL, cattleAccessKey, cattleSecretKey)
	if err != nil {
		logrus.Fatal(err)
	}

	metadataURL := c.GlobalString("metadata-url")
	md, err := util.GetMetadataConfig(metadataURL)
	if err != nil {
		logrus.Fatalf("Unable to get metadata: %v", err)
	}

	resultChan := make(chan error)

	go func(rc chan error) {
		storagePoolAgent := NewStoragepoolAgent(healthCheckInterval, md.DriverName, cattleClient)
		err := storagePoolAgent.Run(metadataURL)
		logrus.Errorf("Error while running storage pool agent [%v]", err)
		rc <- err
	}(resultChan)

	go func(rc chan error) {
		conf := cattleevents.Config{
			CattleURL:       cattleURL,
			CattleAccessKey: cattleAccessKey,
			CattleSecretKey: cattleSecretKey,
			WorkerCount:     10,
		}
		err := cattleevents.ConnectToEventStream(conf)
		logrus.Errorf("Cattle event listener exited with error: %s", err)
		rc <- err
	}(resultChan)

	<-resultChan
}
