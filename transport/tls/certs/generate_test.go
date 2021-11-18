package certs

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "test new and verify the CA",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New()
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if got != nil && got.CACrt == nil {
				t.Error("ca cert is nil")
				return
			}
			if got != nil && got.CAKey == nil {
				t.Error("ca key is nil")
				return
			}
			if got != nil && got.ServerCrt == nil {
				t.Error("server crt is nil")
				return
			}
			if got != nil && got.ServerKey == nil {
				t.Error("server key is nil")
				return
			}
			if got != nil && got.ClientCrt == nil {
				t.Error("client crt is nil")
				return
			}
			if got != nil && got.ClientKey == nil {
				t.Error("client key is nil")
				return
			}

			//if !verifySingedCAFiles() {
			//	t.Error("client cert is not verified with root CA")
			//}

			if !verifySingedCA(got.CACrt, got.ClientCrt) {
				t.Error("client cert is not verified with root CA")
			}
			if !verifySingedCA(got.CACrt, got.ServerCrt) {
				t.Error("server cert is not verified with root CA")
			}

			got2, err := New()
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if verifySingedCA(got.CACrt, got2.ClientCrt) {
				t.Error("client cert is verified with different root CA")
			}
			if verifySingedCA(got.CACrt, got2.ServerCrt) {
				t.Error("server cert is not verified with different root CA")
			}
		})
	}
}

func verifySingedCA(caCrt *bytes.Buffer, crt *bytes.Buffer) bool {
	roots := x509.NewCertPool()
	ok := roots.AppendCertsFromPEM(caCrt.Bytes())
	if !ok {
		panic("failed to parse root certificate")
	}

	block, _ := pem.Decode(crt.Bytes())
	if block == nil {
		panic("failed to parse certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		panic("failed to parse certificate: " + err.Error())
	}

	opts := x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}

	if _, err := cert.Verify(opts); err != nil {
		return false
	}
	return true
}
