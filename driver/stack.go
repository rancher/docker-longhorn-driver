package driver

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	rancherClient "github.com/rancher/go-rancher/client"
)

const (
	retryInterval          = 2 * time.Second
	retryMax               = 200
	composeAffinityLabel   = "io.rancher.scheduler.affinity:container"
	composeVolumeName      = "VOLUME_NAME"
	composeVolumeSize      = "VOLUME_SIZE"
	composeDriverContainer = "LONGHORN_DRIVER_CONTAINER"
	composeLonghornImage   = "LONGHORN_IMAGE"
)

type stack struct {
	rancherClient       *rancherClient.RancherClient
	externalId          string
	name                string
	environment         map[string]interface{}
	template            string
	driverContainerName string
}

func newStack(volumeName, driverName, driverContainerName, image string, size string, rancherClient *rancherClient.RancherClient) *stack {
	env := map[string]interface{}{
		composeLonghornImage:   image,
		composeVolumeName:      volumeName,
		composeVolumeSize:      size,
		composeDriverContainer: driverContainerName,
	}

	return &stack{
		rancherClient:       rancherClient,
		name:                volumeStackPrefix + strings.Replace(volumeName, "_", "-", -1),
		externalId:          "system://longhorn?name=" + volumeName,
		template:            DockerComposeTemplate,
		environment:         env,
		driverContainerName: driverContainerName,
	}
}

func (s *stack) create() (*rancherClient.Environment, error) {
	env, err := s.find()
	if err != nil {
		return nil, err
	}

	config := &rancherClient.Environment{
		Name:          s.name,
		ExternalId:    s.externalId,
		Environment:   s.environment,
		DockerCompose: s.template,
		StartOnCreate: true,
	}

	if env == nil {
		env, err = s.rancherClient.Environment.Create(config)
		if err != nil {
			return nil, err
		}
	}

	if err := WaitEnvironment(s.rancherClient, env); err != nil {
		return nil, err
	}

	if err := s.waitForServices(env, "active"); err != nil {
		logrus.Debugf("Failed waiting services to be ready to launch. Cleaning up %v", env.Name)
		if err := s.rancherClient.Environment.Delete(env); err != nil {
			return nil, err
		}
	}

	return env, nil
}

func (s *stack) delete() error {
	env, err := s.find()
	if err != nil || env == nil {
		return err
	}

	if err := s.rancherClient.Environment.Delete(env); err != nil {
		return err
	}

	return WaitEnvironment(s.rancherClient, env)
}

func (s *stack) find() (*rancherClient.Environment, error) {
	envs, err := s.rancherClient.Environment.List(&rancherClient.ListOpts{
		Filters: map[string]interface{}{
			"name":         s.name,
			"externalId":   s.externalId,
			"removed_null": nil,
		},
	})
	if err != nil {
		return nil, err
	}
	if len(envs.Data) == 0 {
		return nil, nil
	}
	if len(envs.Data) > 1 {
		// This really shouldn't ever happen
		return nil, fmt.Errorf("More than one stack found for %s", s.name)
	}

	return &envs.Data[0], nil
}

func (s *stack) confirmControllerUpgrade(env *rancherClient.Environment) (*rancherClient.Service, error) {
	services, err := s.rancherClient.Service.List(&rancherClient.ListOpts{
		Filters: map[string]interface{}{
			"environmentId": env.Id,
			"name":          "controller",
		},
	})
	if err != nil {
		return nil, err
	}

	if len(services.Data) != 1 {
		return nil, errors.New("Failed to find controller service")
	}

	controller := &services.Data[0]
	if err := WaitService(s.rancherClient, controller); err != nil {
		return nil, err
	}

	if controller.State == "upgraded" {
		controller, err := s.rancherClient.Service.ActionFinishupgrade(controller)
		if err != nil {
			return nil, err
		}
		err = WaitService(s.rancherClient, controller)
		if err != nil {
			return nil, err
		}
	}

	return controller, nil
}

func (s *stack) moveController() error {
	env, err := s.find()
	if err != nil {
		return err
	}

	controller, err := s.confirmControllerUpgrade(env)
	if err != nil {
		return err
	}

	if controller.LaunchConfig.Labels[composeAffinityLabel] != s.driverContainerName {
		newLaunchConfig := controller.LaunchConfig
		newLaunchConfig.Labels[composeAffinityLabel] = s.driverContainerName

		logrus.Infof("Moving controller to next to container %s", s.driverContainerName)
		controller, err = s.rancherClient.Service.ActionUpgrade(controller, &rancherClient.ServiceUpgrade{
			InServiceStrategy: &rancherClient.InServiceUpgradeStrategy{
				LaunchConfig: newLaunchConfig,
			},
		})
		if err != nil {
			return err
		}
		if _, err := s.confirmControllerUpgrade(env); err != nil {
			return err
		}
	}

	return nil
}

func (s *stack) waitForServices(env *rancherClient.Environment, targetState string) error {
	var serviceCollection rancherClient.ServiceCollection
	ready := false

	if err := s.rancherClient.GetLink(env.Resource, "services", &serviceCollection); err != nil {
		return err
	}
	targetServiceCount := len(serviceCollection.Data)

	for i := 0; !ready && i < retryMax; i++ {
		logrus.Debugf("Waiting for %v services in %v turn to %v state", targetServiceCount, env.Name, targetState)
		time.Sleep(retryInterval)
		if err := s.rancherClient.GetLink(env.Resource, "services", &serviceCollection); err != nil {
			return err
		}
		services := serviceCollection.Data
		if len(services) != targetServiceCount {
			continue
		}
		incorrectState := false
		for _, service := range services {
			if service.State != targetState {
				incorrectState = true
				break
			}
		}
		if incorrectState {
			continue
		}
		ready = true
	}
	if !ready {
		return fmt.Errorf("Failed to wait for %v services in %v turn to %v state", targetServiceCount, env.Name, targetState)
	}
	logrus.Debugf("Services change state to %v in %v", targetState, env.Name)
	return nil
}

func (s *stack) waitActive(service *rancherClient.Service) (*rancherClient.Service, error) {
	err := WaitService(s.rancherClient, service)
	if err != nil || service.State != "upgraded" {
		return service, err
	}

	if _, err := s.rancherClient.Service.ActionFinishupgrade(service); err != nil {
		return nil, err
	}

	if err := WaitService(s.rancherClient, service); err != nil {
		return nil, err
	}

	if service.State != "active" {
		return nil, fmt.Errorf("Service %s is not active, got %s", service.Id, service.State)
	}

	return service, nil
}
