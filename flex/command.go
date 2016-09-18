package flex

import (
	"os"

	"github.com/codegangsta/cli"
	"github.com/pkg/errors"

	rancherClient "github.com/rancher/go-rancher/client"
	flexvol "github.com/rancher/rancher-flexvol"

	"os/exec"

	"github.com/rancher/docker-longhorn-driver/driver"
	"github.com/rancher/docker-longhorn-driver/util"
)

var Command = cli.Command{
	Name:   "flexdriver",
	Usage:  "Start the flexvol volume driver",
	Action: start,
}

func handleErr(message string, err error) {
	if err != nil {
		err = errors.Wrap(err, message)
		flexvol.Error(err).Print()
		os.Exit(1)
	}
}

func start(c *cli.Context) {
	client, err := rancherClient.NewRancherClient(&rancherClient.ClientOpts{
		Url:       c.GlobalString("cattle-url"),
		AccessKey: c.GlobalString("cattle-access-key"),
		SecretKey: c.GlobalString("cattle-secret-key"),
	})
	handleErr("Failed to establish connection to Rancher server", err)

	md, err := util.GetMetadataConfig(c.GlobalString("metadata-url"))
	handleErr("Unable to get metadata:", err)

	cmd := exec.Command("mount", "--rbind", "/host/dev", "/dev")
	if err := cmd.Run(); err != nil {
		handleErr("Couldn't mount /dev:", err)
	}

	sd, err := driver.NewStorageDaemon(md.ContainerName, md.DriverName, md.Image, client)
	handleErr("Error creating storage daemon:", err)

	if err := flexvol.NewApp(New(sd)).Run(os.Args); err != nil {
		// Don't print error, it could mess up things
		os.Exit(1)
	}
}
