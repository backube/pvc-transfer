package loadbalancer

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/backube/pvc-transfer/endpoint"
	logrtesting "github.com/go-logr/logr/testing"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func fakeClientWithObjects(objs ...client.Object) client.WithWatch {
	scheme := runtime.NewScheme()
	AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func testSVCObjects(admitted bool, namespacedName types.NamespacedName, labels map[string]string, reference []metav1.OwnerReference, ingressPort int32, backendPort int32) []client.Object {
	svcStatus := corev1.ServiceStatus{}

	if admitted {
		svcStatus = corev1.ServiceStatus{
			LoadBalancer: corev1.LoadBalancerStatus{
				Ingress: []corev1.LoadBalancerIngress{
					{Hostname: "foo.bar"},
				},
			},
		}
	}
	return []client.Object{
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:            namespacedName.Name,
				Namespace:       namespacedName.Namespace,
				Labels:          labels,
				OwnerReferences: reference,
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{
					{
						Name:     namespacedName.Name,
						Protocol: corev1.ProtocolTCP,
						Port:     ingressPort,
						TargetPort: intstr.IntOrString{
							Type:   intstr.Int,
							IntVal: backendPort,
						},
					}},
				Selector: labels,
				Type:     corev1.ServiceTypeLoadBalancer,
			},
			Status: svcStatus,
		},
	}
}

func testOwnerReferences() []metav1.OwnerReference {
	return []metav1.OwnerReference{metav1.OwnerReference{
		APIVersion:         "api.foo",
		Kind:               "Test",
		Name:               "bar",
		UID:                "123",
		Controller:         pointer.Bool(true),
		BlockOwnerDeletion: pointer.Bool(true),
	}}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name            string
		namespacedName  types.NamespacedName
		labels          map[string]string
		ownerReferences []metav1.OwnerReference
		want            endpoint.Endpoint
		wantErr         bool
		admitted        bool
		alreadyCreated  bool
		ingressPort     int32
		backendPort     int32
	}{
		{
			name:            "test with no svc objects",
			namespacedName:  types.NamespacedName{Namespace: "bar", Name: "foo"},
			labels:          map[string]string{"test": "me"},
			ownerReferences: testOwnerReferences(),
			wantErr:         true,
			admitted:        false,
			alreadyCreated:  false,
			ingressPort:     8080,
			backendPort:     8080,
		},
		{
			name:            "test with svc objects, already created",
			namespacedName:  types.NamespacedName{Namespace: "bar", Name: "foo"},
			labels:          map[string]string{"test": "me"},
			ownerReferences: testOwnerReferences(),
			wantErr:         true,
			admitted:        false,
			alreadyCreated:  true,
			ingressPort:     8080,
			backendPort:     8080,
		},
		{
			name:            "test with svc objects, already created, already admitted",
			namespacedName:  types.NamespacedName{Namespace: "bar", Name: "foo"},
			labels:          map[string]string{"test": "me"},
			ownerReferences: testOwnerReferences(),
			wantErr:         false,
			admitted:        true,
			alreadyCreated:  true,
			ingressPort:     8080,
			backendPort:     8080,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fakeClient client.WithWatch
			if tt.alreadyCreated {
				fakeClient = fakeClientWithObjects(testSVCObjects(tt.admitted, tt.namespacedName, tt.labels, tt.ownerReferences, tt.ingressPort, tt.backendPort)...)
			} else {
				fakeClient = fakeClientWithObjects()
			}
			ctx := context.WithValue(context.Background(), "test", tt.name)
			fakeLogger := logrtesting.TestLogger{t}
			_, err := New(ctx, fakeClient, fakeLogger, tt.namespacedName, tt.backendPort, tt.ingressPort, tt.labels, tt.ownerReferences)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			svc := &corev1.Service{}
			err = fakeClient.Get(context.Background(), tt.namespacedName, svc)
			if err != nil {
				t.Errorf("got an unexpected error from test client: %#v", err)
			}

			if !reflect.DeepEqual(svc.Spec.Selector, tt.labels) &&
				svc.Spec.Ports[0].Port != tt.ingressPort &&
				svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
				t.Errorf("did not reconcile properly, got %#v", svc)
			}
			if !tt.wantErr && svc.Status.LoadBalancer.Ingress[0].Hostname != "foo.bar" {
				t.Errorf("expected healthy loadbalancer, hostname is not populated, got %#v", svc)
			}
		})
	}
}

func Test_route_MarkForCleanup(t *testing.T) {
	tests := []struct {
		name           string
		namespacedName types.NamespacedName
		labels         map[string]string
		wantErr        bool
		key            string
		value          string
	}{
		{
			name:           "test with svc objects",
			namespacedName: types.NamespacedName{Namespace: "bar", Name: "foo"},
			labels:         map[string]string{"test": "me"},
			wantErr:        false,
			key:            "cleanup-key",
			value:          "cleanup-value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &loadBalancer{
				namespacedName:  tt.namespacedName,
				labels:          tt.labels,
				ownerReferences: testOwnerReferences(),
				logger:          logrtesting.TestLogger{t},
			}
			ctx := context.WithValue(context.Background(), "test", tt.name)
			fakeClient := fakeClientWithObjects(testSVCObjects(true, tt.namespacedName, tt.labels, r.ownerReferences, 8080, 8080)...)
			if err := r.MarkForCleanup(ctx, fakeClient, tt.key, tt.value); (err != nil) != tt.wantErr {
				t.Errorf("MarkForCleanup() error = %v, wantErr %v", err, tt.wantErr)
			}

			svc := &corev1.Service{}
			err := fakeClient.Get(context.Background(), tt.namespacedName, svc)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}
			tt.labels[tt.key] = tt.value
			if !reflect.DeepEqual(tt.labels, svc.Labels) {
				t.Errorf("labels on route = %#v, wanted %#v", svc.Labels, tt.labels)
			}

		})
	}
}
