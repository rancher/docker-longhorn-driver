package util

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rancher/go-rancher-metadata/metadata"
)

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
	if image, ok := svc.Metadata["LONGHORN_IMAGE"]; ok {
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

func ParseSize(size string) (string, error) {
	if size == "" {
		return "", nil
	}
	size = strings.ToLower(size)
	readableSize := regexp.MustCompile(`^[0-9.]+[kmgt]$`)
	if !readableSize.MatchString(size) {
		return size, nil
	}

	last := len(size) - 1
	unit := string(size[last])
	value, err := strconv.ParseInt(size[:last], 10, 64)
	if err != nil {
		return "", err
	}

	kb := int64(1024)
	mb := 1024 * kb
	gb := 1024 * mb
	tb := 1024 * gb
	switch unit {
	case "k":
		value *= kb
	case "m":
		value *= mb
	case "g":
		value *= gb
	case "t":
		value *= tb
	default:
		return "", fmt.Errorf("Unrecongized size value %v", size)
	}
	return strconv.FormatInt(value, 10), err
}
