package volumeplugin

import (
	"fmt"

	"github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	"github.com/docker/go-plugins-helpers/volume"

	rancherClient "github.com/rancher/go-rancher/client"

	"github.com/rancher/docker-longhorn-driver/driver"
	"github.com/rancher/docker-longhorn-driver/util"
	"io/ioutil"
	"os/exec"
)

var Command = cli.Command{
	Name:   "volumedriver",
	Usage:  "Start the docker volume driver",
	Action: start,
}

func start(c *cli.Context) {
	client, err := rancherClient.NewRancherClient(&rancherClient.ClientOpts{
		Url:       c.GlobalString("cattle-url"),
		AccessKey: c.GlobalString("cattle-access-key"),
		SecretKey: c.GlobalString("cattle-secret-key"),
	})
	if err != nil {
		logrus.Fatalf("Failed to establish connection to Rancher server")
	}

	md, err := util.GetMetadataConfig(c.GlobalString("metadata-url"))
	if err != nil {
		logrus.Fatalf("Unable to get metadata: %v", err)
	}

	cmd := exec.Command("mount", "--rbind", "/host/dev", "/dev")
	if err := cmd.Run(); err != nil {
		logrus.Fatalf("Couldn't mount /dev: %v", err)
	}

	sockAddr := fmt.Sprintf("unix://%v", util.ConstructSocketNameOnHost(md.DriverName))
	err = ioutil.WriteFile(fmt.Sprintf("/etc/docker/plugins/%v.spec", md.DriverName), []byte(sockAddr), 0644)
	if err != nil {
		logrus.Fatalf("Unable to write spec file: %v", err)
	}

	sd, err := driver.NewStorageDaemon(md.ContainerName, md.DriverName, md.Image, client)
	if err != nil {
		logrus.Fatalf("Error creating storage daemon: %v", err)
	}

	go func() {
		err := sd.ListenAndServe()
		logrus.Fatalf("API Server exited with error: %v.", err)
	}()

	d := NewRancherStorageDriver(sd)
	h := volume.NewHandler(d)
	err = h.ServeUnix("root", util.ConstructSocketNameInContainer(md.DriverName))
	if err != nil {
		logrus.Fatalf("Volume server returned with error: %v", err)
	}
}
