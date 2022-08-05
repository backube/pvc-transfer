package rsync

import (
	"github.com/backube/pvc-transfer/transfer"
	corev1 "k8s.io/api/core/v1"
)

const (
	RsyncContainer = "rsync"
)

const (
	rsyncImage                  = "quay.io/konveyor/rsync-transfer:latest"
	rsyncConfig                 = "rsync-config"
	rsyncServiceAccount         = "rsync-sa"
	rsyncRole                   = "rsync-role"
	rsyncCommunicationMountPath = "/usr/share/rsync"
	rsyncRoleBinding            = "rsync-rolebinding"
	rsyncdLogDir                = "rsyncd-logs"
	rsyncdLogDirPath            = "/var/log/rsyncd/"
)

// applyPodOptions take a PodSpec and PodOptions, applies
// each option to the given podSpec
// Following fields will be mutated:
// - spec.NodeSelector
// - spec.SecurityContext
// - spec.NodeName
// - spec.Containers[*].SecurityContext
// - spec.Containers[*].Resources
func applyPodOptions(podSpec *corev1.PodSpec, options transfer.PodOptions) {
	podSpec.NodeSelector = options.NodeSelector
	podSpec.NodeName = options.NodeName
	podSpec.SecurityContext = &options.PodSecurityContext
	for i := range podSpec.Containers {
		c := &podSpec.Containers[i]
		if options.Image != "" {
			c.Image = options.Image
		} else {
			c.Image = rsyncImage
		}
		c.SecurityContext = &options.ContainerSecurityContext
		c.Resources = options.Resources
	}
}

func getTerminationVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      "termination",
			MountPath: "/mnt/termination",
		},
	}
}

func getTerminationVolumes() []corev1.Volume {
	return []corev1.Volume{
		{
			Name: "termination",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}
}
