#!/bin/bash

cd $(dirname $0)

cat > template.go << EOF
package driver

const (
	DockerComposeTemplate = \`
$(<docker-compose.yml.tmpl)
\`
)
EOF
