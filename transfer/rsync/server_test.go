package rsync

import (
	"context"
	"fmt"
	logrtesting "github.com/go-logr/logr/testing"
	"reflect"
	"strings"
	"testing"

	"github.com/backube/pvc-transfer/transfer"
	"github.com/backube/pvc-transfer/transport"
	"github.com/backube/pvc-transfer/transport/stunnel"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func (f *fakeTransportServer) MarkForCleanup(ctx context.Context, c client.Client, key, value string) error {
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
		objects         []client.Object
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
			objects:         []client.Object{},
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
			objects: []client.Object{
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
			objects: []client.Object{
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
				username:        tt.username,
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

func Test_server_reconcileSecret(t *testing.T) {
	tests := []struct {
		name       string
		username   string
		password   string
		labels     map[string]string
		ownerRefs  []metav1.OwnerReference
		namespace  string
		wantErr    bool
		nameSuffix string
		objects    []client.Object
	}{
		{
			name:       "test if password is empty",
			username:   "root",
			password:   "",
			labels:     map[string]string{"test": "me"},
			ownerRefs:  testOwnerReferences(),
			wantErr:    true,
			nameSuffix: "foo",
			objects:    []client.Object{},
		},
		{
			name:       "secret with invalid data",
			username:   "root",
			password:   "root",
			labels:     map[string]string{"test": "me"},
			ownerRefs:  testOwnerReferences(),
			wantErr:    false,
			nameSuffix: "foo",
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "backube-rsync-foo",
						Namespace:       "foo",
						Labels:          map[string]string{"foo": "bar"},
						OwnerReferences: []metav1.OwnerReference{},
					},
				},
			},
		},
		{
			name:       "secret with valid data",
			username:   "root",
			password:   "root",
			labels:     map[string]string{"test": "me"},
			ownerRefs:  testOwnerReferences(),
			wantErr:    false,
			nameSuffix: "foo",
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "backube-rsync-foo",
						Namespace:       "foo",
						Labels:          map[string]string{"test": "me"},
						OwnerReferences: testOwnerReferences(),
					},
					Data: map[string][]byte{
						"credentials": []byte("root:root"),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		fakeClient := fakeClientWithObjects(tt.objects...)
		ctx := context.WithValue(context.Background(), "test", tt.name)

		t.Run(tt.name, func(t *testing.T) {
			s := &server{
				logger:     logrtesting.TestLogger{t},
				username:   tt.username,
				password:   tt.password,
				labels:     tt.labels,
				ownerRefs:  tt.ownerRefs,
				nameSuffix: tt.nameSuffix,
			}
			err := s.reconcileSecret(ctx, fakeClient, tt.namespace)
			if (err != nil) != tt.wantErr {
				t.Errorf("reconcileSecret() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			secret := &corev1.Secret{}
			err = fakeClient.Get(context.Background(), types.NamespacedName{
				Namespace: tt.namespace,
				Name:      rsyncSecretPrefix + "-" + tt.nameSuffix,
			}, secret)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}

			secretData, ok := secret.Data["credentials"]
			if !ok {
				t.Error("unable to find credentials in secret")
			}
			if !strings.Contains(string(secretData), tt.username+":"+tt.password) {
				t.Error("secrets does not contain the right data")
			}

			if !reflect.DeepEqual(secret.Labels, tt.labels) {
				t.Error("secret does not have the right labels")
			}

			if !reflect.DeepEqual(secret.OwnerReferences, tt.ownerRefs) {
				t.Error("secret does not have the right owner references")
			}
		})
	}
}

func Test_server_reconcileServiceAccount(t *testing.T) {
	tests := []struct {
		name       string
		labels     map[string]string
		ownerRefs  []metav1.OwnerReference
		namespace  string
		wantErr    bool
		nameSuffix string
		objects    []client.Object
	}{
		{
			name:       "test with missing service account",
			namespace:  "foo",
			labels:     map[string]string{"test": "me"},
			ownerRefs:  testOwnerReferences(),
			wantErr:    false,
			nameSuffix: "foo",
			objects:    []client.Object{},
		},
		{
			name:       "test with invalid service account",
			namespace:  "foo",
			labels:     map[string]string{"test": "me"},
			ownerRefs:  testOwnerReferences(),
			wantErr:    false,
			nameSuffix: "foo",
			objects: []client.Object{
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:            fmt.Sprintf("%s-%s", rsyncServiceAccount, "foo"),
						Namespace:       "foo",
						Labels:          map[string]string{"foo": "bar"},
						OwnerReferences: []metav1.OwnerReference{},
					},
				},
			},
		},
		{
			name:       "test with valid service account",
			namespace:  "foo",
			labels:     map[string]string{"test": "me"},
			ownerRefs:  testOwnerReferences(),
			wantErr:    false,
			nameSuffix: "foo",
			objects: []client.Object{
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:            fmt.Sprintf("%s-%s", rsyncServiceAccount, "foo"),
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
				logger:     logrtesting.TestLogger{t},
				nameSuffix: tt.nameSuffix,
				labels:     tt.labels,
				ownerRefs:  tt.ownerRefs,
			}
			if err := s.reconcileServiceAccount(ctx, fakeClient, tt.namespace); (err != nil) != tt.wantErr {
				t.Errorf("reconcileServiceAccount() error = %v, wantErr %v", err, tt.wantErr)
			}

			sa := &corev1.ServiceAccount{}
			err := fakeClient.Get(context.Background(), types.NamespacedName{
				Namespace: tt.namespace,
				Name:      rsyncServiceAccount + "-" + tt.nameSuffix,
			}, sa)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}

			if !reflect.DeepEqual(sa.Labels, tt.labels) {
				t.Error("sa does not have the right labels")
			}
			if !reflect.DeepEqual(sa.OwnerReferences, tt.ownerRefs) {
				t.Error("sa does not have the right owner references")
			}
		})
	}
}

func Test_server_reconcileRole(t *testing.T) {
	tests := []struct {
		name       string
		labels     map[string]string
		ownerRefs  []metav1.OwnerReference
		namespace  string
		wantErr    bool
		nameSuffix string
		objects    []client.Object
	}{
		{
			name:       "test with missing role",
			namespace:  "foo",
			labels:     map[string]string{"test": "me"},
			ownerRefs:  testOwnerReferences(),
			wantErr:    false,
			nameSuffix: "foo",
			objects:    []client.Object{},
		},
		{
			name:       "test with invalid role",
			namespace:  "foo",
			labels:     map[string]string{"test": "me"},
			ownerRefs:  testOwnerReferences(),
			wantErr:    false,
			nameSuffix: "foo",
			objects: []client.Object{
				&rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Name:            fmt.Sprintf("%s-%s", rsyncRole, "foo"),
						Namespace:       "foo",
						Labels:          map[string]string{"foo": "bar"},
						OwnerReferences: []metav1.OwnerReference{},
					},
				},
			},
		},
		{
			name:       "test with valid role",
			namespace:  "foo",
			labels:     map[string]string{"test": "me"},
			ownerRefs:  testOwnerReferences(),
			wantErr:    false,
			nameSuffix: "foo",
			objects: []client.Object{
				&rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Name:            fmt.Sprintf("%s-%s", rsyncRole, "foo"),
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
			s := &server{
				logger:     logrtesting.TestLogger{t},
				nameSuffix: tt.nameSuffix,
				labels:     tt.labels,
				ownerRefs:  tt.ownerRefs,
			}

			fakeClient := fakeClientWithObjects(tt.objects...)
			ctx := context.WithValue(context.Background(), "test", tt.name)

			if err := s.reconcileRole(ctx, fakeClient, tt.namespace); (err != nil) != tt.wantErr {
				t.Errorf("reconcileRole() error = %v, wantErr %v", err, tt.wantErr)
			}

			role := &rbacv1.Role{}
			err := fakeClient.Get(context.Background(), types.NamespacedName{
				Namespace: tt.namespace,
				Name:      rsyncRole + "-" + tt.nameSuffix,
			}, role)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}

			if !reflect.DeepEqual(role.Labels, tt.labels) {
				t.Error("role does not have the right labels")
			}
			if !reflect.DeepEqual(role.OwnerReferences, tt.ownerRefs) {
				t.Error("role does not have the right owner references")
			}

		})
	}
}

func Test_server_reconcileRoleBinding(t *testing.T) {
	tests := []struct {
		name       string
		labels     map[string]string
		ownerRefs  []metav1.OwnerReference
		namespace  string
		wantErr    bool
		nameSuffix string
		objects    []client.Object
	}{
		{
			name:       "test with missing rolebinding",
			namespace:  "foo",
			labels:     map[string]string{"test": "me"},
			ownerRefs:  testOwnerReferences(),
			wantErr:    false,
			nameSuffix: "foo",
			objects:    []client.Object{},
		},
		{
			name:       "test with invalid rolebinding",
			namespace:  "foo",
			labels:     map[string]string{"test": "me"},
			ownerRefs:  testOwnerReferences(),
			wantErr:    false,
			nameSuffix: "foo",
			objects: []client.Object{
				&rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:            fmt.Sprintf("%s-%s", rsyncRoleBinding, "foo"),
						Namespace:       "foo",
						Labels:          map[string]string{"foo": "bar"},
						OwnerReferences: []metav1.OwnerReference{},
					},
				},
			},
		},
		{
			name:       "test with valid rolebinding",
			namespace:  "foo",
			labels:     map[string]string{"test": "me"},
			ownerRefs:  testOwnerReferences(),
			wantErr:    false,
			nameSuffix: "foo",
			objects: []client.Object{
				&rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:            fmt.Sprintf("%s-%s", rsyncRoleBinding, "foo"),
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
			s := &server{
				logger:     logrtesting.TestLogger{t},
				nameSuffix: tt.nameSuffix,
				labels:     tt.labels,
				ownerRefs:  tt.ownerRefs,
			}

			fakeClient := fakeClientWithObjects(tt.objects...)
			ctx := context.WithValue(context.Background(), "test", tt.name)

			if err := s.reconcileRoleBinding(ctx, fakeClient, tt.namespace); (err != nil) != tt.wantErr {
				t.Errorf("reconcileRoleBinding() error = %v, wantErr %v", err, tt.wantErr)
			}
			rolebinding := &rbacv1.RoleBinding{}
			err := fakeClient.Get(context.Background(), types.NamespacedName{
				Namespace: tt.namespace,
				Name:      rsyncRoleBinding + "-" + tt.nameSuffix,
			}, rolebinding)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}

			if !reflect.DeepEqual(rolebinding.Labels, tt.labels) {
				t.Error("rolebinding does not have the right labels")
			}
			if !reflect.DeepEqual(rolebinding.OwnerReferences, tt.ownerRefs) {
				t.Error("rolebinding does not have the right owner references")
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
		objects         []client.Object
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
			objects:         []client.Object{},
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
			objects: []client.Object{
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
			objects: []client.Object{
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
				username:        tt.username,
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