package ingress

import (
	"context"
	"fmt"

	"github.com/backube/pvc-transfer/endpoint"
	"github.com/backube/pvc-transfer/internal/utils"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	NginxIngressPassthroughAnnotation = "nginx.ingress.kubernetes.io/ssl-passthrough"
)

const (
	backendPort = 6443
	ingressPort = 443
)

type ingress struct {
	logger logr.Logger

	namespacedName     types.NamespacedName
	labels             map[string]string
	ingressAnnotations map[string]string
	ownerReferences    []metav1.OwnerReference
	ingressPort        int32
	backendPort        int32
	ingressClassName   *string
	subdomain          *string
	hostname           string
}

func (i *ingress) NamespacedName() types.NamespacedName {
	return i.namespacedName
}

func (i *ingress) Hostname() string {
	return i.hostname
}

func (i *ingress) BackendPort() int32 {
	return i.backendPort
}

func (i *ingress) IngressPort() int32 {
	return i.ingressPort
}

func (i *ingress) IsHealthy(ctx context.Context, c client.Client) (bool, error) {
	svc := &corev1.Service{}
	err := c.Get(ctx, i.NamespacedName(), svc)
	if err != nil {
		i.logger.Error(err, "failed to get service")
		return false, err
	}

	ingress := &networkingv1.Ingress{}
	err = c.Get(ctx, i.NamespacedName(), ingress)
	if err != nil {
		i.logger.Error(err, "failed to get ingress")
		return false, err
	}
	if len(ingress.Spec.Rules) > 0 && ingress.Spec.Rules[0].Host == "" {
		return false, fmt.Errorf("host not set for ingress: %s", ingress)
	}
	if len(ingress.Status.LoadBalancer.Ingress) > 0 {
		if ingress.Status.LoadBalancer.Ingress[0].Hostname != "" {
			return true, nil
		}
		if ingress.Status.LoadBalancer.Ingress[0].IP != "" {
			return true, nil
		}
	}
	i.logger.Info("endpoint is unhealthy")
	return false, nil
}

func (i *ingress) MarkForCleanup(ctx context.Context, c client.Client, key, value string) error {
	i.logger.Info("marking endpoint evc for cleanup")
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      i.namespacedName.Name,
			Namespace: i.namespacedName.Namespace,
		},
	}
	err := utils.UpdateWithLabel(ctx, c, svc, key, value)
	if err != nil {
		i.logger.Error(err, "failed to mark endpoint svc for cleanup", "svc", i)
		return err
	}
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      i.namespacedName.Name,
			Namespace: i.namespacedName.Namespace,
		},
	}
	err = utils.UpdateWithLabel(ctx, c, ingress, key, value)
	if err != nil {
		i.logger.Error(err, "failed to mark endpoint ingress for cleanup", "ingress", i)
		return err
	}
	return nil
}

// AddToScheme should be used as soon as scheme is created to add
// core  objects for encoding/decoding
func AddToScheme(scheme *runtime.Scheme) error {
	return networkingv1.AddToScheme(scheme)
}

// APIsToWatch give a list of APIs to watch if using this package
// to deploy the endpoint
func APIsToWatch() ([]client.Object, error) {
	return []client.Object{
		&corev1.Service{},
		&networkingv1.Ingress{}}, nil
}

// New creates an ingress endpoint object, deploys the resources on  the cluster
// and then checks for the health of the loadbalancer. Before using the fields
// it is always recommended to check if the loadbalancer is healthy.
//
// In order to generate the right RBAC, add the following lines to the Reconcile function annotations.
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
func New(ctx context.Context, c client.Client, logger logr.Logger,
	namespacedName types.NamespacedName,
	ingressClassName *string,
	subdomain *string,
	labels, ingressAnnotations map[string]string,
	ownerReferences []metav1.OwnerReference) (endpoint.Endpoint, error) {
	ingressLogger := logger.WithValues("ingress", namespacedName)

	ingressEndpoint := &ingress{
		logger:             ingressLogger,
		namespacedName:     namespacedName,
		labels:             labels,
		ingressAnnotations: ingressAnnotations,
		ownerReferences:    ownerReferences,
		backendPort:        backendPort,
		ingressPort:        ingressPort,
		ingressClassName:   ingressClassName,
		subdomain:          subdomain,
	}

	if ingressClassName == nil || *ingressClassName == "" {
		ingressLogger.Info("ingress class not specified, using default ingress class in the cluster")
	}

	if subdomain == nil {
		return nil, fmt.Errorf("subdomain cannot be nil")
	}

	ingressEndpoint.setHostname(*subdomain)

	err := ingressEndpoint.reconcileServiceForIngress(ctx, c)
	if err != nil {
		return nil, err
	}

	err = ingressEndpoint.reconcileIngress(ctx, c)
	if err != nil {
		return nil, err
	}

	return ingressEndpoint, nil
}

func (i *ingress) setHostname(subdomain string) {
	prefix := fmt.Sprintf("%s-%s",
		i.namespacedName.Name,
		i.namespacedName.Namespace)
	if len(prefix) > 62 {
		prefix = prefix[0:62]
	}
	i.hostname = fmt.Sprintf(
		"%s.%s", prefix, subdomain)
}

func (i *ingress) reconcileServiceForIngress(ctx context.Context, c client.Client) error {
	port := i.BackendPort()
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      i.namespacedName.Name,
			Namespace: i.namespacedName.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, c, service, func() error {
		service.Labels = i.labels
		service.OwnerReferences = i.ownerReferences

		service.Spec.Ports = []corev1.ServicePort{
			{
				Name:     i.NamespacedName().Name,
				Protocol: corev1.ProtocolTCP,
				Port:     port,
				TargetPort: intstr.IntOrString{
					Type:   intstr.Int,
					IntVal: port,
				},
			},
		}

		service.Spec.Selector = i.labels
		service.Spec.Type = corev1.ServiceTypeClusterIP
		return nil
	})

	return err
}

func (i *ingress) reconcileIngress(ctx context.Context, c client.Client) error {
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      i.namespacedName.Name,
			Namespace: i.namespacedName.Namespace,
		},
	}
	pathType := networkingv1.PathTypePrefix
	_, err := controllerutil.CreateOrUpdate(ctx, c, ingress, func() error {
		ingress.Labels = i.labels
		ingress.OwnerReferences = i.ownerReferences
		ingress.Annotations = i.ingressAnnotations

		if i.ingressClassName != nil {
			ingress.Spec.IngressClassName = i.ingressClassName
		}

		ingress.Spec.Rules = []networkingv1.IngressRule{
			{
				Host: i.Hostname(),
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{
							{
								Path:     "/",
								PathType: &pathType,
								Backend: networkingv1.IngressBackend{
									Service: &networkingv1.IngressServiceBackend{
										Name: i.namespacedName.Name,
										Port: networkingv1.ServiceBackendPort{
											Number: i.backendPort,
										},
									},
								},
							},
						},
					},
				},
			},
		}
		return nil
	})
	return err
}
