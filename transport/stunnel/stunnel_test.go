package stunnel

import (
	"context"
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
						Name:      "foo-stunnel-credentials",
						Namespace: "bar",
						Labels:    map[string]string{"test": "me"},
					},
					Data: map[string][]byte{"tls.crt": certificateBundle.ServerCrt.Bytes()},
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
						Name:      "foo-stunnel-credentials",
						Namespace: "bar",
						Labels:    map[string]string{"test": "me"},
					},
					Data: map[string][]byte{"tls.key": certificateBundle.ServerKey.Bytes()},
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
						Name:      "foo-stunnel-credentials",
						Namespace: "bar",
						Labels:    map[string]string{"test": "me"},
					},
					Data: map[string][]byte{"tls.key": certificateBundle.ServerKey.Bytes(), "tls.crt": certificateBundle.ServerKey.Bytes()},
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
						Name:      "foo-stunnel-credentials",
						Namespace: "bar",
						Labels:    map[string]string{"test": "me"},
					},
					Data: map[string][]byte{"tls.key": certificateBundle.ServerKey.Bytes(), "tls.crt": certificateBundle.ServerCrt.Bytes(), "ca.crt": certificateBundle.CACrt.Bytes()},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &server{
				logger:         logrtesting.TestLogger{t},
				namespacedName: tt.namespacedName,
				options: &transport.Options{
					Labels: tt.labels,
					Owners: testOwnerReferences(),
				},
			}
			ctx := context.WithValue(context.Background(), "test", tt.name)
			found, err := isSecretValid(ctx, fakeClientWithObjects(tt.objects...), s.logger, s.namespacedName, stunnelSecret)
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
