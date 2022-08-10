package stunnel

import (
	"bytes"
	"context"
	"fmt"
	"text/template"

	"github.com/backube/pvc-transfer/endpoint"
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
	// TCP_NODELAY=1 bypasses Nagle's Delay algorithm
	// this means that the tcp stack does not wait for receiving an ack
	// before sending the next packet https://en.wikipedia.org/wiki/Nagle%27s_algorithm
	// At scale setting/unsetting this option might drive different network characteristics
	stunnelServerConfTemplate = `foreground = no
pid =
socket = l:TCP_NODELAY=1
socket = r:TCP_NODELAY=1
debug = 7
sslVersion = TLSv1.3
output=/dev/stdout
{{ if .UsePSK }}
ciphers = PSK
PSKsecrets = /etc/stunnel/certs/key
{{ else }}
key = /etc/stunnel/certs/server.key
cert = /etc/stunnel/certs/server.crt
CAfile = /etc/stunnel/certs/ca.crt
verify = 2
{{ end }}

[transfer]
accept = {{ $.AcceptPort }}
connect = {{ $.ConnectPort }}
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

// NewServer creates the stunnel server object, deploys the resource on the cluster
// and then generates the necessary containers and volumes for transport to consume.
//
// Before passing the client c make sure to call AddToScheme() if core types are not already registered
// In order to generate the right RBAC, add the following lines to the Reconcile function annotations.
// +kubebuilder:rbac:groups=core,resources=configmaps;secrets,verbs=get;list;watch;create;update;patch;delete
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
	return getCredentialsSecretRef(s, s.options.Credentials)
}

func (s *server) Hostname() string {
	return "localhost"
}

func (s *server) MarkForCleanup(ctx context.Context, c ctrlclient.Client, key, value string) error {
	return markForCleanup(ctx, c, s.namespacedName, key, value, "server")
}

func (s *server) reconcileConfig(ctx context.Context, c ctrlclient.Client) error {
	stunnelConfTemplate, err := template.New("config").Parse(stunnelServerConfTemplate)
	if err != nil {
		s.logger.Error(err, "unable to parse stunnel server config template")
		return err
	}

	type confFields struct {
		AcceptPort  int32
		ConnectPort int32
		UsePSK      bool
	}
	fields := confFields{
		// acceptPort on which Stunnel service listens on, must connect with endpoint
		AcceptPort: s.ListenPort(),
		// connectPort in the container on which Transfer is listening on
		ConnectPort: s.ConnectPort(),
		UsePSK:      false,
	}
	if s.options.Credentials != nil && s.options.Credentials.Type == CredentialsTypePSK {
		fields.UsePSK = true
	}
	var stunnelConf bytes.Buffer
	err = stunnelConfTemplate.Execute(&stunnelConf, fields)
	if err != nil {
		s.logger.Error(err, "unable to execute stunnel server config template")
		return err
	}

	stunnelConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getResourceName(s.namespacedName, "server", stunnelConfig),
			Namespace: s.NamespacedName().Namespace,
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

func (s *server) reconcileSecret(ctx context.Context, c ctrlclient.Client) error {
	return reconcileCredentialSecret(ctx, c, s.logger, s, s.options)
}

func (s *server) serverContainers() []corev1.Container {
	stunnelScript := `/bin/stunnel /etc/stunnel/stunnel.conf
	# terminate the transport when transfer isn't available
	RETRY=0
	while true; do
		nc -z localhost %d
		rc=$?
		if [ $rc -ne 0 ]; then
			RETRY=$((RETRY+1))
		else
			RETRY=0
		fi
		if [ $RETRY -gt 10 ]; then
			exit 0
		else
			sleep 1
		fi
	done
	`
	stunnelScript = fmt.Sprintf(stunnelScript, s.ConnectPort())
	return []corev1.Container{
		{
			Name:  Container,
			Image: getImage(s.options),
			Command: []string{
				"/bin/bash",
				"-c",
				stunnelScript,
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
					Name:      getResourceName(s.namespacedName, "server", stunnelConfig),
					MountPath: "/etc/stunnel/stunnel.conf",
					SubPath:   "stunnel.conf",
				},
				{
					Name:      getResourceName(s.namespacedName, "certs", stunnelSecret),
					MountPath: "/etc/stunnel/certs",
				},
			},
		},
	}
}

func (s *server) serverVolumes() []corev1.Volume {
	return []corev1.Volume{
		{
			Name: getResourceName(s.namespacedName, "server", stunnelConfig),
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: getResourceName(s.namespacedName, "server", stunnelConfig),
					},
				},
			},
		},
		{
			Name:         getResourceName(s.namespacedName, "certs", stunnelSecret),
			VolumeSource: getCredentialsVolumeSource(s, s.options.Credentials, "server"),
		},
	}
}
