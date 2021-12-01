package stunnel

import (
	"bytes"
	"context"
	"strconv"
	"text/template"

	"github.com/backube/pvc-transfer/transport"
	"github.com/backube/pvc-transfer/transport/tls/certs"
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
 [transfer]
 debug = 7
 accept = {{ .listenPort }}
 cert = /etc/stunnel/certs/tls.crt
 key = /etc/stunnel/certs/tls.key
 CAfile = /etc/stunnel/certs/ca.crt
 verify = 2
{{- if not (eq .proxyHost "") }}
 protocol = connect
 connect = {{ .proxyHost }}
 protocolHost = {{ .hostname }}:{{ .listenPort }}
{{- if not (eq .proxyUsername "") }}
 protocolUsername = {{ .proxyUsername }}
{{- end }}
{{- if not (eq .proxyPassword "") }}
 protocolPassword = {{ .proxyPassword }}
{{- end }}
{{- else }}
 connect = {{ .hostname }}:{{ .connectPort }}
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
	return types.NamespacedName{
		Namespace: sc.namespacedName.Namespace,
		Name:      stunnelSecret,
	}
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

	connections := map[string]string{
		"listenPort":    strconv.Itoa(int(sc.ListenPort())),
		"hostname":      sc.serverHostname,
		"connectPort":   strconv.Itoa(int(sc.ConnectPort())),
		"proxyHost":     sc.Options().ProxyURL,
		"proxyUsername": sc.Options().ProxyUsername,
		"proxyPassword": sc.Options().ProxyPassword,
	}
	var stunnelConf bytes.Buffer
	err = stunnelConfTemplate.Execute(&stunnelConf, connections)
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
	valid, err := isSecretValid(ctx, c, sc.logger, sc.namespacedName, "client")
	if err != nil {
		sc.logger.Error(err, "error getting existing ssl certs from secret")
		return err
	}
	if valid {
		return nil
	}

	crtBundle, err := certs.New()
	if err != nil {
		sc.logger.Error(err, "error generating ssl certificate bundle for stunnel client")
		return err
	}

	return reconcileCertificateSecrets(ctx, c, sc.namespacedName, sc.options, crtBundle)
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
					Name:      getResourceName(sc.namespacedName, "client", stunnelSecret),
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
			Name: getResourceName(sc.namespacedName, "client", stunnelSecret),
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: getResourceName(sc.namespacedName, "client", stunnelSecret),
					Items: []corev1.KeyToPath{
						{
							Key:  "tls.crt",
							Path: "tls.crt",
						},
						{
							Key:  "tls.key",
							Path: "tls.key",
						},
						{
							Key:  "ca.crt",
							Path: "ca.crt",
						},
					},
				},
			},
		},
	}
}
