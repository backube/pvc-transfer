package route

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/backube/pvc-transfer/endpoint"
	logrtesting "github.com/go-logr/logr/testing"
	routev1 "github.com/openshift/api/route/v1"
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

func testRouteObjects(admitted bool, namespacedName types.NamespacedName, labels map[string]string, reference []metav1.OwnerReference) []client.Object {
	routeStatus := routev1.RouteStatus{}
	if admitted {
		routeStatus = routev1.RouteStatus{Ingress: []routev1.RouteIngress{{Conditions: []routev1.RouteIngressCondition{{Type: routev1.RouteAdmitted, Status: corev1.ConditionTrue}}}}}
	}
	return []client.Object{
		&routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Name:            namespacedName.Name,
				Namespace:       namespacedName.Namespace,
				Labels:          labels,
				OwnerReferences: reference,
			},
			Spec: routev1.RouteSpec{
				Host: "foo.bar",
				Port: &routev1.RoutePort{
					TargetPort: intstr.FromInt(TLSTerminationPassthroughPolicyPort),
				},
				TLS: &routev1.TLSConfig{
					Termination: routev1.TLSTerminationPassthrough,
				},
				To: routev1.RouteTargetReference{
					Kind: "Service",
					Name: namespacedName.Name,
				},
			},
			Status: routeStatus,
		},
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
						Port:     TLSTerminationPassthroughPolicyPort,
						TargetPort: intstr.IntOrString{
							Type:   intstr.Int,
							IntVal: TLSTerminationPassthroughPolicyPort,
						},
					},
				},
				Selector: labels,
				Type:     corev1.ServiceTypeClusterIP,
			},
		},
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name            string
		namespacedName  types.NamespacedName
		eType           EndpointType
		subdomain       *string
		labels          map[string]string
		ownerReferences []metav1.OwnerReference
		want            endpoint.Endpoint
		wantErr         bool
		admitted        bool
		alreadyCreated  bool
	}{
		{
			name:            "test with no route objects",
			namespacedName:  types.NamespacedName{Namespace: "bar", Name: "foo"},
			eType:           EndpointTypePassthrough,
			labels:          map[string]string{"test": "me"},
			ownerReferences: testOwnerReferences(),
			subdomain:       nil,
			wantErr:         true,
			admitted:        false,
			alreadyCreated:  false,
		},
		{
			name:            "test with route objects already created",
			namespacedName:  types.NamespacedName{Namespace: "bar", Name: "foo"},
			eType:           EndpointTypePassthrough,
			labels:          map[string]string{"test": "me"},
			ownerReferences: testOwnerReferences(),
			subdomain:       nil,
			wantErr:         true,
			admitted:        false,
			alreadyCreated:  true,
		},
		{
			name:            "test with create route objects already created and already admitted",
			namespacedName:  types.NamespacedName{Namespace: "bar", Name: "foo"},
			eType:           EndpointTypePassthrough,
			labels:          map[string]string{"test": "me"},
			ownerReferences: testOwnerReferences(),
			subdomain:       nil,
			wantErr:         false,
			admitted:        true,
			alreadyCreated:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fakeClient client.WithWatch
			if tt.alreadyCreated {
				fakeClient = fakeClientWithObjects(testRouteObjects(tt.admitted, tt.namespacedName, tt.labels, tt.ownerReferences)...)
			} else {
				fakeClient = fakeClientWithObjects()
			}
			AddToScheme(fakeClient.Scheme())
			ctx := context.WithValue(context.Background(), "test", tt.name)
			fakeLogger := logrtesting.TestLogger{t}
			_, gotError := New(ctx, fakeClient, fakeLogger, tt.namespacedName, tt.eType, tt.subdomain, tt.labels, tt.ownerReferences)
			route := &routev1.Route{}
			err := fakeClient.Get(context.Background(), tt.namespacedName, route)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}

			if route.Spec.TLS != nil && route.Spec.Port.TargetPort.IntVal != int32(TLSTerminationPassthroughPolicyPort) && route.Spec.To.Name == tt.namespacedName.Name {
				t.Errorf("didnt get the expected route %#v", route)
			}

			svc := &corev1.Service{}
			err = fakeClient.Get(context.Background(), tt.namespacedName, svc)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}
			if svc.Spec.Type != corev1.ServiceTypeClusterIP && !reflect.DeepEqual(svc.Spec.Selector, tt.labels) && svc.Spec.Ports[0].Port != TLSTerminationPassthroughPolicyPort {
				t.Errorf("didnt get the expected service %#v", svc)
			}

			if (gotError != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", gotError, tt.wantErr)
				return
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
			name:           "test with route and svc objects",
			namespacedName: types.NamespacedName{Namespace: "bar", Name: "foo"},
			labels:         map[string]string{"test": "me"},
			wantErr:        false,
			key:            "cleanup-key",
			value:          "cleanup-value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &route{
				namespacedName:  tt.namespacedName,
				labels:          tt.labels,
				ownerReferences: testOwnerReferences(),
				logger:          logrtesting.TestLogger{t},
			}
			ctx := context.WithValue(context.Background(), "test", tt.name)
			fakeClient := fakeClientWithObjects(testRouteObjects(true, tt.namespacedName, tt.labels, r.ownerReferences)...)
			if err := r.MarkForCleanup(ctx, fakeClient, tt.key, tt.value); (err != nil) != tt.wantErr {
				t.Errorf("MarkForCleanup() error = %v, wantErr %v", err, tt.wantErr)
			}

			route := &routev1.Route{}
			err := fakeClient.Get(context.Background(), tt.namespacedName, route)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}
			tt.labels[tt.key] = tt.value
			if !reflect.DeepEqual(tt.labels, route.Labels) {
				t.Errorf("labels on route = %#v, wanted %#v", route.Labels, tt.labels)
			}

			svc := &corev1.Service{}
			err = fakeClient.Get(context.Background(), tt.namespacedName, svc)
			if err != nil {
				panic(fmt.Errorf("%#v should not be getting error from fake client", err))
			}
			if !reflect.DeepEqual(tt.labels, svc.Labels) {
				t.Errorf("labels on route = %#v, wanted %#v", route.Labels, tt.labels)
			}

		})
	}
}
