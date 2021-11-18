package stunnel

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/backube/pvc-transfer/endpoint"
	"github.com/backube/pvc-transfer/transport"
	logrtesting "github.com/go-logr/logr/testing"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func fakeClientWithObjects(objs ...ctrlclient.Object) ctrlclient.WithWatch {
	scheme := runtime.NewScheme()
	_ = AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func testOwnerReferences() []metav1.OwnerReference {
	return []metav1.OwnerReference{{
		APIVersion:         "api.foo",
		Kind:               "Test",
		Name:               "bar",
		UID:                "123",
		Controller:         pointer.Bool(true),
		BlockOwnerDeletion: pointer.Bool(true),
	}}
}

type fakeEndpoint struct {
	nn       types.NamespacedName
	hostname string
}

func (f fakeEndpoint) NamespacedName() types.NamespacedName {
	return f.nn
}

func (f fakeEndpoint) Hostname() string {
	return f.hostname
}

func (f fakeEndpoint) BackendPort() int32 {
	return 1234
}

func (f fakeEndpoint) IngressPort() int32 {
	return 1234
}

func (f fakeEndpoint) IsHealthy(_ context.Context, _ ctrlclient.Client) (bool, error) {
	return true, nil
}

func (f fakeEndpoint) MarkForCleanup(_ context.Context, _ ctrlclient.Client, _, _ string) error {
	return nil
}

func newFakeEndpoint() endpoint.Endpoint {
	return fakeEndpoint{
		nn:       types.NamespacedName{Name: "foo", Namespace: "bar"},
		hostname: "foo.bar",
	}
}

func TestNewServer(t *testing.T) {
	tests := []struct {
		name            string
		namespacedName  types.NamespacedName
		endpoint        endpoint.Endpoint
		labels          map[string]string
		ownerReferences []metav1.OwnerReference
		wantErr         bool
		objects         []ctrlclient.Object
	}{
		{
			name:            "test with no stunnel server objects",
			namespacedName:  types.NamespacedName{Namespace: "bar", Name: "foo"},
			endpoint:        newFakeEndpoint(),
			labels:          map[string]string{"test": "me"},
			ownerReferences: testOwnerReferences(),
			wantErr:         false,
			objects:         []ctrlclient.Object{},
		},
		{
			name:            "test stunnel server, valid secret exists but no configmap",
			namespacedName:  types.NamespacedName{Namespace: "bar", Name: "foo"},
			endpoint:        newFakeEndpoint(),
			labels:          map[string]string{"test": "me"},
			ownerReferences: testOwnerReferences(),
			wantErr:         false,
			objects: []ctrlclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo-server-stunnel-credentials",
						Namespace: "bar",
					},
					Data: map[string][]byte{"tls.key": []byte(`key`), "tls.crt": []byte(`crt`)},
				},
			},
		},
		{
			name:            "test stunnel server, invalid secret exists but no configmap",
			namespacedName:  types.NamespacedName{Namespace: "bar", Name: "foo"},
			endpoint:        newFakeEndpoint(),
			labels:          map[string]string{"test": "me"},
			ownerReferences: testOwnerReferences(),
			wantErr:         false,
			objects: []ctrlclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo-server-stunnel-credentials",
						Namespace: "bar",
					},
					Data: map[string][]byte{"tls.crt": []byte(`crt`)},
				},
			},
		},
		{
			name:            "test stunnel server, valid configmap but no secret",
			namespacedName:  types.NamespacedName{Namespace: "bar", Name: "foo"},
			endpoint:        newFakeEndpoint(),
			labels:          map[string]string{"test": "me"},
			ownerReferences: testOwnerReferences(),
			wantErr:         false,
			objects: []ctrlclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo-server-stunnel-config",
						Namespace: "bar",
					},
					Data: map[string]string{"stunnel.conf": "foreground = yes"},
				},
			},
		},
		{
			name:            "test stunnel server, invalid configmap but no secret",
			namespacedName:  types.NamespacedName{Namespace: "bar", Name: "foo"},
			endpoint:        newFakeEndpoint(),
			labels:          map[string]string{"test": "me"},
			ownerReferences: testOwnerReferences(),
			wantErr:         false,
			objects: []ctrlclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo-server-stunnel-config",
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
			fakeLogger := logrtesting.TestLogger{T: t}
			stunnelServer, err := NewServer(ctx, fakeClient, fakeLogger, tt.namespacedName, tt.endpoint, &transport.Options{Labels: tt.labels, Owners: tt.ownerReferences})
			if (err != nil) != tt.wantErr {
				t.Errorf("NewServer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			cm := &corev1.ConfigMap{}
			err = fakeClient.Get(context.Background(), types.NamespacedName{
				Namespace: "bar",
				Name:      "foo-server-" + stunnelConfig,
			}, cm)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}

			configData, ok := cm.Data["stunnel.conf"]
			if !ok {
				t.Error("unable to find stunnel config data in configmap")
			}
			if !strings.Contains(configData, "foreground = yes") {
				t.Error("configmap data does not contain the right data")
			}

			secret := &corev1.Secret{}
			err = fakeClient.Get(context.Background(), types.NamespacedName{
				Namespace: "bar",
				Name:      "foo-server-" + stunnelSecret,
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

			_, ok = secret.Data["ca.crt"]
			if !ok {
				t.Error("unable to find ca.crt in stunnel secret")
			}

			if len(stunnelServer.Volumes()) == 0 {
				t.Error("stunnel server volumes not set properly")
			}

			if len(stunnelServer.Containers()) == 0 {
				t.Error("stunnel server containers not set properly")
			}
		})
	}
}

func Test_server_MarkForCleanup(t *testing.T) {
	tests := []struct {
		name           string
		namespacedName types.NamespacedName
		labels         map[string]string
		wantErr        bool
		key            string
		value          string
		objects        []ctrlclient.Object
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
						Name:      "foo-server-stunnel-credentials",
						Namespace: "bar",
						Labels:    map[string]string{"test": "me"},
					},
					Data: map[string][]byte{"tls.key": []byte(`key`), "tls.crt": []byte(`crt`)},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo-client-stunnel-credentials",
						Namespace: "bar",
						Labels:    map[string]string{"test": "me"},
					},
					Data: map[string][]byte{"tls.key": []byte(`key`), "tls.crt": []byte(`crt`)},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo-ca-bundle-stunnel-credentials",
						Namespace: "bar",
						Labels:    map[string]string{"test": "me"},
					},
					Data: map[string][]byte{"tls.key": []byte(`key`), "tls.crt": []byte(`crt`)},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo-server-stunnel-config",
						Namespace: "bar",
						Labels:    map[string]string{"test": "me"},
					},
					Data: map[string]string{"stunnel.conf": "foreground = yes"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &server{
				logger: logrtesting.TestLogger{T: t},
				options: &transport.Options{
					Labels: tt.labels,
					Owners: testOwnerReferences(),
				},
				namespacedName: tt.namespacedName,
			}
			ctx := context.WithValue(context.Background(), "test", tt.name)
			fakeClient := fakeClientWithObjects(tt.objects...)
			if err := s.MarkForCleanup(ctx, fakeClient, tt.key, tt.value); (err != nil) != tt.wantErr {
				t.Errorf("MarkForCleanup() error = %v, wantErr %v", err, tt.wantErr)
			}

			cm := &corev1.ConfigMap{}
			err := fakeClient.Get(context.Background(), types.NamespacedName{
				Namespace: "bar",
				Name:      "foo-server-" + stunnelConfig,
			}, cm)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}

			tt.labels[tt.key] = tt.value
			if !reflect.DeepEqual(tt.labels, cm.Labels) {
				t.Errorf("labels on configmap = %#v, wanted %#v", cm.Labels, tt.labels)
			}

			secretSecret := &corev1.Secret{}
			err = fakeClient.Get(context.Background(), types.NamespacedName{
				Namespace: "bar",
				Name:      "foo-server-" + stunnelSecret,
			}, secretSecret)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}
			if !reflect.DeepEqual(tt.labels, secretSecret.Labels) {
				t.Errorf("labels on secretSecret = %#v, wanted %#v", secretSecret.Labels, tt.labels)
			}

			clientSecret := &corev1.Secret{}
			err = fakeClient.Get(context.Background(), types.NamespacedName{
				Namespace: "bar",
				Name:      "foo-client-" + stunnelSecret,
			}, clientSecret)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}
			if !reflect.DeepEqual(tt.labels, clientSecret.Labels) {
				t.Errorf("labels on secretSecret = %#v, wanted %#v", secretSecret.Labels, tt.labels)
			}

			caBundleSecret := &corev1.Secret{}
			err = fakeClient.Get(context.Background(), types.NamespacedName{
				Namespace: "bar",
				Name:      "foo-ca-bundle-" + stunnelSecret,
			}, caBundleSecret)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}
			if !reflect.DeepEqual(tt.labels, clientSecret.Labels) {
				t.Errorf("labels on secretSecret = %#v, wanted %#v", secretSecret.Labels, tt.labels)
			}
		})
	}
}
