package stunnel

import (
	"bytes"
	"context"

	"github.com/backube/pvc-transfer/transport"
	"github.com/backube/pvc-transfer/transport/tls/certs"
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
	Container                           = "stunnel"
)

func serverSecretNameSuffix() string {
	return "server-" + stunnelSecret
}

func clientSecretNameSuffix() string {
	return "client-" + stunnelSecret
}

func caBundleSecretNameSuffix() string {
	return "ca-bundle-" + stunnelSecret
}

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

func isSecretValid(ctx context.Context, c ctrlclient.Client, logger logr.Logger, key types.NamespacedName, suffix string) (bool, error) {
	secret := &corev1.Secret{}
	err := c.Get(ctx, types.NamespacedName{
		Namespace: key.Namespace,
		Name:      getResourceName(key, suffix),
	}, secret)
	switch {
	case k8serrors.IsNotFound(err):
		return false, nil
	case err != nil:
		return false, err
	}

	_, ok := secret.Data["tls.key"]
	if !ok {
		logger.Info("secret data missing key tls.key", "secret", types.NamespacedName{
			Namespace: key.Namespace,
			Name:      getResourceName(key, suffix),
		})
		return false, nil
	}

	crt, ok := secret.Data["tls.crt"]
	if !ok {
		logger.Info("secret data missing key tls.crt", "secret", types.NamespacedName{
			Namespace: key.Namespace,
			Name:      getResourceName(key, suffix),
		})
		return false, nil
	}

	ca, ok := secret.Data["ca.crt"]
	if !ok {
		logger.Info("secret data missing key ca.crt", "secret", types.NamespacedName{
			Namespace: key.Namespace,
			Name:      getResourceName(key, suffix),
		})
		return false, nil
	}

	return certs.VerifyCertificate(bytes.NewBuffer(ca), bytes.NewBuffer(crt))
}
