package stunnel

import (
	"bytes"
	"context"
	"text/template"

	"github.com/backube/pvc-transfer/transport"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const clientListenPort = 6443

const (
	stunnelClientConfTemplate = `
pid =
sslVersion = TLSv1.3
client = yes
syslog = no
output = /dev/stdout
{{ if .UseTLS }}
key = /etc/stunnel/certs/client.key
cert = /etc/stunnel/certs/client.crt
CAfile = /etc/stunnel/certs/ca.crt
verify = 2
{{ else }}
ciphers = PSK
PSKsecrets = /etc/stunnel/certs/key
{{ end }}

[transfer]
debug = 7
accept = {{ .ListenPort }}
{{- if not (eq .ProxyHost "") }}
protocol = connect
connect = {{ .ProxyHost }}
protocolHost = {{ .Hostname }}:{{ .ListenPort }}
{{- if not (eq .ProxyUsername "") }}
protocolUsername = {{ .ProxyUsername }}
{{- end }}
{{- if not (eq .ProxyPassword "") }}
protocolPassword = {{ .ProxyPassword }}
{{- end }}
{{- else }}
connect = {{ .Hostname }}:{{ .ConnectPort }}
{{- end }}
`
)

type client struct {
	logger         logr.Logger
	connectPort    int32
	listenPort     int32
	containers     []corev1.Container
	volumes        []corev1.Volume
	options        *transport.Options
	serverHostname string
	namespacedName types.NamespacedName
}

func (sc *client) MarkForCleanup(ctx context.Context, c ctrlclient.Client, key, value string) error {
	return markForCleanup(ctx, c, sc.namespacedName, key, value, "client")
}

func (sc *client) NamespacedName() types.NamespacedName {
	return sc.namespacedName
}

func (sc *client) ConnectPort() int32 {
	return sc.connectPort
}

func (sc *client) ListenPort() int32 {
	return sc.listenPort
}

func (sc *client) Containers() []corev1.Container {
	return sc.containers
}

func (sc *client) Volumes() []corev1.Volume {
	return sc.volumes
}

func (sc *client) Options() *transport.Options {
	return sc.options
}

func (sc *client) Type() transport.Type {
	return TransportTypeStunnel
}

func (sc *client) Credentials() types.NamespacedName {
	return getCredentialsSecretRef(sc, sc.options.Credentials)
}

func (sc *client) Hostname() string {
	return "localhost"
}

// NewClient creates the stunnel client object, deploys the resource on the cluster
// and then generates the necessary containers and volumes for transport to consume.
//
// Before passing the client c make sure to call AddToScheme() if core types are not already registered
// In order to generate the right RBAC, add the following lines to the Reconcile function annotations.
// +kubebuilder:rbac:groups=core,resources=configmaps,secrets,verbs=get;list;watch;create;update;patch;delete
func NewClient(ctx context.Context, c ctrlclient.Client, logger logr.Logger,
	namespacedName types.NamespacedName,
	hostname string,
	connectPort int32,
	options *transport.Options) (transport.Transport, error) {
	clientLogger := logger.WithValues("stunnelClient", namespacedName)
	tc := &client{
		logger:         clientLogger,
		namespacedName: namespacedName,
		options:        options,
		connectPort:    connectPort,
		serverHostname: hostname,
		listenPort:     clientListenPort,
	}

	err := tc.reconcileConfig(ctx, c)
	if err != nil {
		return nil, err
	}

	err = tc.reconcileSecret(ctx, c)
	if err != nil {
		return nil, err
	}

	tc.containers = tc.clientContainers(tc.ListenPort())
	tc.volumes = tc.clientVolumes()

	return tc, nil
}

func (sc *client) reconcileConfig(ctx context.Context, c ctrlclient.Client) error {
	stunnelConfTemplate, err := template.New("config").Parse(stunnelClientConfTemplate)
	if err != nil {
		sc.logger.Error(err, "unable to parse stunnel client config template")
		return err
	}

	type confFields struct {
		ListenPort    int32
		ConnectPort   int32
		Hostname      string
		ProxyHost     string
		ProxyUsername string
		ProxyPassword string
		UseTLS        bool
	}

	fields := confFields{
		ListenPort:    sc.ListenPort(),
		Hostname:      sc.serverHostname,
		ConnectPort:   sc.ConnectPort(),
		ProxyHost:     sc.Options().ProxyURL,
		ProxyUsername: sc.Options().ProxyUsername,
		ProxyPassword: sc.Options().ProxyPassword,
		UseTLS:        true,
	}
	if sc.options.Credentials != nil && sc.options.Credentials.Type == CredentialsTypePSK {
		fields.UseTLS = false
	}
	var stunnelConf bytes.Buffer
	err = stunnelConfTemplate.Execute(&stunnelConf, fields)
	if err != nil {
		sc.logger.Error(err, "unable to execute stunnel client config template")
		return err
	}

	stunnelConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: sc.NamespacedName().Namespace,
			Name:      getResourceName(sc.namespacedName, "client", stunnelConfig),
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, c, stunnelConfigMap, func() error {
		stunnelConfigMap.Labels = sc.options.Labels
		stunnelConfigMap.OwnerReferences = sc.options.Owners

		stunnelConfigMap.Data = map[string]string{
			"stunnel.conf": stunnelConf.String(),
		}
		return err
	})
	return err
}

func (sc *client) reconcileSecret(ctx context.Context, c ctrlclient.Client) error {
	return reconcileCredentialSecret(ctx, c, sc.logger, sc, sc.options)
}

func (sc *client) clientContainers(listenPort int32) []corev1.Container {
	return []corev1.Container{
		{
			Name:  Container,
			Image: getImage(sc.options),
			Command: []string{
				"/bin/stunnel",
				"/etc/stunnel/stunnel.conf",
			},
			Ports: []corev1.ContainerPort{
				{
					Name:          "stunnel",
					Protocol:      corev1.ProtocolTCP,
					ContainerPort: listenPort,
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      getResourceName(sc.namespacedName, "client", stunnelConfig),
					MountPath: "/etc/stunnel/stunnel.conf",
					SubPath:   "stunnel.conf",
				},
				{
					Name:      getResourceName(sc.namespacedName, "certs", stunnelSecret),
					MountPath: "/etc/stunnel/certs",
				},
			},
		},
	}
}

func (sc *client) clientVolumes() []corev1.Volume {
	return []corev1.Volume{
		{
			Name: getResourceName(sc.namespacedName, "client", stunnelConfig),
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: getResourceName(sc.namespacedName, "client", stunnelConfig),
					},
				},
			},
		},
		{
			Name:         getResourceName(sc.namespacedName, "certs", stunnelSecret),
			VolumeSource: getCredentialsVolumeSource(sc, sc.options.Credentials, "client"),
		},
	}
}
