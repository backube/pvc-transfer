package transport

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Transport exposes the methods required for transfers to add
// a tunneling mechanism for the traffic sent over the network.
type Transport interface {
	// NamespacedName returns the namespaced name to identify this transport Transport
	NamespacedName() types.NamespacedName
	// ListenPort returns a port on which the transport server listens for incomming connections
	ListenPort() int32
	// ConnectPort returns the port to connect to transfer server
	// Using this the server acts as a client to the transfer relaying all the data
	// sent from the transport client
	ConnectPort() int32
	// Containers returns a list of containers transfers can add to their server Pods
	Containers() []corev1.Container
	// Volumes returns a list of volumes transfers have add to their server Pods for getting the configurations
	// mounted for the transport containers to work
	Volumes() []corev1.Volume
	// Type
	Type() Type
	// Credentials returns the namespaced name of the secret holding credentials for talking to the server
	Credentials() types.NamespacedName
	// Hostname returns the string to which the transfer will connect to
	// in case of a null transport, it will simple relay the endpoint hostname
	// in case of a valid transport, it will have a custom hostname where transfers will have to connect to.
	Hostname() string
	// MarkForCleanup adds a label to all the resources created for the endpoint
	// Callers are expected to not overwrite
	MarkForCleanup(ctx context.Context, c client.Client, key, value string) error
}

// Options allows users of the transport to configure certain field
type Options struct {
	// Labels will be applied to objects reconciled by the transport
	Labels map[string]string
	// Owners will be applied to all objects reconciled by the transport
	Owners []metav1.OwnerReference
	// Image allows for specifying the image used for running the transport containers
	Image string
	// Credentials allows specifying pre-existing transport credentials
	*Credentials

	// ProxyURL is used if the cluster is behind a proxy
	ProxyURL string
	// ProxyUsername username for connecting to the proxy
	ProxyUsername string
	// ProxyPassword password for connecting to the proxy
	ProxyPassword string
}

// Credentials are used by transports to encrypt data
type Credentials struct {
	// SecretRef ref to the secret holding credentials data
	SecretRef types.NamespacedName
	// Type type of credentials used
	Type CredentialsType
}

type CredentialsType string

type Type string
