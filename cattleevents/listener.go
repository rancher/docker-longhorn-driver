package cattleevents

import (
	"github.com/Sirupsen/logrus"
	"github.com/mitchellh/mapstructure"

	revents "github.com/rancher/go-machine-service/events"
	"github.com/rancher/go-rancher/client"

	"fmt"
	"github.com/rancher/docker-longhorn-driver/driver"
	"io/ioutil"
	"net/http"
)

const (
	deleteUrl = "http://driver/v1/volumes/%s"
)

func ConnectToEventStream(conf Config) error {
	logrus.Infof("Listening for cattle events")

	nh := noopHandler{}
	ph := PingHandler{}
	vh := &volumeDeleteHandler{}

	eventHandlers := map[string]revents.EventHandler{
		"storage.volume.activate":   nh.Handler,
		"storage.volume.deactivate": nh.Handler,
		"storage.volume.remove":     vh.Handler,
		"ping":                      ph.Handler,
	}

	router, err := revents.NewEventRouter("", 0, conf.CattleURL, conf.CattleAccessKey, conf.CattleSecretKey, nil, eventHandlers, "", conf.WorkerCount)
	if err != nil {
		return err
	}
	err = router.StartWithoutCreate(nil)
	return err
}

type volumeDeleteHandler struct {
	daemon *driver.StorageDaemon
}

func (h *volumeDeleteHandler) Handler(event *revents.Event, cli *client.RancherClient) error {
	logrus.Infof("Received event: Name: %s, Event Id: %s, Resource Id: %s", event.Name, event.ID, event.ResourceID)

	vspm := &struct {
		VSPM struct {
			V struct {
				Name string `mapstructure:"name"`
			} `mapstructure:"volume"`
		} `mapstructure:"volumeStoragePoolMap"`
	}{}

	err := mapstructure.Decode(event.Data, &vspm)
	if err != nil {
		return fmt.Errorf("Cannot parse event %v. Error: %v", event, err)
	}

	name := vspm.VSPM.V.Name
	if name != "" {
		req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf(deleteUrl, name), nil)
		if err != nil {
			return fmt.Errorf("Error building delete request for %v: %v", name, err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("Error calling volume delete API for %v: %v", name, err)
		}

		if resp.StatusCode >= 300 {
			body, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("Unexpected repsonse code %v deleting %v. Body: %s", resp.StatusCode, name, body)
		}
	}

	return volumeReply(event, cli)
}

type noopHandler struct{}

func (h *noopHandler) Handler(event *revents.Event, cli *client.RancherClient) error {
	logrus.Infof("Received and ignoring event: Name: %s, Event Id: %s, Resource Id: %s", event.Name, event.ID, event.ResourceID)
	return volumeReply(event, cli)
}

type PingHandler struct {
}

func (h *PingHandler) Handler(event *revents.Event, cli *client.RancherClient) error {
	return nil
}

func volumeReply(event *revents.Event, cli *client.RancherClient) error {
	replyData := make(map[string]interface{})
	reply := newReply(event)
	reply.ResourceType = "volume"
	reply.ResourceId = event.ResourceID
	reply.Data = replyData
	logrus.Infof("Reply: %+v", reply)
	err := publishReply(reply, cli)
	if err != nil {
		return err
	}
	return nil
}

func newReply(event *revents.Event) *client.Publish {
	return &client.Publish{
		Name:        event.ReplyTo,
		PreviousIds: []string{event.ID},
	}
}

func publishReply(reply *client.Publish, apiClient *client.RancherClient) error {
	_, err := apiClient.Publish.Create(reply)
	return err
}

type Config struct {
	CattleURL       string
	CattleAccessKey string
	CattleSecretKey string
	WorkerCount     int
}
