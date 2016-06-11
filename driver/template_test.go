package driver

import (
	"bytes"
	"fmt"
	"testing"
)

func TestTemplateParses(t *testing.T) {
	volConf := volumeConfig{
		Name:             "foo",
		SizeGB:           "11GB",
		ReadIOPS:         "11000",
		WriteIOPS:        "10000",
		ReplicaBaseImage: "rancher/vm-ubuntu",
	}
	dockerCompose := new(bytes.Buffer)
	if err := composeTemplate.Execute(dockerCompose, volConf); err != nil {
		t.Fatalf("Error while executing template %v", err)
	}
	fmt.Printf("%s", dockerCompose)

	fmt.Println("\n\n-----------\b\b")
	volConf.ReplicaBaseImage = ""
	dockerCompose = new(bytes.Buffer)
	if err := composeTemplate.Execute(dockerCompose, volConf); err != nil {
		t.Fatalf("Error while executing template %v", err)
	}
	fmt.Printf("%s", dockerCompose)
}
