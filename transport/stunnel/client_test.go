package stunnel

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/backube/pvc-transfer/transport"
	"github.com/backube/pvc-transfer/transport/tls/certs"
	logrtesting "github.com/go-logr/logr/testing"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name            string
		namespacedName  types.NamespacedName
		hostname        string
		ingressPort     int32
		labels          map[string]string
		ownerReferences []metav1.OwnerReference
		wantErr         bool
		objects         []ctrlclient.Object
	}{
		{
			name:            "test with no stunnel client objects",
			namespacedName:  types.NamespacedName{Namespace: "bar", Name: "foo"},
			hostname:        "example-test.com",
			ingressPort:     8080,
			labels:          map[string]string{"test": "me"},
			ownerReferences: testOwnerReferences(),
			wantErr:         false,
			objects:         []ctrlclient.Object{},
		},
		{
			name:            "test stunnel client, valid secret exists but no configmap",
			namespacedName:  types.NamespacedName{Namespace: "bar", Name: "foo"},
			labels:          map[string]string{"test": "me"},
			ownerReferences: testOwnerReferences(),
			wantErr:         false,
			objects: []ctrlclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "stunnel-creds-certs-foo",
						Namespace: "bar",
					},
					Data: map[string][]byte{"client.key": []byte(`key`), "client.crt": []byte(`crt`)},
				},
			},
		},
		{
			name:            "test stunnel client, invalid secret exists but no configmap",
			namespacedName:  types.NamespacedName{Namespace: "bar", Name: "foo"},
			labels:          map[string]string{"test": "me"},
			ownerReferences: testOwnerReferences(),
			wantErr:         false,
			objects: []ctrlclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "stunnel-creds-certs-foo",
						Namespace: "bar",
					},
					Data: map[string][]byte{"client.crt": []byte(`crt`)},
				},
			},
		},
		{
			name:            "test stunnel client, valid configmap but no secret",
			namespacedName:  types.NamespacedName{Namespace: "bar", Name: "foo"},
			labels:          map[string]string{"test": "me"},
			ownerReferences: testOwnerReferences(),
			wantErr:         false,
			objects: []ctrlclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "stunnel-config-client-foo",
						Namespace: "bar",
					},
					Data: map[string]string{"stunnel.conf": "foreground = yes"},
				},
			},
		},
		{
			name:            "test stunnel client, invalid configmap but no secret",
			namespacedName:  types.NamespacedName{Namespace: "bar", Name: "foo"},
			labels:          map[string]string{"test": "me"},
			ownerReferences: testOwnerReferences(),
			wantErr:         false,
			objects: []ctrlclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "stunnel-config-foo-client",
						Namespace: "bar",
					},
					Data: map[string]string{"stunnel.conf": "foreground = no"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fakeClientWithObjects(tt.objects...)
			ctx := context.WithValue(context.Background(), "test", tt.name)
			fakeLogger := logrtesting.TestLogger{t}
			stunnelClient, err := NewClient(ctx, fakeClient, fakeLogger, tt.namespacedName, tt.hostname, tt.ingressPort, &transport.Options{Labels: tt.labels, Owners: tt.ownerReferences})
			if (err != nil) != tt.wantErr {
				t.Errorf("NewClient() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			cm := &corev1.ConfigMap{}
			err = fakeClient.Get(context.Background(), types.NamespacedName{
				Namespace: "bar",
				Name:      stunnelConfig + "-client-foo",
			}, cm)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}

			configdata, ok := cm.Data["stunnel.conf"]
			if !ok {
				t.Error("unable to find stunnel config data in configmap")
			}
			if !strings.Contains(configdata, "pid =") {
				t.Error("configmap data does not contain the right data")
			}

			secret := &corev1.Secret{}
			err = fakeClient.Get(context.Background(), types.NamespacedName{
				Namespace: "bar",
				Name:      stunnelSecret + "-certs-foo",
			}, secret)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}

			_, ok = secret.Data["client.key"]
			if !ok {
				t.Error("unable to find tls.key in stunnel secret")
			}

			_, ok = secret.Data["client.crt"]
			if !ok {
				t.Error("unable to find tls.crt in stunnel secret")
			}

			if len(stunnelClient.Volumes()) == 0 {
				t.Error("stunnel client volumes not set properly")
			}

			if len(stunnelClient.Containers()) == 0 {
				t.Error("stunnel client containers not set properly")
			}
		})
	}
}

func Test_client_reconcileSecret(t *testing.T) {
	testCert, _ := certs.New()
	tests := []struct {
		name        string
		options     *transport.Options
		secretRef   types.NamespacedName
		withObjects []ctrlclient.Object
		wantSecret  *corev1.Secret
		wantErr     bool
	}{
		{
			name: "no credentials type provided, must create TLS secret",
			options: &transport.Options{
				Credentials: nil,
			},
			secretRef: types.NamespacedName{
				Namespace: "foo",
				Name:      "bar",
			},
			wantErr:     false,
			withObjects: []ctrlclient.Object{},
			wantSecret: &corev1.Secret{
				Data: map[string][]byte{
					"ca.crt":     {},
					"ca.key":     {},
					"client.key": {},
					"client.crt": {},
					"server.crt": {},
					"server.key": {},
				},
			},
		},
		{
			name: "PSK secret type specified but no secret ref set, must return error as PSK cannot be created",
			options: &transport.Options{
				Credentials: &transport.Credentials{
					Type: CredentialsTypePSK,
				},
			},
			secretRef: types.NamespacedName{
				Namespace: "foo",
				Name:      "bar",
			},
			wantErr:     true,
			withObjects: []ctrlclient.Object{},
			wantSecret:  nil,
		},
		{
			name: "existing PSK credentials provided with invalid secret keys, must return error",
			options: &transport.Options{
				Credentials: &transport.Credentials{
					Type: CredentialsTypePSK,
					SecretRef: types.NamespacedName{
						Namespace: "foo",
						Name:      "bar",
					},
				},
			},
			secretRef: types.NamespacedName{
				Namespace: "foo",
				Name:      "bar",
			},
			wantErr: true,
			withObjects: []ctrlclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "foo",
						Name:      "bar",
					},
					Data: map[string][]byte{
						"invalid_key": []byte("val"),
					},
				},
			},
			wantSecret: nil,
		},
		{
			name: "existing PSK credentials provided with valid secret keys, must not return error",
			options: &transport.Options{
				Credentials: &transport.Credentials{
					Type: CredentialsTypePSK,
					SecretRef: types.NamespacedName{
						Namespace: "foo",
						Name:      "bar",
					},
				},
			},
			secretRef: types.NamespacedName{
				Namespace: "foo",
				Name:      "bar",
			},
			wantErr: false,
			withObjects: []ctrlclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "foo",
						Name:      "bar",
					},
					Data: map[string][]byte{
						"key": []byte("val"),
					},
				},
			},
			wantSecret: nil,
		},
		{
			name: "existing TLS credentials provided with valid secret keys, must not return error",
			options: &transport.Options{
				Credentials: &transport.Credentials{
					Type: CredentialsTypeTLS,
					SecretRef: types.NamespacedName{
						Namespace: "foo",
						Name:      "bar",
					},
				},
			},
			secretRef: types.NamespacedName{
				Namespace: "foo",
				Name:      "bar",
			},
			wantErr: false,
			withObjects: []ctrlclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "foo",
						Name:      "bar",
					},
					Data: map[string][]byte{
						"ca.crt":     testCert.CACrt.Bytes(),
						"ca.key":     testCert.CAKey.Bytes(),
						"server.crt": testCert.ServerCrt.Bytes(),
						"server.key": testCert.ServerKey.Bytes(),
						"client.crt": testCert.ClientCrt.Bytes(),
						"client.key": testCert.ClientKey.Bytes(),
					},
				},
			},
			wantSecret: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &client{
				logger:         logrtesting.TestLogger{T: t},
				connectPort:    6443,
				listenPort:     6443,
				options:        tt.options,
				namespacedName: tt.secretRef,
			}
			c := fakeClientWithObjects(tt.withObjects...)
			if err := sc.reconcileSecret(context.TODO(), c); (err != nil) != tt.wantErr {
				t.Errorf("client.reconcileSecret() error = %v, wantErr %v", err, tt.wantErr)
			}
			// secret won't be created when there was an error
			if tt.wantSecret == nil {
				return
			}

			got := &corev1.Secret{}
			err := c.Get(context.TODO(), types.NamespacedName{
				Namespace: tt.secretRef.Namespace,
				Name:      fmt.Sprintf("%s-%s-%s", stunnelSecret, "certs", tt.secretRef.Name),
			}, got)
			if err != nil {
				panic(fmt.Errorf("shouldn't be getting error from the client, err %v", err))
			}

			for wantK := range tt.wantSecret.Data {
				if _, ok := got.Data[wantK]; !ok {
					t.Errorf("client.reconcileSecret() key = %v not found in secret", wantK)
				}
			}

		})
	}
}
