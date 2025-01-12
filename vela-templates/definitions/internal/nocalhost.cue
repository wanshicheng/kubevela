import (
	"encoding/json"
)

nocalhost: {
	type: "trait"
	annotations: {}
	labels: {}
	description: "nocalhost develop configuration."
	attributes: {
		podDisruptive: true
		appliesToWorkloads: ["*"]
	}
}

template: {
	patch: {
		metadata: annotations: {
			"dev.nocalhost/application-name":      context.appName
			"dev.nocalhost/application-namespace": context.namespace
			"dev.nocalhost":                       json.Marshal({
				"containers": [
					{
						"name": context.name
						"dev": {
							if parameter.gitUrl != _|_ {
								"gitUrl": parameter.gitUrl
							}
							"image":   parameter.image
							"shell":   parameter.shell
							"workDir": parameter.workDir
							if parameter.storageClass != _|_ {
								"storageClass": parameter.storageClass
							}
							"resources": {
								"limits":   parameter.resources.limits
								"requests": parameter.resources.requests
							}
							if parameter.persistentVolumeDirs != _|_ {
								persistentVolumeDirs: [
									for v in parameter.persistentVolumeDirs {
										path:     v.path
										capacity: v.capacity
									},
								]
							}
							if parameter.command != _|_ {
								"command": parameter.command
							}
							if parameter.debug != _|_ {
								"debug": parameter.debug
							}
							"hotReload": parameter.hotReload
							if parameter.sync != _|_ {
								sync: parameter.sync
							}
							if parameter.env != _|_ {
								env: [
									for v in parameter.env {
										name:  v.name
										value: v.value
									},
								]
							}
							if parameter.portForward != _|_ {
								"portForward": parameter.portForward
							}
						}
					},
				]
			})
		}
	}
	parameter: {
		gitUrl?:       string
		image:         string
		shell:         *"bash" | string
		workDir:       *"/home/nocalhost-dev" | string
		storageClass?: string
		command?: {
			run?: [...string]
			debug?: [...string]
		}
		debug?: {
			remoteDebugPort?: int
		}
		hotReload: *true | bool
		sync: {
			type: *"send" | string
			filePattern?: [...string]
			ignoreFilePattern?: [...string]
		}
		env?: [...{
			name:  string
			value: string
		}]
		portForward?: [...string]
		persistentVolumeDirs?: [...{
			path:     string
			capacity: string
		}]
		resources: {
			limits: {
				memory: *"2Gi" | string
				cpu:    *"2" | string
			}
			requests: {
				memory: *"512Mi" | string
				cpu:    *"0.5" | string
			}
		}
	}
}
