package stunnel

import (
	"bytes"
	"context"

	"github.com/backube/pvc-transfer/transport"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultStunnelImage = "quay.io/konveyor/rsync-transfer:latest"
	stunnelConfig       = "stunnel-config"
	stunnelSecret       = "stunnel-credentials"
)

const (
	TransportTypeStunnel transport.Type = "stunnel"
	Container            = "stunnel"
)

func getImage(options *transport.Options) string {
	if options.Image == "" {
		return defaultStunnelImage
	} else {
		return options.Image
	}
}

func getResourceName(obj types.NamespacedName, suffix string) string {
	return obj.Name + "-" + suffix
}

func getExistingCert(ctx context.Context, c ctrlclient.Client, logger logr.Logger, secretName types.NamespacedName, suffix string) (*bytes.Buffer, *bytes.Buffer, bool, error) {
	secret := &corev1.Secret{}
	err := c.Get(ctx, types.NamespacedName{
		Namespace: secretName.Namespace,
		Name:      getResourceName(secretName, suffix),
	}, secret)
	switch {
	case k8serrors.IsNotFound(err):
		return nil, nil, false, nil
	case err != nil:
		return nil, nil, false, err
	}

	key, ok := secret.Data["tls.key"]
	if !ok {
		logger.Info("secret data missing key tls.key", "secret", types.NamespacedName{
			Namespace: secretName.Namespace,
			Name:      getResourceName(secretName, suffix),
		})
		return nil, nil, false, nil
	}

	crt, ok := secret.Data["tls.crt"]
	if !ok {
		logger.Info("secret data missing key tls.crt", "secret", types.NamespacedName{
			Namespace: secretName.Namespace,
			Name:      getResourceName(secretName, suffix),
		})
		return nil, nil, false, nil
	}

	return bytes.NewBuffer(key), bytes.NewBuffer(crt), true, nil
}
