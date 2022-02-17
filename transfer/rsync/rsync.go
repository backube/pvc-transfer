package rsync

import (
	"github.com/backube/pvc-transfer/transfer"
	corev1 "k8s.io/api/core/v1"
)

const (
	RsyncContainer = "rsync"
)

const (
	rsyncImage          = "quay.io/konveyor/rsync-transfer:latest"
	rsyncConfig         = "backube-rsync-config"
	rsyncSecretPrefix   = "backube-rsync"
	rsyncServiceAccount = "backube-rsync-sa"
	rsyncRole           = "backube-rsync-role"
	rsyncRoleBinding    = "backube-rsync-rolebinding"
	rsyncdLogDir        = "rsyncd-logs"
	rsyncdLogDirPath    = "/var/log/rsyncd/"
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
	for _, c := range podSpec.Containers {
		c.SecurityContext = &options.ContainerSecurityContext
		c.Resources = options.Resources
	}
}
