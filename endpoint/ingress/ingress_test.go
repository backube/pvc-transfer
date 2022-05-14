package ingress

import (
	"context"
	"reflect"
	"testing"

	logrtesting "github.com/go-logr/logr/testing"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_ingress_reconcileServiceForIngress(t *testing.T) {
	type fields struct {
		namespacedName  types.NamespacedName
		labels          map[string]string
		ownerReferences []metav1.OwnerReference
		backendPort     int32
	}
	tests := []struct {
		name    string
		fields  fields
		c       client.Client
		wantErr bool
	}{
		{
			name: "if no services were present, must create a new service",
			fields: fields{
				namespacedName: types.NamespacedName{Name: "test", Namespace: "namespace"},
				backendPort:    443,
			},
			c:       fake.NewClientBuilder().WithObjects().Build(),
			wantErr: false,
		},
		{
			name: "if a service is already present, must update the existing service",
			fields: fields{
				namespacedName: types.NamespacedName{Name: "test", Namespace: "namespace"},
				backendPort:    443,
			},
			c: fake.NewClientBuilder().WithObjects(
				&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "namespace"},
				},
			).Build(),
			wantErr: false,
		},
		{
			name: "if a service is already present with wrong fields, must update the existing service with correct fields",
			fields: fields{
				namespacedName:  types.NamespacedName{Name: "test", Namespace: "namespace"},
				backendPort:     443,
				labels:          map[string]string{"app.kubernetes.io/name": "pvc-transfer"},
				ownerReferences: []metav1.OwnerReference{{Kind: "Pod", Name: "test", UID: "test"}},
			},
			c: fake.NewClientBuilder().WithObjects(
				&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "test",
						Namespace:       "namespace",
						OwnerReferences: []metav1.OwnerReference{{Kind: "Secret", Name: "test", UID: "test"}},
						Labels:          map[string]string{"app": "pvc-transfer"},
					},
					Spec: corev1.ServiceSpec{
						Ports: []corev1.ServicePort{{Port: 999}},
					},
				},
			).Build(),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := &ingress{
				logger:          logrtesting.TestLogger{T: t},
				namespacedName:  tt.fields.namespacedName,
				labels:          tt.fields.labels,
				ownerReferences: tt.fields.ownerReferences,
				backendPort:     tt.fields.backendPort,
			}
			if err := i.reconcileServiceForIngress(context.Background(), tt.c); (err != nil) != tt.wantErr {
				t.Errorf("ingress.reconcileServiceForIngress() error = %v, wantErr %v", err, tt.wantErr)
			}
			svc := &corev1.Service{}
			err := tt.c.Get(context.Background(), tt.fields.namespacedName, svc)
			if (err != nil) != tt.wantErr {
				t.Errorf("ingress.reconcileServiceForIngress() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(svc.Spec.Selector, tt.fields.labels) {
				t.Errorf("ingress.reconcileServiceForIngress() labels = %v, wantLabels %v", svc.Spec.Selector, tt.fields.labels)
			}
			if !reflect.DeepEqual(svc.ObjectMeta.OwnerReferences, tt.fields.ownerReferences) {
				t.Errorf("ingress.reconcileServiceForIngress() ownerReferences = %v, wantOwnerReferences %v", svc.OwnerReferences, tt.fields.ownerReferences)
			}
			if (len(svc.Spec.Ports) < 1) || (svc.Spec.Ports[0].Port != tt.fields.backendPort) {
				t.Errorf("ingress.reconcileServiceForIngress() ports = %v, wantPort %v", svc.Spec.Ports, tt.fields.backendPort)
			}
		})
	}
}

func Test_ingress_IsHealthy(t *testing.T) {
	tests := []struct {
		name           string
		namespacedName types.NamespacedName
		c              client.Client
		want           bool
		wantErr        bool
	}{
		{
			name: "when no svc is present, must return unhealthy and error",
			namespacedName: types.NamespacedName{
				Name:      "test",
				Namespace: "test-ns",
			},
			c:       fake.NewClientBuilder().WithObjects().Build(),
			want:    false,
			wantErr: true,
		},
		{
			name: "when no ingress is present, must return unhealthy and error",
			namespacedName: types.NamespacedName{
				Name:      "test",
				Namespace: "test-ns",
			},
			c: fake.NewClientBuilder().WithObjects(
				&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"}},
			).Build(),
			want:    false,
			wantErr: true,
		},
		{
			name: "when an ingress is present and host is not set, must return unhealthy",
			namespacedName: types.NamespacedName{
				Name:      "test",
				Namespace: "test-ns",
			},
			c: fake.NewClientBuilder().WithObjects(
				&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"}},
				&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"}},
			).Build(),
			want:    false,
			wantErr: false,
		},
		{
			name: "when an ingress is present and loadbalancer host is set, must return healthy",
			namespacedName: types.NamespacedName{
				Name:      "test",
				Namespace: "test-ns",
			},
			c: fake.NewClientBuilder().WithObjects(
				&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"}},
				&networkingv1.Ingress{
					ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
					Status: networkingv1.IngressStatus{
						LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{Hostname: "test.net"}}},
					},
				},
			).Build(),
			want:    true,
			wantErr: false,
		},
		{
			name: "when an ingress is present and loadbalancer ip is set, must return healthy",
			namespacedName: types.NamespacedName{
				Name:      "test",
				Namespace: "test-ns",
			},
			c: fake.NewClientBuilder().WithObjects(
				&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"}},
				&networkingv1.Ingress{
					ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
					Status: networkingv1.IngressStatus{
						LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.0"}}},
					},
				},
			).Build(),
			want:    true,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := &ingress{
				logger:         logrtesting.TestLogger{T: t},
				namespacedName: tt.namespacedName,
			}
			got, err := i.IsHealthy(context.Background(), tt.c)
			if (err != nil) != tt.wantErr {
				t.Errorf("ingress.IsHealthy() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ingress.IsHealthy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_ingress_reconcileIngress(t *testing.T) {
	testSubdomain := "test.net"
	type fields struct {
		namespacedName     types.NamespacedName
		labels             map[string]string
		ingressAnnotations map[string]string
		ownerReferences    []metav1.OwnerReference
		subdomain          string
	}
	tests := []struct {
		name         string
		fields       fields
		c            client.Client
		wantHostname string
		wantErr      bool
	}{
		{
			name: "if no ingress is present, must create a new ingress",
			c:    fake.NewClientBuilder().Build(),
			fields: fields{
				namespacedName: types.NamespacedName{Name: "test", Namespace: "test-ns"},
				subdomain:      testSubdomain,
				ingressAnnotations: map[string]string{
					"nginx.ingress.kubernetes.io/ssl-passthrough": "true",
				},
				ownerReferences: nil,
				labels:          nil,
			},
			wantHostname: "test-test-ns.test.net",
			wantErr:      false,
		},
		{
			name: "if an ingress is present, must update the existing ingress",
			c: fake.NewClientBuilder().WithObjects(
				&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"}}).Build(),
			fields: fields{
				namespacedName: types.NamespacedName{Name: "test", Namespace: "test-ns"},
				subdomain:      testSubdomain,
				ingressAnnotations: map[string]string{
					"nginx.ingress.kubernetes.io/ssl-passthrough": "true",
				},
				ownerReferences: nil,
				labels:          nil,
			},
			wantHostname: "test-test-ns.test.net",
			wantErr:      false,
		},
		{
			name: "if an ingress is present with wrong values, must update the existing ingress with correct values",
			c: fake.NewClientBuilder().WithObjects(
				&networkingv1.Ingress{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test", Namespace: "test-ns",
						Labels:      map[string]string{"app": "pvc-transfer"},
						Annotations: map[string]string{"annotation": "false"},
					},
					Spec: networkingv1.IngressSpec{
						Rules: []networkingv1.IngressRule{{Host: "test.com"}},
					},
				}).Build(),
			fields: fields{
				namespacedName: types.NamespacedName{Name: "test", Namespace: "test-ns"},
				subdomain:      testSubdomain,
				ingressAnnotations: map[string]string{
					"nginx.ingress.kubernetes.io/ssl-passthrough": "true",
				},
				ownerReferences: nil,
				labels:          nil,
			},
			wantHostname: "test-test-ns.test.net",
			wantErr:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := &ingress{
				logger:             logrtesting.TestLogger{T: t},
				namespacedName:     tt.fields.namespacedName,
				labels:             tt.fields.labels,
				ingressAnnotations: tt.fields.ingressAnnotations,
				ownerReferences:    tt.fields.ownerReferences,
				subdomain:          tt.fields.subdomain,
			}
			if err := i.reconcileIngress(context.Background(), tt.c); (err != nil) != tt.wantErr {
				t.Errorf("ingress.reconcileIngress() error = %v, wantErr %v", err, tt.wantErr)
			}
			ingress := &networkingv1.Ingress{}
			err := tt.c.Get(context.Background(), tt.fields.namespacedName, ingress)
			if err != nil {
				t.Errorf("New() Failed to get ingress")
			}
			if !reflect.DeepEqual(ingress.Labels, tt.fields.labels) {
				t.Errorf("New() labels = %v, wantLabels %v", ingress.Labels, tt.fields.labels)
			}
			if !reflect.DeepEqual(ingress.Annotations, tt.fields.ingressAnnotations) {
				t.Errorf("New() annotations = %v, wantAnnotations %v", ingress.Labels, tt.fields.labels)
			}
			if !reflect.DeepEqual(ingress.OwnerReferences, tt.fields.ownerReferences) {
				t.Errorf("New() ownerReferences = %v, wantOwnerReferences %v", ingress.Labels, tt.fields.labels)
			}
			if len(ingress.Spec.Rules) < 1 || ingress.Spec.Rules[0].Host != tt.wantHostname {
				t.Errorf("New() host = %v, wantHost %v", ingress.Spec.Rules, tt.wantHostname)
			}
		})
	}
}
