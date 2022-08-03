package service

import (
	"context"
	"fmt"

	"github.com/backube/pvc-transfer/endpoint"
	"github.com/backube/pvc-transfer/internal/utils"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type service struct {
	logger logr.Logger

	hostname        string
	ingressPort     int32
	backendPort     int32
	svcType         corev1.ServiceType
	namespacedName  types.NamespacedName
	labels          map[string]string
	annotations     map[string]string
	ownerReferences []metav1.OwnerReference
}

// AddToScheme should be used as soon as scheme is created to add
// core  objects for encoding/decoding
func AddToScheme(scheme *runtime.Scheme) error {
	return corev1.AddToScheme(scheme)
}

// APIsToWatch give a list of APIs to watch if using this package
// to deploy the endpoint
func APIsToWatch() ([]client.Object, error) {
	return []client.Object{&corev1.Service{}}, nil
}

// New creates a service endpoint object, deploys the resources on  the cluster
// and then checks for the health of the service. Before using the fields
// it is always recommended to check if the service is healthy.
//
// In order to generate the right RBAC, add the following lines to the Reconcile function annotations.
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
func New(ctx context.Context, c client.Client, logger logr.Logger,
	namespacedName types.NamespacedName,
	backendPort, ingressPort int32,
	svcType corev1.ServiceType,
	labels map[string]string,
	annotations map[string]string,
	ownerReferences []metav1.OwnerReference) (endpoint.Endpoint, error) {

	svcLogger := logger.WithValues("service", namespacedName)

	s := &service{
		namespacedName:  namespacedName,
		svcType:         svcType,
		labels:          labels,
		annotations:     annotations,
		ownerReferences: ownerReferences,
		backendPort:     backendPort,
		ingressPort:     ingressPort,
		logger:          svcLogger,
	}

	err := s.validate()
	if err != nil {
		s.logger.Error(err, "endpoint validation failed")
		return nil, err
	}

	err = s.reconcileService(ctx, c)
	if err != nil {
		s.logger.Error(err, "unable to reconcile service for endpoint")
		return nil, err
	}

	return s, err
}

func (s *service) NamespacedName() types.NamespacedName {
	return s.namespacedName
}

func (s *service) Hostname() string {
	return s.hostname
}

func (s *service) BackendPort() int32 {
	return s.backendPort
}

func (s *service) IngressPort() int32 {
	return s.ingressPort
}

func (s *service) IsHealthy(ctx context.Context, c client.Client) (bool, error) {
	svc := &corev1.Service{}
	err := c.Get(ctx, s.NamespacedName(), svc)
	if err != nil {
		s.logger.Error(err, "unable to get service")
		return false, err
	}

	switch s.svcType {
	case corev1.ServiceTypeLoadBalancer:
		if len(svc.Status.LoadBalancer.Ingress) > 0 {
			if svc.Status.LoadBalancer.Ingress[0].Hostname != "" {
				s.hostname = svc.Status.LoadBalancer.Ingress[0].Hostname
			}
			if svc.Status.LoadBalancer.Ingress[0].IP != "" {
				s.hostname = svc.Status.LoadBalancer.Ingress[0].IP
			}
			return true, nil
		}
	case corev1.ServiceTypeClusterIP:
		if svc.Spec.ClusterIP != "" {
			s.hostname = svc.Spec.ClusterIP
		}
		return true, nil
	case corev1.ServiceTypeNodePort:
		if svc.Spec.ClusterIP != "" {
			s.hostname = svc.Spec.ClusterIP
			if len(svc.Spec.Ports) > 0 {
				port := svc.Spec.Ports[0]
				if port.NodePort != 0 {
					s.ingressPort = port.NodePort
				}
			}
		}
		return true, nil
	default:
		return false, fmt.Errorf("unsupported service type %s", s.svcType)
	}
	s.logger.Info("endpoint is unhealthy")
	return false, nil
}

func (s *service) MarkForCleanup(ctx context.Context, c client.Client, key, value string) error {
	// mark service for deletion
	s.logger.Info("marking loadbalancer endpoint for deletion")
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.namespacedName.Name,
			Namespace: s.namespacedName.Namespace,
		},
	}
	return utils.UpdateWithLabel(ctx, c, svc, key, value)
}

func (s *service) validate() error {
	switch s.svcType {
	case corev1.ServiceTypeLoadBalancer,
		corev1.ServiceTypeNodePort,
		corev1.ServiceTypeClusterIP:
		break
	default:
		return fmt.Errorf("unsupported service type %s", s.svcType)
	}
	return nil
}

func (s *service) reconcileService(ctx context.Context, c client.Client) error {
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
		Name:      s.namespacedName.Name,
		Namespace: s.namespacedName.Namespace,
	}}

	// TODO: log the return operation from CreateOrUpdate
	_, err := controllerutil.CreateOrUpdate(ctx, c, service, func() error {
		service.Labels = s.labels
		service.OwnerReferences = s.ownerReferences

		service.Spec.Ports = []corev1.ServicePort{
			{
				Name:     s.namespacedName.Name,
				Protocol: corev1.ProtocolTCP,
				Port:     s.IngressPort(),
				TargetPort: intstr.IntOrString{
					Type:   intstr.Int,
					IntVal: s.BackendPort(),
				},
			},
		}
		service.Spec.Selector = s.labels
		if service.CreationTimestamp.IsZero() {
			service.Spec.Type = s.svcType
		}
		return nil
	})

	return err
}
