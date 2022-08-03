package stunnel

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/backube/pvc-transfer/transport"
	"github.com/backube/pvc-transfer/transport/tls/certs"
	logrtesting "github.com/go-logr/logr/testing"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var certificateBundle, _ = certs.New()

func Test_getExistingCert(t *testing.T) {
	tests := []struct {
		name           string
		namespacedName types.NamespacedName
		labels         map[string]string
		wantErr        bool
		wantFound      bool
		objects        []ctrlclient.Object
	}{
		{
			name:           "test with no secret",
			namespacedName: types.NamespacedName{Namespace: "bar", Name: "foo"},
			labels:         map[string]string{"test": "me"},
			wantErr:        false,
			wantFound:      false,
			objects:        []ctrlclient.Object{},
		},
		{
			name:           "test with invalid secret, key missing",
			namespacedName: types.NamespacedName{Namespace: "bar", Name: "foo"},
			labels:         map[string]string{"test": "me"},
			wantErr:        false,
			wantFound:      false,
			objects: []ctrlclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "stunnel-creds-foo",
						Namespace: "bar",
						Labels:    map[string]string{"test": "me"},
					},
					Data: map[string][]byte{"server.crt": certificateBundle.ServerCrt.Bytes()},
				},
			},
		},
		{
			name:           "test with invalid secret, crt missing",
			namespacedName: types.NamespacedName{Namespace: "bar", Name: "foo"},
			labels:         map[string]string{"test": "me"},
			wantErr:        false,
			wantFound:      false,
			objects: []ctrlclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "stunnel-creds-foo",
						Namespace: "bar",
						Labels:    map[string]string{"test": "me"},
					},
					Data: map[string][]byte{"server.key": certificateBundle.ServerKey.Bytes()},
				},
			},
		},
		{
			name:           "test with secret missing ca.crt",
			namespacedName: types.NamespacedName{Namespace: "bar", Name: "foo"},
			labels:         map[string]string{"test": "me"},
			wantErr:        true,
			wantFound:      false,
			objects: []ctrlclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "stunnel-creds-foo",
						Namespace: "bar",
						Labels:    map[string]string{"test": "me"},
					},
					Data: map[string][]byte{"server.key": certificateBundle.ServerKey.Bytes(), "server.crt": certificateBundle.ServerKey.Bytes()},
				},
			},
		},
		{
			name:           "test with valid secret",
			namespacedName: types.NamespacedName{Namespace: "bar", Name: "foo"},
			labels:         map[string]string{"test": "me"},
			wantErr:        true,
			wantFound:      true,
			objects: []ctrlclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "stunnel-creds-foo-foo",
						Namespace: "bar",
						Labels:    map[string]string{"test": "me"},
					},
					Data: map[string][]byte{
						"server.key": certificateBundle.ServerKey.Bytes(), "server.crt": certificateBundle.ServerCrt.Bytes(),
						"client.key": certificateBundle.ClientKey.Bytes(), "client.crt": certificateBundle.ClientCrt.Bytes(),
						"ca.crt": certificateBundle.CACrt.Bytes()},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &server{
				logger:         logrtesting.TestLogger{T: t},
				namespacedName: tt.namespacedName,
				options: &transport.Options{
					Labels: tt.labels,
					Owners: testOwnerReferences(),
				},
			}
			ctx := context.WithValue(context.Background(), "test", tt.name)
			found, err := isSecretValid(ctx, fakeClientWithObjects(tt.objects...), s.logger, s.namespacedName, "foo")
			if err != nil {
				t.Error("found unexpected error", err)
			}
			if !tt.wantFound && found {
				t.Error("found unexpected secret")
			}
			if tt.wantFound && !found {
				t.Error("not found unexpected")
			}
		})
	}
}

func Test_mrkForCleanup(t *testing.T) {
	tests := []struct {
		name           string
		namespacedName types.NamespacedName
		labels         map[string]string
		wantErr        bool
		key            string
		value          string
		objects        []ctrlclient.Object
		verifyObjects  []ctrlclient.Object
	}{
		{
			name:           "test with configmap and secret objects",
			namespacedName: types.NamespacedName{Namespace: "bar", Name: "foo"},
			labels:         map[string]string{"test": "me"},
			wantErr:        false,
			key:            "cleanup-key",
			value:          "cleanup-value",
			objects: []ctrlclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "stunnel-creds-certs-foo",
						Namespace: "bar",
						Labels:    map[string]string{"test": "me"},
					},
					Data: map[string][]byte{
						"server.key": []byte(`key`), "server.crt": []byte(`crt`),
						"client.key": []byte(`key`), "client.crt": []byte(`crt`),
						"ca.key": []byte(`key`), "ca.crt": []byte(`crt`),
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "stunnel-config-foo-foo",
						Namespace: "bar",
						Labels:    map[string]string{"test": "me"},
					},
					Data: map[string]string{"stunnel.conf": "foreground = yes"},
				},
			},
			verifyObjects: []ctrlclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name: stunnelConfig + "-foo-foo",
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name: stunnelSecret + "-certs-foo",
					},
				},
			},
		},
		{
			name:           "test with configmap but no server secret",
			namespacedName: types.NamespacedName{Namespace: "bar", Name: "foo"},
			labels:         map[string]string{"test": "me"},
			wantErr:        false,
			key:            "cleanup-key",
			value:          "cleanup-value",
			objects: []ctrlclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "stunnel-creds-certs-foo",
						Namespace: "bar",
						Labels:    map[string]string{"test": "me"},
					},
					Data: map[string][]byte{
						"client.key": []byte(`key`), "client.crt": []byte(`crt`),
						"ca.key": []byte(`key`), "ca.crt": []byte(`crt`),
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "stunnel-config-foo-foo",
						Namespace: "bar",
						Labels:    map[string]string{"test": "me"},
					},
					Data: map[string]string{"stunnel.conf": "foreground = yes"},
				},
			},
			verifyObjects: []ctrlclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name: stunnelConfig + "-foo-foo",
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name: stunnelSecret + "-certs-foo",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), "test", tt.name)
			fakeClient := fakeClientWithObjects(tt.objects...)
			if err := markForCleanup(ctx, fakeClient, tt.namespacedName, tt.key, tt.value, "foo"); (err != nil) != tt.wantErr {
				t.Errorf("markForCleanup() error = %v, wantErr %v", err, tt.wantErr)
			}

			tt.labels[tt.key] = tt.value
			for _, obj := range tt.verifyObjects {
				err := fakeClient.Get(context.Background(), types.NamespacedName{
					Namespace: tt.namespacedName.Namespace,
					Name:      obj.GetName()}, obj)
				if err != nil {
					panic(fmt.Errorf("%#v should not be getting error from fake client", err))
				}
				if !reflect.DeepEqual(tt.labels, obj.GetLabels()) {
					t.Errorf("labels on obj = %#v, wanted %#v", obj.GetLabels(), tt.labels)
				}
			}
		})
	}
}
