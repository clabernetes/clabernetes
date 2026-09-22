package directpod

import k8scorev1 "k8s.io/api/core/v1"

const (
	// StartupGateContainerName identifies the optional per-host admission init container.
	StartupGateContainerName = "startup-gate"
	// StartupAdmittedAnnotation holds the admitted Pod UID, never a template-wide grant.
	StartupAdmittedAnnotation = "c9s.run/startup-host-admitted"
)

func startupGate(image string) (k8scorev1.Container, k8scorev1.Volume) {
	const directory = "/var/run/c9s-startup"

	return k8scorev1.Container{
			Name: StartupGateContainerName, Image: image,
			Command: []string{runtimeBinaryPath, runtimeCommandName, "startup-gate"},
			Args:    []string{"--directory", directory},
			VolumeMounts: []k8scorev1.VolumeMount{{
				Name: StartupGateContainerName, MountPath: directory, ReadOnly: true,
			}},
		}, k8scorev1.Volume{
			Name: StartupGateContainerName,
			VolumeSource: k8scorev1.VolumeSource{DownwardAPI: &k8scorev1.DownwardAPIVolumeSource{
				Items: []k8scorev1.DownwardAPIVolumeFile{
					{
						Path:     "uid",
						FieldRef: &k8scorev1.ObjectFieldSelector{FieldPath: "metadata.uid"},
					},
					{Path: "admitted", FieldRef: &k8scorev1.ObjectFieldSelector{
						FieldPath: "metadata.annotations['" + StartupAdmittedAnnotation + "']",
					}},
				},
			}},
		}
}
