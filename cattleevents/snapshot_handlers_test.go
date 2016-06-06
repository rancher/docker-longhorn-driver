package cattleevents

import (
	"encoding/json"
	"testing"

	revents "github.com/rancher/go-machine-service/events"
)

func TestEvent(t *testing.T) {
	event := createEvent(t)

	snapshotData := &eventSnapshot{}
	err := decodeEvent(event, "snapshot", snapshotData)
	if err != nil {
		t.Fatal(err)
	}

	if snapshotData.Volume.Name != "expected-vol-name" || snapshotData.Volume.UUID != "expected-vol-uuid" {
		t.Fatalf("Unexpected: %v %v", snapshotData.Volume.Name, snapshotData.Volume.UUID)
	}
}

func createEvent(t *testing.T) *revents.Event {
	eventJSON := []byte(`{"name":"asdf",
	"data":{"snapshot":{"name":"","id":3,"state":"creating","description":"","created":1464108273000,"data":{},"accountId":5,
	"volumeId":126,"kind":"snapshot","removed":"","uuid":"snap-uuid", "type":"snapshot",
	"volume":{"name":"expected-vol-name","id":126,"state":"active","created":1464107700000,
	"data":{"fields":{"driver":"longhorn","capabilities":["snapshot"]}},"accountId":5,"kind":"volume","zoneId":1,"hostId":1,
	"uuid":"expected-vol-uuid","externalId":"vd1","accessMode":"singleHostRW",
	"allocationState":"active","type":"volume"}}
	}}`)
	event := &revents.Event{}
	err := json.Unmarshal(eventJSON, event)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return event
}
