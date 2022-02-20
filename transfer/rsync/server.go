package rsync

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"text/template"

	"github.com/backube/pvc-transfer/endpoint"
	"github.com/backube/pvc-transfer/endpoint/route"
	"github.com/backube/pvc-transfer/internal/utils"
	"github.com/backube/pvc-transfer/transfer"
	"github.com/backube/pvc-transfer/transport"
	"github.com/backube/pvc-transfer/transport/stunnel"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// AddToScheme should be used as soon as scheme is created to add
// kube objects for encoding/decoding required in this package
func AddToScheme(scheme *runtime.Scheme) error {
	err := corev1.AddToScheme(scheme)
	if err != nil {
		return err
	}
	err = rbacv1.AddToScheme(scheme)
	if err != nil {
		return err
	}
	return nil
}

// APIsToWatch give a list of APIs to watch if using this package
// to deploy the endpoint
func APIsToWatch() ([]ctrlclient.Object, error) {
	return []ctrlclient.Object{
		&corev1.Secret{},
		&corev1.ConfigMap{},
		&corev1.Pod{},
		&corev1.ServiceAccount{},
		&rbacv1.RoleBinding{},
		&rbacv1.Role{},
	}, nil
}

// DefaultSCCName is the default name of the security context constraint
const DefaultSCCName = "pvc-transfer-mover"

const (
	rsyncServerConfTemplate = `syslog facility = local7
read only = no
list = yes
log file = /dev/stdout
max verbosity = 4
auth users = {{ $.Username }}
{{- if $.AllowLocalhostOnly }}
hosts allow = ::1, 127.0.0.1, localhost
{{- else }}
hosts allow = *.*.*.*, *
{{- end }}
uid = root
gid = root
{{ range $i, $pvc := .PVCList }}
[{{ $pvc.LabelSafeName }}]
    comment = archive for {{ $pvc.Claim.Namespace }}/{{ $pvc.Claim.Name }}
    path = /mnt/{{ $pvc.Claim.Namespace }}/{{ $pvc.LabelSafeName }}
    use chroot = no
    munge symlinks = no
    list = yes
    read only = false
    auth users = {{ $.Username }}
    secrets file = /etc/rsync-secret/rsyncd.secrets
{{ end }}
`
)

type rsyncConfigData struct {
	Username           string
	PVCList            transfer.PVCList
	AllowLocalhostOnly bool
}

type reconcileFunc func(ctx context.Context, c ctrlclient.Client, namespace string) error

type server struct {
	username        string
	password        string
	pvcList         transfer.PVCList
	transportServer transport.Transport
	endpoint        endpoint.Endpoint
	listenPort      int32

	nameSuffix string

	labels    map[string]string
	ownerRefs []metav1.OwnerReference
	options   transfer.PodOptions
	logger    logr.Logger

	// TODO: this is a temporary field that needs to give away once multiple
	//  namespace pvcList is supported
	namespace string
}

func (s *server) Endpoint() endpoint.Endpoint {
	return s.endpoint
}

func (s *server) Transport() transport.Transport {
	return s.transportServer
}

func (s *server) IsHealthy(ctx context.Context, c ctrlclient.Client) (bool, error) {
	return transfer.IsPodHealthy(ctx, c, ctrlclient.ObjectKey{Namespace: s.pvcList.Namespaces()[0], Name: fmt.Sprintf("rsync-server-%s", s.nameSuffix)})
}

func (s *server) Completed(ctx context.Context, c ctrlclient.Client) (bool, error) {
	return transfer.IsPodCompleted(ctx, c, ctrlclient.ObjectKey{Namespace: s.pvcList.Namespaces()[0], Name: fmt.Sprintf("rsync-server-%s", s.nameSuffix)}, "rsync")
}

// MarkForCleanup marks the provided "obj" to be deleted at the end of the
// synchronization iteration.
func (s *server) MarkForCleanup(ctx context.Context, c ctrlclient.Client, key, value string) error {
	// mark endpoint for deletion
	err := s.Endpoint().MarkForCleanup(ctx, c, key, value)
	if err != nil {
		return err
	}

	// mark transport for deletion
	err = s.Transport().MarkForCleanup(ctx, c, key, value)
	if err != nil {
		return err
	}

	// update configmap
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", rsyncConfig, s.nameSuffix),
			Namespace: s.namespace,
		},
	}
	err = utils.UpdateWithLabel(ctx, c, cm, key, value)
	if err != nil {
		return err
	}
	// update secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", rsyncSecretPrefix, s.nameSuffix),
			Namespace: s.namespace,
		},
	}
	err = utils.UpdateWithLabel(ctx, c, secret, key, value)
	if err != nil {
		return err
	}

	// update pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("rsync-server-%s", s.nameSuffix),
			Namespace: s.namespace,
		},
	}
	err = utils.UpdateWithLabel(ctx, c, pod, key, value)
	if err != nil {
		return err
	}

	// update service account
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", rsyncServiceAccount, s.nameSuffix),
			Namespace: s.namespace,
		},
	}
	err = utils.UpdateWithLabel(ctx, c, sa, key, value)
	if err != nil {
		return err
	}

	// update role
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", rsyncRole, s.nameSuffix),
			Namespace: s.namespace,
		},
	}
	err = utils.UpdateWithLabel(ctx, c, role, key, value)
	if err != nil {
		return err
	}

	// update rolebinding
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", rsyncRoleBinding, s.nameSuffix),
			Namespace: s.namespace,
		},
	}
	return utils.UpdateWithLabel(ctx, c, roleBinding, key, value)
}

func (s *server) PVCs() []*corev1.PersistentVolumeClaim {
	pvcs := []*corev1.PersistentVolumeClaim{}
	for _, pvc := range s.pvcList.PVCs() {
		pvcs = append(pvcs, pvc.Claim())
	}
	return pvcs
}

func (s *server) ListenPort() int32 {
	return s.listenPort
}

// NewServerWithStunnelRoute creates the stunnel server resources and a route before attempting
// to create the rsync server pod and its resources. This requires the callers to call stunnel.APIsToWatch()
// and route.APIsToWatch(), to get correct list of all the APIs to be watched for the reconcilers

// In order to generate the right RBAC, add the following lines to the Reconcile function annotations.
// +kubebuilder:rbac:groups=core,resources=services;secrets;configmaps;pods;serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete
func NewServerWithStunnelRoute(ctx context.Context, c ctrlclient.Client, logger logr.Logger,
	pvcList transfer.PVCList,
	labels map[string]string,
	ownerRefs []metav1.OwnerReference,
	password string, podOptions transfer.PodOptions) (transfer.Server, error) {

	var namespace string
	namespaces := pvcList.Namespaces()
	if len(namespaces) > 0 {
		namespace = pvcList.Namespaces()[0]
	}

	for _, ns := range namespaces {
		if ns != namespace {
			return nil, fmt.Errorf("PVC list provided has pvcs in different namespaces which is not supported")
		}
	}

	if namespace == "" {
		return nil, fmt.Errorf("ether PVC list is empty or namespace is not specified")
	}
	hm := transfer.NamespaceHashForNames(pvcList)
	e, err := route.New(ctx, c, logger, types.NamespacedName{
		Namespace: namespace,
		Name:      hm[namespace],
	}, route.EndpointTypePassthrough, labels, ownerRefs)
	if err != nil {
		return nil, err
	}

	t, err := stunnel.NewServer(ctx, c, logger, types.NamespacedName{Namespace: namespace, Name: hm[namespace]}, e, &transport.Options{Labels: labels, Owners: ownerRefs})
	if err != nil {
		return nil, err
	}

	return NewServer(ctx, c, logger, pvcList, t, e, labels, ownerRefs, password, podOptions)
}

// NewServer takes PVCList, transport and endpoint object and all
// the resources required by the transfer server pod as well as the transfer
// pod. All the PVCs in the list can be sync'ed via the endpoint object

// In order to generate the right RBAC, add the following lines to the Reconcile function annotations.
// +kubebuilder:rbac:groups=core,resources=secrets;configmaps;pods;serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
func NewServer(ctx context.Context, c ctrlclient.Client, logger logr.Logger,
	pvcList transfer.PVCList,
	t transport.Transport,
	e endpoint.Endpoint,
	labels map[string]string,
	ownerRefs []metav1.OwnerReference,
	password string, podOptions transfer.PodOptions) (transfer.Server, error) {

	// TODO: add proper validation for podOptions
	if podOptions.ContainerSecurityContext.RunAsUser != nil && *podOptions.ContainerSecurityContext.RunAsUser != 0 {
		return nil, fmt.Errorf("running as non-root user is not supported yet")
	}

	r := &server{
		username:        "root",
		password:        password,
		pvcList:         pvcList,
		transportServer: t,
		endpoint:        e,
		listenPort:      t.ConnectPort(),
		labels:          labels,
		ownerRefs:       ownerRefs,
		options:         podOptions,
	}

	var namespace string
	namespaces := pvcList.Namespaces()
	if len(namespaces) > 0 {
		namespace = pvcList.Namespaces()[0]
	}

	r.nameSuffix = transfer.NamespaceHashForNames(pvcList)[namespace][:10]
	r.logger = logger.WithValues("rsyncServer", r.nameSuffix)

	for _, ns := range namespaces {
		if ns != namespace {
			return nil, fmt.Errorf("PVC list provided has pvcs in different namespaces which is not supported")
		}
	}
	if namespace == "" {
		return nil, fmt.Errorf("ether PVC list is empty or namespace is not specified")
	}
	r.namespace = namespace

	reconcilers := []reconcileFunc{
		r.reconcileConfigMap,
		r.reconcileSecret,
		r.reconcileServiceAccount,
		r.reconcileRole,
		r.reconcileRoleBinding,
		r.reconcilePod,
	}

	for _, reconcile := range reconcilers {
		err := reconcile(ctx, c, r.namespace)
		if err != nil {
			r.logger.Error(err, "error reconciling rsyncServer")
			return nil, err
		}
	}

	return r, nil
}

func (s *server) reconcileConfigMap(ctx context.Context, c ctrlclient.Client, namespace string) error {
	var rsyncConf bytes.Buffer
	rsyncConfTemplate, err := template.New("config").Parse(rsyncServerConfTemplate)
	if err != nil {
		s.logger.Error(err, "unable to parse rsyncServerConfTemplate")
		return err
	}

	allowLocalhostOnly := s.Transport().Type() == stunnel.TransportTypeStunnel
	configdata := rsyncConfigData{
		Username:           s.username,
		PVCList:            s.pvcList.InNamespace(namespace),
		AllowLocalhostOnly: allowLocalhostOnly,
	}

	err = rsyncConfTemplate.Execute(&rsyncConf, configdata)
	if err != nil {
		s.logger.Error(err, "unable to execute rsyncServerConfTemplate")
		return err
	}

	rsyncConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      fmt.Sprintf("%s-%s", rsyncConfig, s.nameSuffix),
		},
	}

	_, err = ctrlutil.CreateOrUpdate(ctx, c, rsyncConfigMap, func() error {
		rsyncConfigMap.Labels = s.labels
		rsyncConfigMap.OwnerReferences = s.ownerRefs
		rsyncConfigMap.Data = map[string]string{
			"rsyncd.conf": rsyncConf.String(),
		}
		return nil
	})
	return err
}

func (s *server) reconcileSecret(ctx context.Context, c ctrlclient.Client, namespace string) error {
	if s.password == "" {
		e := fmt.Errorf("password is empty")
		s.logger.Error(e, "unable to find password for rsyncServer")
		return e
	}

	rsyncSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      fmt.Sprintf("%s-%s", rsyncSecretPrefix, s.nameSuffix),
		},
	}

	_, err := ctrlutil.CreateOrUpdate(ctx, c, rsyncSecret, func() error {
		rsyncSecret.Labels = s.labels
		rsyncSecret.OwnerReferences = s.ownerRefs
		rsyncSecret.Data = map[string][]byte{
			"credentials": []byte(s.username + ":" + s.password),
		}
		return nil
	})

	return err
}

func (s *server) reconcilePod(ctx context.Context, c ctrlclient.Client, namespace string) error {
	volumeMounts := []corev1.VolumeMount{}
	configVolumeMounts := s.getConfigVolumeMounts()
	pvcVolumeMounts := s.getPVCVolumeMounts(namespace)

	volumeMounts = append(volumeMounts, configVolumeMounts...)
	volumeMounts = append(volumeMounts, pvcVolumeMounts...)
	containers := s.getContainers(volumeMounts)

	containers = append(containers, s.Transport().Containers()...)

	mode := int32(0600)

	configVolumes := s.getConfigVolumes(mode)
	pvcVolumes := s.getPVCVolumes(namespace)

	volumes := append(pvcVolumes, configVolumes...)
	volumes = append(volumes, s.Transport().Volumes()...)

	podSpec := corev1.PodSpec{
		Containers:         containers,
		Volumes:            volumes,
		RestartPolicy:      corev1.RestartPolicyNever,
		ServiceAccountName: fmt.Sprintf("%s-%s", rsyncServiceAccount, s.nameSuffix),
	}

	applyPodOptions(&podSpec, s.options)

	server := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("rsync-server-%s", s.nameSuffix),
			Namespace: namespace,
		},
		Spec: podSpec,
	}

	_, err := ctrlutil.CreateOrUpdate(ctx, c, server, func() error {
		server.Labels = s.labels
		server.OwnerReferences = s.ownerRefs
		server.Spec = podSpec
		return nil
	})
	return err
}

func (s *server) getConfigVolumes(mode int32) []corev1.Volume {
	return []corev1.Volume{
		{
			Name: fmt.Sprintf("%s-%s", rsyncConfig, s.nameSuffix),
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: fmt.Sprintf("%s-%s", rsyncConfig, s.nameSuffix),
					},
				},
			},
		},
		{
			Name: fmt.Sprintf("%s-%s", rsyncSecretPrefix, s.nameSuffix),
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  fmt.Sprintf("%s-%s", rsyncSecretPrefix, s.nameSuffix),
					DefaultMode: &mode,
					Items: []corev1.KeyToPath{
						{
							Key:  "credentials",
							Path: "rsyncd.secrets",
						},
					},
				},
			},
		},
		{
			Name: rsyncdLogDir,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}
}

func (s *server) getPVCVolumeMounts(namespace string) []corev1.VolumeMount {
	pvcVolumeMounts := []corev1.VolumeMount{}
	for _, pvc := range s.pvcList.InNamespace(namespace).PVCs() {
		pvcVolumeMounts = append(
			pvcVolumeMounts,
			corev1.VolumeMount{
				Name:      pvc.LabelSafeName(),
				MountPath: fmt.Sprintf("/mnt/%s/%s", pvc.Claim().Namespace, pvc.LabelSafeName()),
			})
	}
	return pvcVolumeMounts
}

func (s *server) getContainers(volumeMounts []corev1.VolumeMount) []corev1.Container {
	rsyncCommandTemplate := `/usr/bin/rsync --daemon --no-detach --port=` + strconv.Itoa(int(s.ListenPort())) + ` -vvv | tee ` + rsyncdLogDirPath + `rsync.log &
while true; do
	grep "_exit_cleanup" ` + rsyncdLogDirPath + `rsync.log >> /dev/null
	if [[ $? -eq 0 ]]
	then
		exit 0; 
	fi
	sleep 1;
done`

	return []corev1.Container{
		{
			Name:  RsyncContainer,
			Image: rsyncImage,
			Command: []string{
				"/bin/bash",
				"-c",
				rsyncCommandTemplate,
			},
			Ports: []corev1.ContainerPort{
				{
					Name:          "rsyncd",
					Protocol:      corev1.ProtocolTCP,
					ContainerPort: s.ListenPort(),
				},
			},
			VolumeMounts: volumeMounts,
		},
	}
}

func (s *server) getPVCVolumes(namespace string) []corev1.Volume {
	pvcVolumes := []corev1.Volume{}
	for _, pvc := range s.pvcList.InNamespace(namespace).PVCs() {
		pvcVolumes = append(
			pvcVolumes,
			corev1.Volume{
				Name: pvc.LabelSafeName(),
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pvc.Claim().Name,
					},
				},
			},
		)
	}
	return pvcVolumes
}

func (s *server) getConfigVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      fmt.Sprintf("%s-%s", rsyncConfig, s.nameSuffix),
			MountPath: "/etc/rsyncd.conf",
			SubPath:   "rsyncd.conf",
		},
		{
			Name:      fmt.Sprintf("%s-%s", rsyncSecretPrefix, s.nameSuffix),
			MountPath: "/etc/rsync-secret",
		},
		{
			Name:      rsyncdLogDir,
			MountPath: rsyncdLogDirPath,
		},
	}
}

func (s *server) reconcileServiceAccount(ctx context.Context, c ctrlclient.Client, namespace string) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", rsyncServiceAccount, s.nameSuffix),
			Namespace: namespace,
		},
	}
	_, err := ctrlutil.CreateOrUpdate(ctx, c, sa, func() error {
		sa.Labels = s.labels
		sa.OwnerReferences = s.ownerRefs
		return nil
	})
	return err
}

func (s *server) reconcileRole(ctx context.Context, c ctrlclient.Client, namespace string) error {
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", rsyncRole, s.nameSuffix),
			Namespace: namespace,
		},
	}
	_, err := ctrlutil.CreateOrUpdate(ctx, c, role, func() error {
		role.OwnerReferences = s.ownerRefs
		role.Labels = s.labels
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{"security.openshift.io"},
				Resources: []string{"securitycontextconstraints"},
				// Must match the name of the SCC that is deployed w/ the operator
				// config/openshift/mover_scc.yaml
				ResourceNames: []string{DefaultSCCName},
				Verbs:         []string{"use"},
			},
		}
		return nil
	})
	return err
}

func (s *server) reconcileRoleBinding(ctx context.Context, c ctrlclient.Client, namespace string) error {
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", rsyncRoleBinding, s.nameSuffix),
			Namespace: namespace,
		},
	}
	_, err := ctrlutil.CreateOrUpdate(ctx, c, roleBinding, func() error {
		roleBinding.OwnerReferences = s.ownerRefs
		roleBinding.Labels = s.labels
		roleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     fmt.Sprintf("%s-%s", rsyncRole, s.nameSuffix),
		}
		roleBinding.Subjects = []rbacv1.Subject{
			{Kind: "ServiceAccount", Name: fmt.Sprintf("%s-%s", rsyncServiceAccount, s.nameSuffix)},
		}
		return nil
	})
	return err
}
