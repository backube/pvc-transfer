package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Transport interface {
	// NamespacedName returns the namespaced name to identify this transport Transport
	NamespacedName() types.NamespacedName
	// ListenPort returns a port on which the transport server listens for incomming connections
	ListenPort() int32
	// ConnectPort returns the port to connect to transfer server
	// Using this the server acts as a client to the transfer relaying all the data
	// sent from the transport client
	ConnectPort() int32
	// Containers returns a list of containers transfers can add to their server Pods
	Containers() []corev1.Container
	// Volumes returns a list of volumes transfers have add to their server Pods for getting the configurations
	// mounted for the transport containers to work
	Volumes() []corev1.Volume
	// Type
	Type() Type
	// Credentials returns the namespaced name of the secret holding credentials for talking to the server
	Credentials() types.NamespacedName
	// Hostname returns the string to which the transfer will connect to
	// in case of a null transport, it will simple relay the endpoint hostname
	// in case of a valid transport, it will have a custom hostname where transfers will have to connect to.
	Hostname() string
	// MarkForCleanup adds a label to all the resources created for the endpoint
	// Callers are expected to not overwrite
	MarkForCleanup(ctx context.Context, c client.Client, key, value string) error
}

type Options struct {
	Labels map[string]string
	Owners []metav1.OwnerReference
	Image  string

	ProxyURL      string
	ProxyUsername string
	ProxyPassword string
	NoVerifyCA    bool
	CAVerifyLevel string
}

type Type string

func GenerateSSLCert() (*bytes.Buffer, *bytes.Buffer, *bytes.Buffer, error) {
	caPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, nil, err
	}

	subj := pkix.Name{
		CommonName:         "backube.dev",
		Country:            []string{"US"},
		Organization:       []string{"Backube"},
		OrganizationalUnit: []string{"Engineering"},
	}

	certTemp := x509.Certificate{
		SerialNumber:          big.NewInt(2020),
		Subject:               subj,
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	caBytes, err := x509.CreateCertificate(
		rand.Reader,
		&certTemp,
		&certTemp,
		&caPrivKey.PublicKey,
		caPrivKey,
	)
	if err != nil {
		return nil, nil, nil, err
	}
	crt := new(bytes.Buffer)
	err = pem.Encode(crt, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	key := new(bytes.Buffer)
	err = pem.Encode(key, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(caPrivKey),
	})
	if err != nil {
		return nil, nil, nil, err
	}

	return crt, crt, key, nil
}
