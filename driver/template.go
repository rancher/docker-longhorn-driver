package driver

const (
	DockerComposeTemplate = `
replica:
    scale: 2
    image: {{if .ReplicaBaseImage}}{{.ReplicaBaseImage}}{{else}}$IMAGE{{end}}
    entrypoint:
    {{if .ReplicaBaseImage -}}
    - /cmd/launch-replica-with-vm-backing-file
    {{else -}}
    - longhorn
    {{end -}}
    command:
    {{if not .ReplicaBaseImage -}}
    - replica
    {{end -}}
    - --listen
    - 0.0.0.0:9502
    - --sync-agent=false
    - /volume/$VOLUME_NAME
    volumes:
    - /volume/$VOLUME_NAME
    {{ if .ReplicaBaseImage -}}
    volumes_from:
    - replica-binary
    {{end -}}
    labels:
        io.rancher.sidekicks: replica-healthcheck, sync-agent{{if .ReplicaBaseImage}}, replica-binary{{end}}
        io.rancher.container.hostname_override: container_name
        io.rancher.scheduler.affinity:container_label_ne: io.rancher.stack_service.name=$${stack_name}/$${service_name}
        io.rancher.scheduler.affinity:container_soft: $DRIVER_CONTAINER
        io.rancher.scheduler.disksize.{{.Name}}: {{.SizeGB}}
    metadata:
        volume:
            volume_name: $VOLUME_NAME
            volume_size: $VOLUME_SIZE
    health_check:
        healthy_threshold: 1
        unhealthy_threshold: 4
        interval: 5000
        port: 8199
        request_line: GET /replica/status HTTP/1.0
        response_timeout: 50000
        initializing_timeout: 10000
        reinitializing_timeout: 20000
        strategy: recreateOnQuorum
        recreate_on_quorum_strategy_config:
            quorum: 1

{{- if .ReplicaBaseImage}}

replica-binary:
    image: $IMAGE
    net: none
    command: copy-binary
    volumes:
    - /cmd
    labels:
        io.rancher.container.start_once: true
{{- end}}

sync-agent:
    image: $IMAGE
    net: container:replica
    working_dir: /volume/$VOLUME_NAME
    volumes_from:
    - replica
    command:
    - longhorn
    - sync-agent
    - --listen
    - 0.0.0.0:9504

replica-healthcheck:
    image: $IMAGE
    net: container:replica
    metadata:
        volume:
            volume_name: $VOLUME_NAME
            volume_size: $VOLUME_SIZE
    command:
    - longhorn-agent
    - --replica

controller:
    image: $IMAGE
    command:
    - launch
    - controller
    - --listen
    - 0.0.0.0:9501
    - --frontend
    - tcmu
    - $VOLUME_NAME
    privileged: true
    volumes:
    - /dev:/host/dev
    - /lib/modules:/lib/modules:ro
    labels:
        io.rancher.sidekicks: controller-agent
        io.rancher.container.hostname_override: container_name
        io.rancher.scheduler.affinity:container: $DRIVER_CONTAINER
    metadata:
        volume:
          volume_name: $VOLUME_NAME
          volume_config: {{.Json}}
    health_check:
        healthy_threshold: 1
        unhealthy_threshold: 2
        interval: 5000
        port: 8199
        request_line: GET /controller/status HTTP/1.0
        response_timeout: 5000
        strategy: none

controller-agent:
    image: $IMAGE
    net: container:controller
    metadata:
        volume:
          volume_name: $VOLUME_NAME
    command:
    - longhorn-agent
    - --controller
`
)
