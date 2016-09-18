package main

import (
	"os"

	"github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"

	"github.com/rancher/kubernetes-agent/healthcheck"

	"github.com/rancher/docker-longhorn-driver/docker/volumeplugin"
	"github.com/rancher/docker-longhorn-driver/flex"
	"github.com/rancher/docker-longhorn-driver/storagepool"
)

const healthCheckPort = 10241

func main() {
	logrus.Info("Launching plugin")

	app := cli.NewApp()
	app.Name = "docker-longhorn-driver"
	app.Version = "0.1.0"
	app.Author = "Rancher Labs"
	app.Usage = "Docker volume plugin for Rancher Longhorn"

	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "debug, d",
			Usage: "enable debug logging level",
		},
		cli.StringFlag{
			Name:   "cattle-url",
			Usage:  "The URL endpoint to communicate with cattle server",
			EnvVar: "CATTLE_URL",
		},
		cli.StringFlag{
			Name:   "cattle-access-key",
			Usage:  "The access key required to authenticate with cattle server",
			EnvVar: "CATTLE_ACCESS_KEY",
		},
		cli.StringFlag{
			Name:   "cattle-secret-key",
			Usage:  "The secret key required to authenticate with cattle server",
			EnvVar: "CATTLE_SECRET_KEY",
		},
		cli.IntFlag{
			Name:  "healthcheck-interval",
			Value: 5000,
			Usage: "set the frequency of performing healthchecks",
		},
		cli.StringFlag{
			Name:  "metadata-url",
			Usage: "set the metadata url",
			Value: "http://rancher-metadata/2015-12-19",
		},
	}

	commands := []cli.Command{volumeplugin.Command, storagepool.Command, flex.Command}
	app.Commands = commands

	go func() {
		err := healthcheck.StartHealthCheck(healthCheckPort)
		logrus.Fatalf("Error while running healthcheck [%v]", err)
	}()
	app.Run(os.Args)

}
