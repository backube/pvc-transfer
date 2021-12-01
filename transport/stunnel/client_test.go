package stunnel

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/backube/pvc-transfer/transport"
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
						Name:      "foo-client-stunnel-credentials",
						Namespace: "bar",
					},
					Data: map[string][]byte{"tls.key": []byte(`key`), "tls.crt": []byte(`crt`)},
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
						Name:      "foo-client-stunnel-credentials",
						Namespace: "bar",
					},
					Data: map[string][]byte{"tls.crt": []byte(`crt`)},
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
						Name:      "foo-client-stunnel-config",
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
						Name:      "foo-client-stunnel-config",
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
				Name:      "foo-client-" + stunnelConfig,
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
				Name:      "foo-client-" + stunnelSecret,
			}, secret)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}

			_, ok = secret.Data["tls.key"]
			if !ok {
				t.Error("unable to find tls.key in stunnel secret")
			}

			_, ok = secret.Data["tls.crt"]
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
