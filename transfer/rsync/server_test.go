package rsync

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/backube/pvc-transfer/transfer"
	"github.com/backube/pvc-transfer/transport"
	"github.com/backube/pvc-transfer/transport/stunnel"
	logrtesting "github.com/go-logr/logr/testing"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

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

type fakeTransportServer struct {
	transportType transport.Type
}

func (f *fakeTransportServer) NamespacedName() types.NamespacedName {
	panic("implement me")
}

func (f *fakeTransportServer) ListenPort() int32 {
	panic("implement me")
}

func (f *fakeTransportServer) ConnectPort() int32 {
	panic("implement me")
}

func (f *fakeTransportServer) Containers() []corev1.Container {
	return []corev1.Container{{Name: "fakeTransportServerContainer"}}
}

func (f *fakeTransportServer) Volumes() []corev1.Volume {
	return []corev1.Volume{{
		Name: "fakeVolume",
	}}
}

func (f *fakeTransportServer) Type() transport.Type {
	return f.transportType
}

func (f *fakeTransportServer) Credentials() types.NamespacedName {
	panic("implement me")
}

func (f *fakeTransportServer) Hostname() string {
	panic("implement me")
}

func (f *fakeTransportServer) MarkForCleanup(ctx context.Context, c ctrlclient.Client, key, value string) error {
	panic("implement me")
}

func fakeClientWithObjects(objs ...ctrlclient.Object) ctrlclient.WithWatch {
	scheme := runtime.NewScheme()
	_ = AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func Test_server_reconcileConfigMap(t *testing.T) {
	tests := []struct {
		name            string
		username        string
		pvcList         transfer.PVCList
		transportServer transport.Transport
		labels          map[string]string
		ownerRefs       []metav1.OwnerReference
		namespace       string
		wantErr         bool
		nameSuffix      string
		objects         []ctrlclient.Object
	}{
		{
			name:     "test with no configmap",
			username: "root",
			pvcList: transfer.NewSingletonPVC(&corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc",
					Namespace: "foo",
				},
			}),
			transportServer: &fakeTransportServer{stunnel.TransportTypeStunnel},
			labels:          map[string]string{"test": "me"},
			ownerRefs:       testOwnerReferences(),
			wantErr:         false,
			nameSuffix:      "foo",
			objects:         []ctrlclient.Object{},
		},
		{
			name:     "test with invalid configmap",
			username: "root",
			pvcList: transfer.NewSingletonPVC(&corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc",
					Namespace: "foo",
				},
			}),
			transportServer: &fakeTransportServer{stunnel.TransportTypeStunnel},
			labels:          map[string]string{"test": "me"},
			ownerRefs:       testOwnerReferences(),
			wantErr:         false,
			nameSuffix:      "foo",
			objects: []ctrlclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:            rsyncConfig + "-foo",
						Namespace:       "foo",
						Labels:          map[string]string{"foo": "bar"},
						OwnerReferences: []metav1.OwnerReference{},
					},
				},
			},
		},
		{
			name:     "test with valid configmap",
			username: "root",
			pvcList: transfer.NewSingletonPVC(&corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc",
					Namespace: "foo",
				},
			}),
			transportServer: &fakeTransportServer{stunnel.TransportTypeStunnel},
			labels:          map[string]string{"test": "me"},
			ownerRefs:       testOwnerReferences(),
			wantErr:         false,
			nameSuffix:      "foo",
			objects: []ctrlclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:            rsyncConfig + "-foo",
						Namespace:       "foo",
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
			s := &server{
				logger:          logrtesting.TestLogger{t},
				nameSuffix:      tt.nameSuffix,
				pvcList:         tt.pvcList,
				labels:          tt.labels,
				ownerRefs:       tt.ownerRefs,
				transportServer: tt.transportServer,
			}
			ctx := context.WithValue(context.Background(), "test", tt.name)
			if err := s.reconcileConfigMap(ctx, fakeClient, tt.namespace); (err != nil) != tt.wantErr {
				t.Errorf("reconcileConfigMap() error = %v, wantErr %v", err, tt.wantErr)
			}
			cm := &corev1.ConfigMap{}
			err := fakeClient.Get(context.Background(), types.NamespacedName{
				Namespace: tt.namespace,
				Name:      rsyncConfig + "-" + tt.nameSuffix,
			}, cm)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}

			configData, ok := cm.Data["rsyncd.conf"]
			if !ok {
				t.Error("unable to find rsyncd config data in configmap")
			}
			if !strings.Contains(configData, "syslog facility = local7") {
				t.Error("configmap data does not contain the right data")
			}

			if !reflect.DeepEqual(cm.Labels, tt.labels) {
				t.Error("configmap does not have the right labels")
			}

			if !reflect.DeepEqual(cm.OwnerReferences, tt.ownerRefs) {
				t.Error("configmap does not have the right owner references")
			}
		})
	}
}

func Test_server_reconcilePod(t *testing.T) {
	tests := []struct {
		name            string
		username        string
		pvcList         transfer.PVCList
		transportServer transport.Transport
		labels          map[string]string
		ownerRefs       []metav1.OwnerReference
		namespace       string
		wantErr         bool
		nameSuffix      string
		listenPort      int32
		objects         []ctrlclient.Object
	}{
		{
			name:     "test with no pod",
			username: "root",
			pvcList: transfer.NewSingletonPVC(&corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc",
					Namespace: "foo",
				},
			}),
			listenPort:      8080,
			transportServer: &fakeTransportServer{stunnel.TransportTypeStunnel},
			labels:          map[string]string{"test": "me"},
			ownerRefs:       testOwnerReferences(),
			wantErr:         false,
			nameSuffix:      "foo",
			objects:         []ctrlclient.Object{},
		},
		{
			name:     "test with invalid pod",
			username: "root",
			pvcList: transfer.NewSingletonPVC(&corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc",
					Namespace: "foo",
				},
			}),
			listenPort:      8080,
			transportServer: &fakeTransportServer{stunnel.TransportTypeStunnel},
			labels:          map[string]string{"test": "me"},
			ownerRefs:       testOwnerReferences(),
			wantErr:         false,
			nameSuffix:      "foo",
			objects: []ctrlclient.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rsync-server-foo",
						Namespace:       "foo",
						Labels:          map[string]string{"foo": "bar"},
						OwnerReferences: []metav1.OwnerReference{},
					},
				},
			},
		},
		{
			name:     "test with valid pod",
			username: "root",
			pvcList: transfer.NewSingletonPVC(&corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc",
					Namespace: "foo",
				},
			}),
			listenPort:      8080,
			transportServer: &fakeTransportServer{stunnel.TransportTypeStunnel},
			labels:          map[string]string{"test": "me"},
			ownerRefs:       testOwnerReferences(),
			wantErr:         false,
			nameSuffix:      "foo",
			objects: []ctrlclient.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "rsync-server-foo",
						Namespace:       "foo",
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
			s := &server{
				logger:          logrtesting.TestLogger{t},
				pvcList:         tt.pvcList,
				transportServer: tt.transportServer,
				listenPort:      tt.listenPort,
				nameSuffix:      tt.nameSuffix,
				labels:          tt.labels,
				ownerRefs:       tt.ownerRefs,
			}
			if err := s.reconcilePod(ctx, fakeClient, tt.namespace); (err != nil) != tt.wantErr {
				t.Errorf("reconcilePod() error = %v, wantErr %v", err, tt.wantErr)
			}

			pod := &corev1.Pod{}
			err := fakeClient.Get(context.Background(), types.NamespacedName{
				Namespace: tt.namespace,
				Name:      "rsync-server-" + tt.nameSuffix,
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
		})
	}
}
