package stunnel

import (
	"bytes"
	"context"
	"fmt"

	"github.com/backube/pvc-transfer/internal/utils"
	"github.com/backube/pvc-transfer/transport"
	"github.com/backube/pvc-transfer/transport/tls/certs"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	defaultStunnelImage = "quay.io/konveyor/rsync-transfer:latest"
	stunnelConfig       = "stunnel-config"
	stunnelSecret       = "stunnel-creds"
)

const (
	CredentialsTypePSK transport.CredentialsType = "PSK"
	CredentialsTypeTLS transport.CredentialsType = "TLS"
)

const (
	TransportTypeStunnel transport.Type = "stunnel"
	Container                           = "stunnel"
)

func getImage(options *transport.Options) string {
	if options.Image == "" {
		return defaultStunnelImage
	} else {
		return options.Image
	}
}

func getResourceName(obj types.NamespacedName, component, prefix string) string {
	resourceName := fmt.Sprintf("%s-%s-%s", prefix, component, obj.Name)
	if len(resourceName) > 62 {
		return resourceName[:62]
	}
	return resourceName
}

func isTLSSecretValid(ctx context.Context, c ctrlclient.Client, logger logr.Logger, secretRef types.NamespacedName) (bool, error) {
	secret := &corev1.Secret{}
	err := c.Get(ctx, secretRef, secret)
	switch {
	case k8serrors.IsNotFound(err):
		return false, nil
	case err != nil:
		return false, err
	}

	_, ok := secret.Data["client.key"]
	if !ok {
		logger.Info("secret data missing key client.key", "secret", secretRef)
		return false, nil
	}

	_, ok = secret.Data["server.key"]
	if !ok {
		logger.Info("secret data missing key server.key", "secret", secretRef)
		return false, nil
	}

	clientCrt, ok := secret.Data["client.crt"]
	if !ok {
		logger.Info("secret data missing key client.crt", "secret", secretRef)
		return false, nil
	}

	serverCrt, ok := secret.Data["server.crt"]
	if !ok {
		logger.Info("secret data missing key server.crt", "secret", secretRef)
		return false, nil
	}

	ca, ok := secret.Data["ca.crt"]
	if !ok {
		logger.Info("secret data missing key ca.crt", "secret", secretRef)
		return false, nil
	}

	verified, err := certs.VerifyCertificate(bytes.NewBuffer(ca), bytes.NewBuffer(clientCrt))
	if err != nil {
		return verified, err
	}

	return certs.VerifyCertificate(bytes.NewBuffer(ca), bytes.NewBuffer(serverCrt))
}

func isPSKSecretValid(ctx context.Context, c ctrlclient.Client, logger logr.Logger, secretRef types.NamespacedName) (bool, error) {
	secret := &corev1.Secret{}
	err := c.Get(ctx, secretRef, secret)
	switch {
	case k8serrors.IsNotFound(err):
		return false, nil
	case err != nil:
		return false, err
	}

	_, ok := secret.Data["key"]
	if !ok {
		logger.Info("secret data missing PSK key", "secret", secretRef)
		return false, nil
	}

	return true, nil
}

// reconcileCredentialSecret reconciles credential secrets for a stunnel transport
func reconcileCredentialSecret(ctx context.Context,
	c ctrlclient.Client,
	logger logr.Logger,
	s transport.Transport,
	o *transport.Options) error {
	var err error
	secretValid := false
	credType := CredentialsTypeTLS
	secretRef := types.NamespacedName{
		Namespace: s.NamespacedName().Namespace,
		Name:      getResourceName(s.NamespacedName(), "certs", stunnelSecret),
	}
	if o.Credentials != nil {
		if o.Credentials.Type != "" {
			credType = o.Credentials.Type
		}
		if o.Credentials.SecretRef.Name != "" {
			secretRef = o.Credentials.SecretRef
		}
	}

	switch credType {
	case CredentialsTypePSK:
		secretValid, err = isPSKSecretValid(ctx, c, logger, secretRef)
		if err != nil {
			logger.Error(err, "error getting existing PSK credentials from secret")
			return err
		}
	case CredentialsTypeTLS:
		secretValid, err = isTLSSecretValid(ctx, c, logger, secretRef)
		if err != nil {
			logger.Error(err, "error getting existing ssl certs from secret")
			return err
		}
	default:
		return fmt.Errorf("unsupported credentials type %s", credType)
	}

	if secretValid {
		logger.V(4).Info("found secret with valid certs")
		return nil
	}

	logger.Info("generating new certificate bundle")

	switch credType {
	case CredentialsTypeTLS:
		crtBundle, err := certs.New()
		if err != nil {
			logger.Error(err, "error generating ssl certs for stunnel server")
			return err
		}
		return reconcileTLSSecret(ctx, c, secretRef, o, crtBundle)
	default:
		return fmt.Errorf("cannot create credentials of type %s", credType)
	}
}

// reconcileTLSSecret reconciles secret of TLS type
func reconcileTLSSecret(ctx context.Context,
	c ctrlclient.Client,
	secretRef types.NamespacedName,
	options *transport.Options,
	crtBundle *certs.CertificateBundle) error {
	crtBundleSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: secretRef.Namespace,
			Name:      secretRef.Name,
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

	return err
}

func getCredentialsSecretRef(t transport.Transport, c *transport.Credentials) types.NamespacedName {
	secretRef := types.NamespacedName{
		Name:      getResourceName(t.NamespacedName(), "certs", stunnelSecret),
		Namespace: t.NamespacedName().Namespace,
	}
	if c != nil {
		if c.SecretRef.Name != "" {
			secretRef = c.SecretRef
		}
	}
	return secretRef
}

func getCredentialsVolumeSource(t transport.Transport, c *transport.Credentials, key string) corev1.VolumeSource {
	tlsItems := []corev1.KeyToPath{
		{
			Key:  fmt.Sprintf("%s.crt", key),
			Path: fmt.Sprintf("%s.crt", key),
		},
		{
			Key:  fmt.Sprintf("%s.key", key),
			Path: fmt.Sprintf("%s.key", key),
		},
		{
			Key:  "ca.crt",
			Path: "ca.crt",
		},
	}
	pskItems := []corev1.KeyToPath{
		{
			Key:  "key",
			Path: "key",
		},
	}
	volumeSource := corev1.VolumeSource{
		Secret: &corev1.SecretVolumeSource{
			SecretName: getCredentialsSecretRef(t, c).Name,
			Items:      tlsItems,
		},
	}
	// when no existing credentials are provided, default to TLS
	if c != nil {
		switch c.Type {
		case CredentialsTypeTLS:
			volumeSource.Secret.Items = tlsItems
			return volumeSource
		case CredentialsTypePSK:
			volumeSource.Secret.Items = pskItems
			return volumeSource
		}
	}
	return volumeSource
}

func markForCleanup(ctx context.Context, c ctrlclient.Client, objKey types.NamespacedName, key, value, component string) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getResourceName(objKey, component, stunnelConfig),
			Namespace: objKey.Namespace,
		},
	}
	err := utils.UpdateWithLabel(ctx, c, cm, key, value)
	if err != nil {
		return err
	}

	clientSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getResourceName(objKey, "certs", stunnelSecret),
			Namespace: objKey.Namespace,
		},
	}
	err = utils.UpdateWithLabel(ctx, c, clientSecret, key, value)
	switch {
	case k8serrors.IsNotFound(err):
		break
	case err != nil:
		return err
	}

	return nil
}
