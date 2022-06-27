package rsync

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/backube/pvc-transfer/transfer"
	"github.com/backube/pvc-transfer/transport"
	"github.com/backube/pvc-transfer/transport/stunnel"
	logrtesting "github.com/go-logr/logr/testing"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type fakeTransportClient struct {
	transportType transport.Type
}

func (f *fakeTransportClient) NamespacedName() types.NamespacedName {
	panic("implement me")
}

func (f *fakeTransportClient) ListenPort() int32 {
	return 8080
}

func (f *fakeTransportClient) ConnectPort() int32 {
	panic("implement me")
}

func (f *fakeTransportClient) Containers() []corev1.Container {
	return []corev1.Container{{Name: stunnel.Container}}
}

func (f *fakeTransportClient) Volumes() []corev1.Volume {
	return []corev1.Volume{{
		Name: "fakeVolume",
	}}
}

func (f *fakeTransportClient) Type() transport.Type {
	return f.transportType
}

func (f *fakeTransportClient) Credentials() types.NamespacedName {
	panic("implement me")
}

func (f *fakeTransportClient) Hostname() string {
	return "foo.bar.dev"
}

func (f *fakeTransportClient) MarkForCleanup(ctx context.Context, c ctrlclient.Client, key, value string) error {
	panic("implement me")
}

func Test_client_reconcilePod(t *testing.T) {
	tests := []struct {
		name            string
		username        string
		pvcList         transfer.PVCList
		transportClient transport.Transport
		labels          map[string]string
		ownerRefs       []metav1.OwnerReference
		namespace       string
		wantErr         bool
		nameSuffix      string
		listenPort      int32
		objects         []ctrlclient.Object
	}{
		{
			name:      "test with no pod",
			namespace: "foo",
			username:  "root",
			pvcList: transfer.NewSingletonPVC(&corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc",
					Namespace: "foo",
				},
			}),
			listenPort:      8080,
			transportClient: &fakeTransportClient{transportType: stunnel.TransportTypeStunnel},
			labels:          map[string]string{"test": "me"},
			ownerRefs:       testOwnerReferences(),
			wantErr:         false,
			nameSuffix:      "foo",
			objects:         []ctrlclient.Object{},
		},
		{
			name:      "test with invalid pod",
			namespace: "foo",
			username:  "root",
			pvcList: transfer.NewSingletonPVC(&corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc",
					Namespace: "foo",
				},
			}),
			listenPort:      8080,
			transportClient: &fakeTransportClient{transportType: stunnel.TransportTypeStunnel},
			labels:          map[string]string{"test": "me"},
			ownerRefs:       testOwnerReferences(),
			wantErr:         false,
			nameSuffix:      "foo",
			objects: []ctrlclient.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rsync-client-data-0",
						Namespace:       "foo",
						Labels:          map[string]string{"foo": "bar"},
						OwnerReferences: []metav1.OwnerReference{},
					},
				},
			},
		},
		{
			name:      "test with valid pod",
			namespace: "foo",
			username:  "root",
			pvcList: transfer.NewSingletonPVC(&corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc",
					Namespace: "foo",
				},
			}),
			listenPort:      8080,
			transportClient: &fakeTransportClient{transportType: stunnel.TransportTypeStunnel},
			labels:          map[string]string{"test": "me"},
			ownerRefs:       testOwnerReferences(),
			wantErr:         false,
			nameSuffix:      "foo",
			objects: []ctrlclient.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rsync-client-foo",
						Namespace:       "foo",
						Annotations:     map[string]string{"pvc": "test-pvc"},
						Labels:          map[string]string{"test": "me"},
						OwnerReferences: testOwnerReferences(),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fakeClientWithObjects(tt.objects...)
			ctx := context.WithValue(context.Background(), "test", tt.name)
			s := &client{
				logger:          logrtesting.TestLogger{t},
				username:        tt.username,
				pvcList:         tt.pvcList,
				nameSuffix:      tt.nameSuffix,
				labels:          tt.labels,
				ownerRefs:       tt.ownerRefs,
				transportClient: tt.transportClient,
			}
			if err := s.reconcilePod(ctx, fakeClient, tt.namespace); (err != nil) != tt.wantErr {
				t.Errorf("reconcilePod() error = %v, wantErr %v", err, tt.wantErr)
			}

			pod := &corev1.Pod{}
			err := fakeClient.Get(context.Background(), types.NamespacedName{
				Namespace: tt.namespace,
				Name:      "rsync-client-foo",
			}, pod)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}

			if !reflect.DeepEqual(pod.Labels, tt.labels) {
				t.Error("pod does not have the right labels")
			}
			if !reflect.DeepEqual(pod.OwnerReferences, tt.ownerRefs) {
				t.Error("pod does not have the right owner references")
			}
			if !reflect.DeepEqual(pod.Annotations, map[string]string{"pvc": tt.pvcList.PVCs()[0].Claim().Name}) {
				t.Error("pod does not have the right annotations")
			}
		})
	}
}

func Test_client_reconcileSecret(t *testing.T) {
	tests := []struct {
		name       string
		password   string
		labels     map[string]string
		ownerRefs  []metav1.OwnerReference
		namespace  string
		wantErr    bool
		nameSuffix string
		objects    []ctrlclient.Object
	}{
		{
			name:       "test with missing secret",
			namespace:  "foo",
			password:   "testme123",
			labels:     map[string]string{"test": "me"},
			ownerRefs:  testOwnerReferences(),
			wantErr:    false,
			nameSuffix: "foo",
			objects:    []ctrlclient.Object{},
		},
		{
			name:       "test with invalid secret data",
			namespace:  "foo",
			password:   "testme123",
			labels:     map[string]string{"test": "me"},
			ownerRefs:  testOwnerReferences(),
			wantErr:    false,
			nameSuffix: "foo",
			objects: []ctrlclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:            fmt.Sprintf("%s-%s", rsyncPassword, "foo"),
						Namespace:       "foo",
						Labels:          map[string]string{"foo": "bar"},
						OwnerReferences: []metav1.OwnerReference{},
					},
					Data: map[string][]byte{
						rsyncPasswordKey: []byte("badPassword"),
					},
				},
			},
		},
		{
			name:       "test with valid secret",
			namespace:  "foo",
			password:   "testme123",
			labels:     map[string]string{"test": "me"},
			ownerRefs:  testOwnerReferences(),
			wantErr:    false,
			nameSuffix: "foo",
			objects: []ctrlclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:            fmt.Sprintf("%s-%s", rsyncPassword, "foo"),
						Namespace:       "foo",
						Labels:          map[string]string{"foo": "bar"},
						OwnerReferences: []metav1.OwnerReference{},
					},
					Data: map[string][]byte{
						rsyncPasswordKey: []byte("testme123"),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &client{
				logger:     logrtesting.TestLogger{t},
				nameSuffix: tt.nameSuffix,
				labels:     tt.labels,
				ownerRefs:  tt.ownerRefs,
				password:   tt.password,
			}

			fakeClient := fakeClientWithObjects(tt.objects...)
			ctx := context.WithValue(context.Background(), "test", tt.name)

			if err := s.reconcilePassword(ctx, fakeClient, tt.namespace); (err != nil) != tt.wantErr {
				t.Errorf("reconcilePassword() error = %v, wantErr %v", err, tt.wantErr)
			}
			secret := &corev1.Secret{}
			err := fakeClient.Get(context.Background(), types.NamespacedName{
				Namespace: tt.namespace,
				Name:      rsyncPassword + "-" + tt.nameSuffix,
			}, secret)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}

			if !reflect.DeepEqual(secret.Labels, tt.labels) {
				t.Error("secret does not have the right labels")
			}
			if !reflect.DeepEqual(secret.OwnerReferences, tt.ownerRefs) {
				t.Error("secret does not have the right owner references")
			}

			if !reflect.DeepEqual(secret.Data, map[string][]byte{rsyncPasswordKey: []byte("testme123")}) {
				t.Errorf("secret does not have the right password")
			}
		})
	}
}
