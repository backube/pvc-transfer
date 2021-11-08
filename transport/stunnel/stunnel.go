package stunnel

import "github.com/backube/pvc-transfer/transport"

const (
	defaultStunnelImage = "quay.io/konveyor/rsync-transfer:latest"
	stunnelConfig       = "stunnel-config"
	stunnelSecret       = "stunnel-credentials"
)

const (
	TransportTypeStunnel = "stunnel"
	Container            = "stunnel"
)

func getImage(options *transport.Options) string {
	if options.Image == "" {
		return defaultStunnelImage
	} else {
		return options.Image
	}
}
