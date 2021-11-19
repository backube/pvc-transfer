package stunnel

import (
	"bytes"
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

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

func reconcileCertificateSecrets(ctx context.Context,
	c ctrlclient.Client,
	key types.NamespacedName,
	options *transport.Options,
	crtBundle *certs.CertificateBundle) error {
	crtBundleSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getResourceName(key, caBundleSecretNameSuffix()),
			Namespace: key.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, c, crtBundleSecret, func() error {
		crtBundleSecret.Labels = options.Labels
		crtBundleSecret.OwnerReferences = options.Owners

		crtBundleSecret.Data = map[string][]byte{
			"server.crt": crtBundle.ServerCrt.Bytes(),
			"server.key": crtBundle.ServerKey.Bytes(),
			"client.crt": crtBundle.ClientCrt.Bytes(),
			"client.key": crtBundle.ClientKey.Bytes(),
			"ca.crt":     crtBundle.CACrt.Bytes(),
			"ca.key":     crtBundle.CAKey.Bytes(),
		}
		return nil
	})
	if err != nil {
		return err
	}

	serverSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getResourceName(key, serverSecretNameSuffix()),
			Namespace: key.Namespace,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, c, serverSecret, func() error {
		serverSecret.Labels = options.Labels
		serverSecret.OwnerReferences = options.Owners

		serverSecret.Data = map[string][]byte{
			"tls.crt": crtBundle.ServerCrt.Bytes(),
			"tls.key": crtBundle.ServerKey.Bytes(),
			"ca.crt":  crtBundle.CACrt.Bytes(),
		}
		return nil
	})
	if err != nil {
		return err
	}

	clientSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getResourceName(key, clientSecretNameSuffix()),
			Namespace: key.Namespace,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, c, clientSecret, func() error {
		clientSecret.Labels = options.Labels
		clientSecret.OwnerReferences = options.Owners

		clientSecret.Data = map[string][]byte{
			"tls.crt": crtBundle.ClientCrt.Bytes(),
			"tls.key": crtBundle.ClientKey.Bytes(),
			"ca.crt":  crtBundle.CACrt.Bytes(),
		}
		return nil
	})
	return err
}
