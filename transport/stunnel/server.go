package stunnel

import (
	"bytes"
	"context"
	"strconv"
	"text/template"

	"github.com/backube/pvc-transfer/endpoint"
	"github.com/backube/pvc-transfer/internal/utils"
	"github.com/backube/pvc-transfer/transport"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	stunnelServerConfTemplate = `foreground = yes
pid =
socket = l:TCP_NODELAY=1
socket = r:TCP_NODELAY=1
debug = 7
sslVersion = TLSv1.2
[rsync]
accept = {{ $.acceptPort }}
connect = {{ $.connectPort }}
key = /etc/stunnel/certs/tls.key
cert = /etc/stunnel/certs/tls.crt
TIMEOUTclose = 0
`
	stunnelConnectPort = 8080
)

// AddToScheme should be used as soon as scheme is created to add
// core  objects for encoding/decoding
func AddToScheme(scheme *runtime.Scheme) error {
	return corev1.AddToScheme(scheme)
}

// APIsToWatch give a list of APIs to watch if using this package
// to deploy the transport
func APIsToWatch() ([]ctrlclient.Object, error) {
	return []ctrlclient.Object{&corev1.Secret{}, &corev1.ConfigMap{}}, nil
}

type server struct {
	logger         logr.Logger
	listenPort     int32
	connectPort    int32
	containers     []corev1.Container
	volumes        []corev1.Volume
	options        *transport.Options
	namespacedName types.NamespacedName
}

func NewServer(ctx context.Context, c ctrlclient.Client, logger logr.Logger,
	namespacedName types.NamespacedName,
	e endpoint.Endpoint,
	options *transport.Options) (transport.Transport, error) {
	transportLogger := logger.WithValues("transportServer", namespacedName)
	transferPort := e.BackendPort()

	s := &server{
		namespacedName: namespacedName,
		options:        options,
		listenPort:     transferPort,
		connectPort:    stunnelConnectPort,
		logger:         transportLogger,
	}

	err := s.reconcileConfig(ctx, c)
	if err != nil {
		s.logger.Error(err, "unable to reconcile stunnel server config")
		return nil, err
	}

	err = s.reconcileSecret(ctx, c)
	if err != nil {
		s.logger.Error(err, "unable to reconcile stunnel server secret")
		return nil, err
	}

	s.volumes = s.serverVolumes()
	s.containers = s.serverContainers()

	return s, nil
}

func (s *server) NamespacedName() types.NamespacedName {
	return s.namespacedName
}

func (s *server) ListenPort() int32 {
	return s.listenPort
}

func (s *server) ConnectPort() int32 {
	return s.connectPort
}

func (s *server) Containers() []corev1.Container {
	return s.containers
}

func (s *server) Volumes() []corev1.Volume {
	return s.volumes
}

func (s *server) Type() transport.Type {
	return TransportTypeStunnel
}

func (s *server) Credentials() types.NamespacedName {
	return types.NamespacedName{Name: s.prefixedName(stunnelSecret), Namespace: s.NamespacedName().Namespace}
}

func (s *server) Hostname() string {
	return "localhost"
}

func (s *server) MarkForCleanup(ctx context.Context, c ctrlclient.Client, key, value string) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.prefixedName(stunnelConfig),
			Namespace: s.NamespacedName().Namespace,
		},
	}
	err := utils.UpdateWithLabel(ctx, c, cm, key, value)
	if err != nil {
		return err
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.prefixedName(stunnelSecret),
			Namespace: s.NamespacedName().Namespace,
		},
	}
	return utils.UpdateWithLabel(context.TODO(), c, secret, key, value)
}

func (s *server) reconcileConfig(ctx context.Context, c ctrlclient.Client) error {
	stunnelConfTemplate, err := template.New("config").Parse(stunnelServerConfTemplate)
	if err != nil {
		s.logger.Error(err, "unable to parse stunnel server config template")
		return err
	}

	ports := map[string]string{
		// acceptPort on which Stunnel service listens on, must connect with endpoint
		"acceptPort": strconv.Itoa(int(s.ListenPort())),
		// connectPort in the container on which Transfer is listening on
		"connectPort": strconv.Itoa(int(s.ConnectPort())),
	}
	var stunnelConf bytes.Buffer
	err = stunnelConfTemplate.Execute(&stunnelConf, ports)
	if err != nil {
		s.logger.Error(err, "unable to execute stunnel server config template")
		return err
	}

	stunnelConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: s.NamespacedName().Namespace,
			Name:      s.prefixedName(stunnelConfig),
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, c, stunnelConfigMap, func() error {
		stunnelConfigMap.Labels = s.options.Labels
		stunnelConfigMap.OwnerReferences = s.options.Owners

		stunnelConfigMap.Data = map[string]string{
			"stunnel.conf": stunnelConf.String(),
		}
		return nil
	})
	return err
}

func (s *server) prefixedName(name string) string {
	return s.namespacedName.Name + "-server-" + name
}

func (s *server) reconcileSecret(ctx context.Context, c ctrlclient.Client) error {
	_, _, found, err := getExistingCert(ctx, c, s.logger, s.namespacedName, s.secretNameSuffix())
	if found {
		return nil
	}

	if err != nil {
		s.logger.Error(err, "error getting existing ssl certs from secret")
		return err
	}

	_, newCrt, newKey, err := transport.GenerateSSLCert()
	if err != nil {
		s.logger.Error(err, "error generating ssl certs for stunnel server")
		return err
	}

	stunnelSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: s.NamespacedName().Namespace,
			Name:      getResourceName(s.namespacedName, s.secretNameSuffix()),
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, c, stunnelSecret, func() error {
		stunnelSecret.Labels = s.options.Labels
		stunnelSecret.OwnerReferences = s.options.Owners

		stunnelSecret.Data = map[string][]byte{
			"tls.crt": newCrt.Bytes(),
			"tls.key": newKey.Bytes(),
		}
		return nil
	})
	return err
}

func (s *server) secretNameSuffix() string {
	return "server-" + stunnelSecret
}

func (s *server) serverContainers() []corev1.Container {
	return []corev1.Container{
		{
			Name:  Container,
			Image: getImage(s.options),
			Command: []string{
				"/bin/stunnel",
				"/etc/stunnel/stunnel.conf",
			},
			Ports: []corev1.ContainerPort{
				{
					Name:          "stunnel",
					Protocol:      corev1.ProtocolTCP,
					ContainerPort: s.ListenPort(),
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      s.prefixedName(stunnelConfig),
					MountPath: "/etc/stunnel/stunnel.conf",
					SubPath:   "stunnel.conf",
				},
				{
					Name:      getResourceName(s.namespacedName, s.secretNameSuffix()),
					MountPath: "/etc/stunnel/certs",
				},
			},
		},
	}
}

func (s *server) serverVolumes() []corev1.Volume {
	return []corev1.Volume{
		{
			Name: s.prefixedName(stunnelConfig),
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: s.prefixedName(stunnelConfig),
					},
				},
			},
		},
		{
			Name: getResourceName(s.namespacedName, s.secretNameSuffix()),
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: getResourceName(s.namespacedName, s.secretNameSuffix()),
					Items: []corev1.KeyToPath{
						{
							Key:  "tls.crt",
							Path: "tls.crt",
						},
						{
							Key:  "tls.key",
							Path: "tls.key",
						},
					},
				},
			},
		},
	}
}
