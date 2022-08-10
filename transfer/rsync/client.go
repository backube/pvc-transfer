package rsync

import (
	"context"
	"fmt"
	"strings"

	"github.com/backube/pvc-transfer/endpoint"
	"github.com/backube/pvc-transfer/internal/utils"
	"github.com/backube/pvc-transfer/transfer"
	"github.com/backube/pvc-transfer/transport"
	"github.com/backube/pvc-transfer/transport/stunnel"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	errorsutil "k8s.io/apimachinery/pkg/util/errors"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type client struct {
	username        string
	pvcList         transfer.PVCList
	transportClient transport.Transport
	endpoint        endpoint.Endpoint

	nameSuffix string

	labels    map[string]string
	ownerRefs []metav1.OwnerReference
	options   transfer.PodOptions
	logger    logr.Logger

	// TODO: this is a temporary field that needs to give away once multiple
	//  namespace pvcList is supported
	namespace string
}

func (tc *client) Transport() transport.Transport {
	return tc.transportClient
}

func (tc *client) PVCs() []*corev1.PersistentVolumeClaim {
	pvcs := []*corev1.PersistentVolumeClaim{}
	for _, pvc := range tc.pvcList.PVCs() {
		pvcs = append(pvcs, pvc.Claim())
	}
	return pvcs
}

func (tc *client) Status(ctx context.Context, c ctrlclient.Client) (*transfer.Status, error) {
	podList := &corev1.PodList{}
	err := c.List(ctx, podList, ctrlclient.MatchingLabels(tc.labels))
	if err != nil {
		return nil, err
	}

	for _, pod := range podList.Items {
		if len(pod.Status.ContainerStatuses) > 0 {
			for _, containerStatus := range pod.Status.ContainerStatuses {
				if containerStatus.Name == "rsync" && containerStatus.State.Terminated != nil {
					if containerStatus.State.Terminated.ExitCode == 0 {
						return &transfer.Status{
							Completed: &transfer.Completed{
								Successful: true,
								Failure:    false,
								FinishedAt: &containerStatus.State.Terminated.FinishedAt,
							},
						}, nil
					} else {
						return &transfer.Status{
							Running: nil,
							Completed: &transfer.Completed{
								Successful: false,
								Failure:    true,
								FinishedAt: &containerStatus.State.Terminated.FinishedAt,
							},
						}, nil
					}
				}
			}
		}
	}
	return nil, fmt.Errorf("unable to find the appropriate container to inspect status for rsync transfer")
}

func (tc *client) MarkForCleanup(ctx context.Context, c ctrlclient.Client, key, value string) error {
	err := tc.Transport().MarkForCleanup(ctx, c, key, value)
	if err != nil {
		return err
	}

	err = tc.endpoint.MarkForCleanup(ctx, c, key, value)
	if err != nil {
		return err
	}

	// update pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("rsync-client-%s", tc.nameSuffix),
			Namespace: tc.namespace,
		},
	}
	err = utils.UpdateWithLabel(ctx, c, pod, key, value)
	if err != nil {
		return err
	}

	// update service account
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", rsyncServiceAccount, tc.nameSuffix),
			Namespace: tc.namespace,
		},
	}
	err = utils.UpdateWithLabel(ctx, c, sa, key, value)
	if err != nil {
		return err
	}

	// update role
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", rsyncRole, tc.nameSuffix),
			Namespace: tc.namespace,
		},
	}
	err = utils.UpdateWithLabel(ctx, c, role, key, value)
	if err != nil {
		return err
	}

	// update rolebinding
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", rsyncRoleBinding, tc.nameSuffix),
			Namespace: tc.namespace,
		},
	}

	return utils.UpdateWithLabel(ctx, c, roleBinding, key, value)
}

// NewClient takes PVCList, transport and endpoint object and creates all
// the resources required by the transfer client pod as well as the transfer
// pod. All the PVCs in the list will have rsync running against the server
// to sync its data.

// The nameSuffix will be appended to the rsync client resources (pod, sa, role and rolebinding)
// hence it needs to adhere to the naming convention of kube resources. This allows for consumers
// to retry with a different suffix until retries are added to the client package

// In order to generate the right RBAC, add the following lines to the Reconcile function annotations.
// +kubebuilder:rbac:groups=core,resources=pods;serviceaccounts;secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
func NewClient(ctx context.Context, c ctrlclient.Client,
	pvcList transfer.PVCList,
	t transport.Transport,
	logger logr.Logger,
	nameSuffix string,
	labels map[string]string,
	ownerRefs []metav1.OwnerReference,
	podOptions transfer.PodOptions) (transfer.Client, error) {
	tc := &client{
		username:        "root",
		pvcList:         pvcList,
		transportClient: t,
		nameSuffix:      nameSuffix,
		labels:          labels,
		ownerRefs:       ownerRefs,
		options:         podOptions,
		logger:          logger,
	}

	var namespace string
	namespaces := pvcList.Namespaces()
	if len(namespaces) > 0 {
		namespace = namespaces[0]
	}

	for _, ns := range namespaces {
		if ns != namespace {
			return nil, fmt.Errorf("PVC list provided has pvcs in different namespaces which is not supported")
		}
	}

	if namespace == "" {
		return nil, fmt.Errorf("ether PVC list is empty or namespace is not specified")
	}
	tc.namespace = namespace

	tc.nameSuffix = transfer.NamespaceHashForNames(pvcList)[namespace][:10]
	reconcilers := []reconcileFunc{
		tc.reconcilePod,
	}

	for _, reconcile := range reconcilers {
		err := reconcile(ctx, c, tc.namespace)
		if err != nil {
			tc.logger.Error(err, "error reconciling rsyncServer")
			return nil, err
		}
	}

	return tc, nil
}

// TODO: add retries
func (tc *client) reconcilePod(ctx context.Context, c ctrlclient.Client, ns string) error {
	var errs []error

	rsyncOptions, err := rsyncDefaultOptions()
	if err != nil {
		tc.logger.Error(err, "unable to get default options for rsync command")
		return err
	}
	if tc.options.CommandOptions != nil {
		rsyncOptions, err = tc.options.CommandOptions.Options()
		if err != nil {
			tc.logger.Error(err, "unable to apply custom options for rsync command")
			return err
		}
	}

	for _, pvc := range tc.pvcList.InNamespace(ns).PVCs() {
		// create Rsync command for PVC
		rsyncContainerCommand := tc.getCommand(rsyncOptions, pvc)

		volumeMounts := []corev1.VolumeMount{
			{
				Name:      "mnt",
				MountPath: fmt.Sprintf("/mnt/%s/%s", pvc.Claim().Namespace, pvc.LabelSafeName()),
			},
			{
				Name:      "rsync-communication",
				MountPath: rsyncCommunicationMountPath,
			},
		}
		volumeMounts = append(volumeMounts, getTerminationVolumeMounts()...)
		// create rsync container
		containers := []corev1.Container{
			{
				Name:         RsyncContainer,
				Command:      rsyncContainerCommand,
				VolumeMounts: volumeMounts,
			},
		}
		// attach transport containers
		err := customizeTransportClientContainers(tc.Transport())
		if err != nil {
			tc.logger.Error(err, "unable to customize Transport client containers for rsync client pod")
			return err
		}
		containers = append(containers, tc.Transport().Containers()...)

		volumes := []corev1.Volume{
			{
				Name: "mnt",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pvc.Claim().Name,
					},
				},
			},
			{
				Name: "rsync-communication",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory},
				},
			},
		}
		volumes = append(volumes, tc.Transport().Volumes()...)
		volumes = append(volumes, getTerminationVolumes()...)

		podSpec := corev1.PodSpec{
			Containers:         containers,
			Volumes:            volumes,
			RestartPolicy:      corev1.RestartPolicyNever,
			ServiceAccountName: tc.options.ServiceAccountName,
		}

		applyPodOptions(&podSpec, tc.options)

		pod := corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("rsync-client-%s", tc.nameSuffix),
				Namespace: pvc.Claim().Namespace,
			},
		}

		_, err = ctrlutil.CreateOrUpdate(ctx, c, &pod, func() error {
			pod.Labels = tc.labels
			// adding pvc name in annotation to avoid constraints on labels in naming
			pod.Annotations = map[string]string{"pvc": pvc.Claim().Name}
			pod.OwnerReferences = tc.ownerRefs
			if pod.CreationTimestamp.IsZero() {
				pod.Spec = podSpec
			}
			return nil
		})
		errs = append(errs, err)
	}

	aggregateErr := errorsutil.NewAggregate(errs)
	if aggregateErr != nil {
		tc.logger.Error(aggregateErr, "errors in creating pods for pvcList, please try again")
	}

	return nil
}

func (tc *client) getCommand(rsyncOptions []string, pvc transfer.PVC) []string {
	// TODO: add a stub for null transport
	rsyncCommand := []string{"/usr/bin/rsync"}
	rsyncCommand = append(rsyncCommand, rsyncOptions...)
	rsyncCommand = append(rsyncCommand, fmt.Sprintf("/mnt/%s/%s/", pvc.Claim().Namespace, pvc.LabelSafeName()))
	rsyncCommand = append(rsyncCommand,
		fmt.Sprintf("rsync://%s@%s/%s/ --port %d",
			tc.username,
			tc.Transport().Hostname(),
			pvc.LabelSafeName(), tc.Transport().ListenPort()))
	rsyncTerminationCommand := fmt.Sprintf(
		"/usr/bin/rsync /mnt/termination/done rsync://%s@%s/termination/ --port %d",
		tc.username,
		tc.Transport().Hostname(),
		tc.Transport().ListenPort())
	rsyncCommandBashScript := fmt.Sprintf(`trap "touch %s/rsync-client-container-done" EXIT SIGINT SIGTERM;
timeout=120;
SECONDS=0;
START_TIME=$SECONDS
touch /mnt/termination/done
while [ $SECONDS -lt $timeout ]
do
	nc -z localhost %d
	rc=$?
	if [ $rc -eq 0 ]
	then 
		MAX_RETRIES=5
		RETRY=0
		DELAY=2
		FACTOR=2
		rc=1
		while [[ ${rc} -ne 0 && ${RETRY} -lt ${MAX_RETRIES} ]]
		do 
			RETRY=$((RETRY+1))
			%s
			rc=$?
			if [[ ${rc} -ne 0 ]]; then
				echo "Synchronization failed. Retrying in ${DELAY} seconds. Retry ${RETRY}/${MAX_RETRIES}."
				if [[ ${RETRY} -lt ${MAX_RETRIES} ]]; then
					sleep ${DELAY}
					DELAY=$((DELAY * FACTOR ))
				fi
			fi
		done 
		break
	fi
done
echo "Rsync completed in $(( SECONDS - START_TIME ))s"
sync
if [[ $rc -eq 0 ]]; then
    echo "Synchronization completed successfully. Notifying destination..."
    %s
else
    echo "Synchronization failed. rsync returned: $rc"
    exit $rc
fi
`,
		rsyncCommunicationMountPath,
		tc.Transport().ListenPort(),
		strings.Join(rsyncCommand, " "),
		rsyncTerminationCommand)
	rsyncContainerCommand := []string{
		"/bin/bash",
		"-c",
		rsyncCommandBashScript,
	}
	return rsyncContainerCommand
}

// customizeTransportClientContainers customizes transport's client containers for specific rsync communication
func customizeTransportClientContainers(transportClient transport.Transport) error {
	switch transportClient.Type() {
	case stunnel.TransportTypeStunnel:
		var stunnelContainer *corev1.Container
		for i := range transportClient.Containers() {
			c := &transportClient.Containers()[i]
			if c.Name == stunnel.Container {
				stunnelContainer = c
			}
		}
		if stunnelContainer == nil {
			return fmt.Errorf("couldnt find container named %s in rsync client pod", stunnel.Container)
		}
		stunnelContainer.Command = []string{
			"/bin/bash",
			"-c",
			fmt.Sprintf(`/bin/stunnel /etc/stunnel/stunnel.conf
while true
do test -f %s/rsync-client-container-done
if [ $? -eq 0 ]
then
break
fi
done
exit 0`, rsyncCommunicationMountPath),
		}
		stunnelContainer.VolumeMounts = append(
			stunnelContainer.VolumeMounts,
			corev1.VolumeMount{
				Name:      "rsync-communication",
				MountPath: rsyncCommunicationMountPath,
			})
	}
	return nil
}
