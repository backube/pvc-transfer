package certs

import (
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

			if ok, _ := VerifyCertificate(got.CACrt, got.ClientCrt); !ok {
				t.Error("client cert is not verified with root CA")
			}
			if ok, _ := VerifyCertificate(got.CACrt, got.ServerCrt); !ok {
				t.Error("server cert is not verified with root CA")
			}

			got2, err := New()
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if ok, _ := VerifyCertificate(got.CACrt, got2.ClientCrt); ok {
				t.Error("client cert is verified with different root CA")
			}
			if ok, _ := VerifyCertificate(got.CACrt, got2.ServerCrt); ok {
				t.Error("server cert is not verified with different root CA")
			}
		})
	}
}
