package util

import (
	"fmt"
	"os/exec"
	"strconv"
	"time"

	"github.com/docker/go-units"

	"github.com/rancher/go-rancher-metadata/metadata"
)

const DevDir = "/dev/longhorn"

var (
	cmdTimeout time.Duration = time.Minute // one minute by default
)

type MetadataConfig struct {
	DriverName    string
	Image         string
	ContainerName string
}

func GetMetadataConfig(metadataUrl string) (MetadataConfig, error) {
	config := MetadataConfig{}
	client, err := metadata.NewClientAndWait(metadataUrl)
	if err != nil {
		return config, err
	}

	stack, err := client.GetSelfStack()
	if err != nil {
		return config, err
	}
	config.DriverName = stack.Name

	svc, err := client.GetSelfService()
	if err != nil {
		return config, err
	}
	if image, ok := svc.Metadata["VOLUME_STACK_IMAGE"]; ok {
		config.Image = fmt.Sprintf("%v", image)
	}

	c, err := client.GetSelfContainer()
	if err != nil {
		return config, err
	}
	config.ContainerName = c.UUID

	return config, nil
}

func ConstructSocketNameInContainer(driverName string) string {
	return fmt.Sprintf("/host/var/run/%v.sock", driverName)
}

func ConstructSocketNameOnHost(driverName string) string {
	return fmt.Sprintf("/var/run/%v.sock", driverName)
}

func Execute(binary string, args []string) (string, error) {
	var output []byte
	var err error
	cmd := exec.Command(binary, args...)
	done := make(chan struct{})

	go func() {
		output, err = cmd.CombinedOutput()
		done <- struct{}{}
	}()

	select {
	case <-done:
	case <-time.After(cmdTimeout):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return "", fmt.Errorf("Timeout executing: %v %v, output %v, error %v", binary, args, string(output), err)
	}

	if err != nil {
		return "", fmt.Errorf("Failed to execute: %v %v, output %v, error %v", binary, args, string(output), err)
	}
	return string(output), nil
}

func ConvertSize(size string) (string, string, error) {
	if size == "" {
		return "", "", nil
	}

	sizeInBytes, err := units.RAMInBytes(size)
	if err != nil {
		return "", "", err
	}

	gbSize := sizeInBytes / units.GiB
	if gbSize < 1 && sizeInBytes != 0 {
		gbSize = 1
	}
	return strconv.FormatInt(sizeInBytes, 10), strconv.FormatInt(gbSize, 10), nil

}
