package driver

import (
	"fmt"
	"time"

	"github.com/rancher/docker-longhorn-driver/util"
	rancherClient "github.com/rancher/go-rancher/client"
)

func WaitFor(client *rancherClient.RancherClient, resource *rancherClient.Resource, output interface{}, transitioning func() string) error {
	return util.Backoff(5*time.Minute, fmt.Sprintf("Failed waiting for %s:%s", resource.Type, resource.Id), func() (bool, error) {
		err := client.Reload(resource, output)
		if err != nil {
			return false, err
		}
		if transitioning() != "yes" {
			return true, nil
		}
		return false, nil
	})
}

func WaitService(client *rancherClient.RancherClient, service *rancherClient.Service) error {
	return WaitFor(client, &service.Resource, service, func() string {
		return service.Transitioning
	})
}

func WaitEnvironment(client *rancherClient.RancherClient, env *rancherClient.Environment) error {
	return WaitFor(client, &env.Resource, env, func() string {
		return env.Transitioning
	})
}
