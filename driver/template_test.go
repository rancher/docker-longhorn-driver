package driver

import (
	"bytes"
	"testing"
)

func TestTemplateParses(t *testing.T) {
	volConf := volumeConfig{}
	dockerCompose := new(bytes.Buffer)
	if err := composeTemplate.Execute(dockerCompose, volConf); err != nil {
		t.Fatalf("Error while executing template %v", err)
	}
}
