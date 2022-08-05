package certs

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

var (
	keySize          = 2048
	defaultCASubject = &pkix.Name{
		Country:            []string{"US"},
		Province:           []string{"NC"},
		Locality:           []string{"RDU"},
		Organization:       []string{"Backube"},
		OrganizationalUnit: []string{"Engineering"},
		// This does not have to be a domain name, but certain implementations/configuration
		// verify the requests from other side using this and SAN fields.
		CommonName: "ca.backube.dev",
	}
	defaultCrtSubject = &pkix.Name{
		Country:            []string{"US"},
		Province:           []string{"NC"},
		Locality:           []string{"RDU"},
		Organization:       []string{"Backube"},
		OrganizationalUnit: []string{"Engineering"},
		CommonName:         "cert.backube.dev",
	}
)

// CertificateBundle stores the data used for creating a secret with tls bundle
// that includes a self signed CA (crt and key) as well as client and server certs
// (cert and key).
type CertificateBundle struct {
	caRSAKey      *rsa.PrivateKey
	caCrtTemplate *x509.Certificate

	CACrt     *bytes.Buffer
	CAKey     *bytes.Buffer
	ServerCrt *bytes.Buffer
	ServerKey *bytes.Buffer
	ClientCrt *bytes.Buffer
	ClientKey *bytes.Buffer
}

// New returns CertificateBundle after populating all the public fields. It should
// ideally be persisted in kubernetes objects (secrets) by consumers. If the secret is
// lost or deleted, New should be called again to get a fresh bundle.
func New() (*CertificateBundle, error) {
	c := &CertificateBundle{}
	var err error
	c.CACrt, c.caRSAKey, c.caCrtTemplate, err = GenerateCA(defaultCASubject)
	if err != nil {
		return nil, err
	}

	c.CAKey, err = rsaKeyBytes(c.caRSAKey)

	c.ServerCrt, c.ServerKey, err = Generate(defaultCrtSubject, *c.caCrtTemplate, *c.caRSAKey)
	if err != nil {
		return nil, err
	}

	c.ClientCrt, c.ClientKey, err = Generate(defaultCrtSubject, *c.caCrtTemplate, *c.caRSAKey)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// GenerateCA take a subject and returns caCrt, caKey and caCrtTemplate
// The caKey and caCrtTemplate should be passed into Generate
// along with a similar subject except the CN name should be different from
// the CA.
func GenerateCA(subject *pkix.Name) (caCrt *bytes.Buffer, caKey *rsa.PrivateKey, caCrtTemplate *x509.Certificate, err error) {
	if subject == nil {
		subject = defaultCASubject
	}
	caCrtTemplate = &x509.Certificate{
		SerialNumber:          big.NewInt(2021),
		Subject:               *subject,
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageAny},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caCrt, caKey, err = createCrtKeyPair(caCrtTemplate, nil, nil)
	if err != nil {
		return
	}
	return
}

// Generate takes a subject, caCrtTemplate and caKey and returns crt, key and error
// if error is not nil, do not rely on crt or keys being not nil.
func Generate(subject *pkix.Name, caCrtTemplate x509.Certificate, caKey rsa.PrivateKey) (crt *bytes.Buffer, key *bytes.Buffer, err error) {
	crtTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2020),
		Subject:      *subject,
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}

	crt, rsaKey, err := createCrtKeyPair(crtTemplate, &caCrtTemplate, &caKey)
	if err != nil {
		return
	}
	key, err = rsaKeyBytes(rsaKey)
	if err != nil {
		return
	}
	return
}

// VerifyCertificate returns true if the crt is signed by the caCrt as the root CA
// with no intermediate DCAs in the chain
func VerifyCertificate(caCrt *bytes.Buffer, crt *bytes.Buffer) (bool, error) {
	roots := x509.NewCertPool()
	ok := roots.AppendCertsFromPEM(caCrt.Bytes())
	if !ok {
		return false, fmt.Errorf("failed to parse root certificate")
	}

	block, _ := pem.Decode(crt.Bytes())
	if block == nil {
		return false, fmt.Errorf("unable to decode certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, fmt.Errorf("failed to parse certificate: %#v", err)
	}

	opts := x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}

	if _, err := cert.Verify(opts); err != nil {
		return false, nil
	}
	return true, nil
}

func createCrtKeyPair(crtTemplate, parent *x509.Certificate, signer *rsa.PrivateKey) (crt *bytes.Buffer, key *rsa.PrivateKey, err error) {
	key, err = rsa.GenerateKey(rand.Reader, keySize)
	if err != nil {
		return
	}
	if parent == nil {
		parent = crtTemplate
	}
	if signer == nil {
		signer = key
	}

	crtBytes, err := x509.CreateCertificate(
		rand.Reader,
		crtTemplate,
		parent,
		&key.PublicKey,
		signer,
	)
	if err != nil {
		return
	}

	crt = new(bytes.Buffer)
	err = pem.Encode(crt, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: crtBytes,
	})
	if err != nil {
		return nil, nil, err
	}
	return
}

func rsaKeyBytes(key *rsa.PrivateKey) (keyBytes *bytes.Buffer, err error) {
	keyBytes = new(bytes.Buffer)
	err = pem.Encode(keyBytes, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err != nil {
		return
	}
	return
}
