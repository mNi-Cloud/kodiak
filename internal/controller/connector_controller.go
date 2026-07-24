/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	pb "github.com/jsiebens/ionscale/pkg/gen/ionscale/v1"
	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
	controlclient "github.com/mNi-Cloud/kodiak/internal/client"
	tsworkload "github.com/mNi-Cloud/kodiak/internal/tailscale/workload"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"tailscale.com/kube/kubetypes"
)

const (
	stateSecretAuthKey                      = "authkey"
	stateSecretReissueAuthKey               = "reissue_authkey"
	stateSecretBootstrapAuthKeyIDAnnotation = "kodiak.mnicloud.jp/bootstrap-auth-key-id"
	stateSecretBootstrapExpiryAnnotation    = "kodiak.mnicloud.jp/bootstrap-auth-key-expires-at"

	reasonConnectorReady         = "ConnectorReady"
	reasonWorkloadReady          = "StatefulSetReady"
	reasonWorkloadNotReady       = "StatefulSetNotReady"
	reasonIdentityReady          = "IdentityReady"
	reasonIdentityNotReady       = "IdentityNotReady"
	reasonControlPlaneReady      = "ControlPlaneReady"
	reasonControlPlaneNotReady   = "ControlPlaneNotReady"
	reasonExternalControlPlane   = "ExternalControlPlaneUnverified"
	reasonRoutesReady            = "RoutesReady"
	reasonRoutesNotReady         = "RoutesNotApproved"
	reasonExternalRoutes         = "ExternalRoutesUnverified"
	reasonConnectorConfiguration = "ConfigurationMissing"
	reasonConnectorError         = "ConnectorError"

	connectorRequeue       = 30 * time.Second
	connectorRetryRequeue  = 10 * time.Second
	bootstrapKeyExpiry     = 10 * time.Minute
	defaultExternalKeyName = "TS_AUTH_KEY"

	connectorTailnetIndex       = "spec.tailnetRef.name"
	connectorAuthKeySecretIndex = "spec.authKeySecretRef.name"
)

// ConnectorReconciler manages stable subnet-router workloads.
type ConnectorReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	ClientFactory    controlclient.ClientFactory
	IonscaleEndpoint string
	IonscaleAdminKey string
	IonscaleSkipTLS  bool
	TailscaleImage   string
}

type connectorControlPlane struct {
	managed   bool
	tailnetID uint64
	loginURL  string
	client    controlclient.ControlServerClientInterface
	authKey   string
}

func (r *ConnectorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("connector", req.NamespacedName)

	var resource kodiakv1alpha1.Connector
	if err := r.Get(ctx, req.NamespacedName, &resource); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !resource.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &resource)
	}
	if result, err := ensureFinalizer(ctx, r.Client, &resource); err != nil || result.Requeue {
		return result, err
	}

	controlPlane, result, err := r.resolveControlPlane(ctx, &resource)
	if err != nil {
		return r.connectorFail(ctx, &resource, err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		return result, nil
	}

	replicas := desiredReplicas(&resource)
	if err := r.reconcileStateSecrets(ctx, &resource, controlPlane, replicas); err != nil {
		return r.connectorFail(ctx, &resource, err)
	}

	if err := r.reconcileWorkloadResources(ctx, &resource, controlPlane.loginURL, replicas); err != nil {
		return r.connectorFail(ctx, &resource, err)
	}
	cleanupPending, err := r.cleanupScaledDownReplicas(ctx, &resource, controlPlane, replicas)
	if err != nil {
		return r.connectorFail(ctx, &resource, err)
	}
	if cleanupPending {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	status, conditions, err := r.observe(ctx, &resource, controlPlane, replicas)
	if err != nil {
		return r.connectorFail(ctx, &resource, err)
	}
	if err := r.updateConnectorStatus(ctx, &resource, status, conditions); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("connector reconciled", "replicas", replicas, "managed", controlPlane.managed)
	return ctrl.Result{RequeueAfter: connectorRequeue}, nil
}

func (r *ConnectorReconciler) resolveControlPlane(
	ctx context.Context,
	resource *kodiakv1alpha1.Connector,
) (*connectorControlPlane, ctrl.Result, error) {
	if resource.Spec.TailnetRef != nil {
		if r.IonscaleEndpoint == "" || r.IonscaleAdminKey == "" {
			return nil, r.connectorNotReady(ctx, resource, reasonConnectorConfiguration, "Ionscale API endpoint or admin key is not configured"), nil
		}
		var tailnet kodiakv1alpha1.Tailnet
		key := types.NamespacedName{Namespace: resource.Namespace, Name: resource.Spec.TailnetRef.Name}
		if err := r.Get(ctx, key, &tailnet); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, r.connectorNotReady(ctx, resource, reasonConnectorConfiguration, fmt.Sprintf("Tailnet %q does not exist", key.Name)), nil
			}
			return nil, ctrl.Result{}, err
		}
		if !meta.IsStatusConditionTrue(tailnet.Status.Conditions, kodiakv1alpha1.TailnetConditionReady) ||
			tailnet.Status.TailnetID == "" || tailnet.Status.LoginURL == "" {
			return nil, r.connectorNotReady(ctx, resource, reasonConnectorConfiguration, fmt.Sprintf("Tailnet %q is not ready", tailnet.Name)), nil
		}
		tailnetID, err := strconv.ParseUint(tailnet.Status.TailnetID, 10, 64)
		if err != nil {
			return nil, ctrl.Result{}, fmt.Errorf("invalid Tailnet status.tailnetID %q: %w", tailnet.Status.TailnetID, err)
		}
		ctrlClient, err := r.controlClient()
		if err != nil {
			return nil, ctrl.Result{}, err
		}
		return &connectorControlPlane{
			managed:   true,
			tailnetID: tailnetID,
			loginURL:  tailnet.Status.LoginURL,
			client:    ctrlClient,
		}, ctrl.Result{}, nil
	}

	if resource.Spec.AuthKeySecretRef == nil {
		return nil, r.connectorNotReady(ctx, resource, reasonConnectorConfiguration, "authKeySecretRef or tailnetRef is required"), nil
	}
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: resource.Namespace, Name: resource.Spec.AuthKeySecretRef.Name}
	if err := r.Get(ctx, key, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, r.connectorNotReady(ctx, resource, reasonConnectorConfiguration, fmt.Sprintf("Secret %q does not exist", key.Name)), nil
		}
		return nil, ctrl.Result{}, err
	}
	dataKey := resource.Spec.AuthKeySecretRef.Key
	if dataKey == "" {
		dataKey = defaultExternalKeyName
	}
	value := strings.TrimSpace(string(secret.Data[dataKey]))
	if value == "" {
		return nil, r.connectorNotReady(ctx, resource, reasonConnectorConfiguration, fmt.Sprintf("Secret %q does not contain key %q", key.Name, dataKey)), nil
	}
	return &connectorControlPlane{
		loginURL: resource.Spec.LoginURL,
		authKey:  value,
	}, ctrl.Result{}, nil
}

func (r *ConnectorReconciler) controlClient() (controlclient.ControlServerClientInterface, error) {
	factory := r.ClientFactory
	if factory == nil {
		factory = controlclient.DefaultClientFactory()
	}
	result, err := factory(r.IonscaleEndpoint, r.IonscaleAdminKey, r.IonscaleSkipTLS)
	if err != nil {
		return nil, fmt.Errorf("construct Ionscale client: %w", err)
	}
	return result, nil
}

func (r *ConnectorReconciler) reconcileWorkloadResources(
	ctx context.Context,
	resource *kodiakv1alpha1.Connector,
	loginURL string,
	replicas int32,
) error {
	image := r.TailscaleImage
	if image == "" {
		image = "tailscale/tailscale:v1.98.9"
	}

	objects := []client.Object{
		tsworkload.ServiceAccount(resource),
		tsworkload.Role(resource, replicas),
		tsworkload.RoleBinding(resource),
		tsworkload.HeadlessService(resource),
		tsworkload.StatefulSet(resource, image, loginURL, replicas),
	}
	for _, desired := range objects {
		if err := r.createOrUpdateOwned(ctx, resource, desired); err != nil {
			return err
		}
	}
	return nil
}

func (r *ConnectorReconciler) createOrUpdateOwned(
	ctx context.Context,
	owner *kodiakv1alpha1.Connector,
	desired client.Object,
) error {
	target := desired.DeepCopyObject().(client.Object)
	wanted := desired.DeepCopyObject().(client.Object)
	target.SetResourceVersion("")
	target.SetUID("")
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, target, func() error {
		switch current := target.(type) {
		case *corev1.ServiceAccount:
			next := wanted.(*corev1.ServiceAccount)
			current.Labels = next.Labels
			current.AutomountServiceAccountToken = next.AutomountServiceAccountToken
		case *rbacv1.Role:
			next := wanted.(*rbacv1.Role)
			current.Labels = next.Labels
			current.Rules = next.Rules
		case *rbacv1.RoleBinding:
			next := wanted.(*rbacv1.RoleBinding)
			current.Labels = next.Labels
			current.RoleRef = next.RoleRef
			current.Subjects = next.Subjects
		case *corev1.Service:
			next := wanted.(*corev1.Service)
			current.Labels = next.Labels
			current.Spec.Selector = next.Spec.Selector
			current.Spec.Ports = next.Spec.Ports
			current.Spec.PublishNotReadyAddresses = next.Spec.PublishNotReadyAddresses
			if current.Spec.ClusterIP == "" {
				current.Spec.ClusterIP = next.Spec.ClusterIP
			}
		case *appsv1.StatefulSet:
			next := wanted.(*appsv1.StatefulSet)
			current.Labels = next.Labels
			current.Spec = next.Spec
		default:
			return fmt.Errorf("unsupported Connector child type %T", target)
		}
		return controllerutil.SetControllerReference(owner, target, r.Scheme)
	})
	return err
}

func (r *ConnectorReconciler) reconcileStateSecrets(
	ctx context.Context,
	resource *kodiakv1alpha1.Connector,
	controlPlane *connectorControlPlane,
	replicas int32,
) error {
	for ordinal := int32(0); ordinal < replicas; ordinal++ {
		name := tsworkload.StateSecretName(resource.Name, ordinal)
		var secret corev1.Secret
		key := types.NamespacedName{Namespace: resource.Namespace, Name: name}
		if err := r.Get(ctx, key, &secret); err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}
			if err := r.prepareMissingStateSecret(ctx, resource, controlPlane, ordinal); err != nil {
				return err
			}
			secret = corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: resource.Namespace,
					Labels:    tsworkload.SelectorLabels(resource),
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{},
			}
			if err := controllerutil.SetControllerReference(resource, &secret, r.Scheme); err != nil {
				return err
			}
			if err := r.Create(ctx, &secret); err != nil {
				return err
			}
			if err := r.injectBootstrapKey(ctx, resource, &secret, controlPlane); err != nil {
				return err
			}
			continue
		}

		if secret.Annotations != nil && secret.Annotations[stateSecretBootstrapAuthKeyIDAnnotation] != "" &&
			len(secret.Data[kubetypes.KeyDeviceID]) != 0 &&
			len(secret.Data[stateSecretAuthKey]) == 0 &&
			len(secret.Data[stateSecretReissueAuthKey]) == 0 &&
			controlPlane.managed {
			if err := deleteRemoteAuthKey(ctx, controlPlane.client, secret.Annotations[stateSecretBootstrapAuthKeyIDAnnotation]); err != nil {
				return fmt.Errorf("revoke bootstrap key for %s: %w", name, err)
			}
			original := secret.DeepCopy()
			delete(secret.Annotations, stateSecretBootstrapAuthKeyIDAnnotation)
			delete(secret.Annotations, stateSecretBootstrapExpiryAnnotation)
			delete(secret.Data, stateSecretAuthKey)
			if err := r.Patch(ctx, &secret, client.MergeFrom(original)); err != nil {
				return err
			}
		}

		if controlPlane.managed &&
			len(secret.Data[kubetypes.KeyDeviceID]) == 0 &&
			bootstrapCredentialExpired(&secret, time.Now()) {
			original := secret.DeepCopy()
			if err := deleteRemoteAuthKey(ctx, controlPlane.client, secret.Annotations[stateSecretBootstrapAuthKeyIDAnnotation]); err != nil {
				return fmt.Errorf("revoke expired bootstrap key for %s: %w", name, err)
			}
			delete(secret.Annotations, stateSecretBootstrapAuthKeyIDAnnotation)
			delete(secret.Annotations, stateSecretBootstrapExpiryAnnotation)
			delete(secret.Data, stateSecretAuthKey)
			if err := r.Patch(ctx, &secret, client.MergeFrom(original)); err != nil {
				return err
			}
		}

		needsAuthKey := len(secret.Data[kubetypes.KeyDeviceID]) == 0 ||
			len(secret.Data[stateSecretReissueAuthKey]) != 0
		if needsAuthKey && len(secret.Data[stateSecretAuthKey]) == 0 {
			if err := r.injectBootstrapKey(ctx, resource, &secret, controlPlane); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *ConnectorReconciler) prepareMissingStateSecret(
	ctx context.Context,
	resource *kodiakv1alpha1.Connector,
	controlPlane *connectorControlPlane,
	ordinal int32,
) error {
	names := tsworkload.ChildNames(resource.Name)
	var statefulSet appsv1.StatefulSet
	err := r.Get(ctx, types.NamespacedName{
		Namespace: resource.Namespace,
		Name:      names.StatefulSet,
	}, &statefulSet)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if statefulSet.Spec.Replicas == nil || ordinal >= *statefulSet.Spec.Replicas {
		return nil
	}

	var previous *kodiakv1alpha1.ConnectorDeviceStatus
	for i := range resource.Status.Devices {
		if resource.Status.Devices[i].Ordinal == ordinal {
			previous = &resource.Status.Devices[i]
			break
		}
	}
	if !controlPlane.managed {
		return fmt.Errorf(
			"state Secret %q was lost for an external Connector; remove the stale control-plane device and recreate the Connector",
			tsworkload.StateSecretName(resource.Name, ordinal),
		)
	}
	if previous == nil || previous.DeviceID == "" || len(previous.TailnetIPs) == 0 {
		return fmt.Errorf(
			"state Secret %q was lost before its remote identity was recorded; manual control-plane cleanup is required",
			tsworkload.StateSecretName(resource.Name, ordinal),
		)
	}

	var pod corev1.Pod
	podKey := types.NamespacedName{
		Namespace: resource.Namespace,
		Name:      tsworkload.StateSecretName(resource.Name, ordinal),
	}
	if err := r.Get(ctx, podKey, &pod); err == nil {
		if err := r.Delete(ctx, &pod); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return fmt.Errorf("waiting for Pod %q to stop before recovering its identity", pod.Name)
	} else if !apierrors.IsNotFound(err) {
		return err
	}

	machines, err := controlPlane.client.ListMachines(ctx, controlPlane.tailnetID)
	if err != nil {
		return fmt.Errorf("list machines while recovering state Secret: %w", err)
	}
	if machine := machineForDevice(machines, *previous); machine != nil {
		if err := controlPlane.client.DeleteMachine(ctx, machine.GetId()); err != nil && !isConnectNotFound(err) {
			return fmt.Errorf("delete stale machine %d while recovering state Secret: %w", machine.GetId(), err)
		}
	}
	return nil
}

func (r *ConnectorReconciler) cleanupScaledDownReplicas(
	ctx context.Context,
	resource *kodiakv1alpha1.Connector,
	controlPlane *connectorControlPlane,
	replicas int32,
) (bool, error) {
	var secrets corev1.SecretList
	if err := r.List(
		ctx,
		&secrets,
		client.InNamespace(resource.Namespace),
		client.MatchingLabels{tsworkload.LabelConnector: tsworkload.ConnectorLabelValue(resource.Name)},
	); err != nil {
		return false, err
	}

	prefix := tsworkload.ChildNames(resource.Name).StatefulSet + "-"
	var machines []*pb.Machine
	machinesLoaded := false
	pending := false
	for i := range secrets.Items {
		secret := &secrets.Items[i]
		ordinalText, found := strings.CutPrefix(secret.Name, prefix)
		if !found {
			continue
		}
		ordinal, err := strconv.ParseInt(ordinalText, 10, 32)
		if err != nil || int32(ordinal) < replicas {
			continue
		}

		var pod corev1.Pod
		err = r.Get(ctx, types.NamespacedName{Namespace: resource.Namespace, Name: secret.Name}, &pod)
		if err == nil {
			pending = true
			continue
		}
		if !apierrors.IsNotFound(err) {
			return false, err
		}

		if controlPlane.managed {
			if !machinesLoaded {
				machines, err = controlPlane.client.ListMachines(ctx, controlPlane.tailnetID)
				if err != nil {
					return false, fmt.Errorf("list machines while scaling down Connector: %w", err)
				}
				machinesLoaded = true
			}
			device, err := connectorDeviceFromSecret(secret, int32(ordinal))
			if err != nil {
				return false, err
			}
			for _, observed := range resource.Status.Devices {
				if observed.Ordinal == int32(ordinal) &&
					observed.DeviceID != "" &&
					observed.DeviceID == device.DeviceID {
					device.MachineID = observed.MachineID
					break
				}
			}
			if machine := machineForDevice(machines, device); machine != nil {
				if err := controlPlane.client.DeleteMachine(ctx, machine.GetId()); err != nil && !isConnectNotFound(err) {
					return false, fmt.Errorf("delete scaled-down machine %d: %w", machine.GetId(), err)
				}
			}
			if keyID := secret.Annotations[stateSecretBootstrapAuthKeyIDAnnotation]; keyID != "" {
				if err := deleteRemoteAuthKey(ctx, controlPlane.client, keyID); err != nil {
					return false, fmt.Errorf("revoke scaled-down bootstrap key %s: %w", keyID, err)
				}
			}
		}
		if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
		pending = true
	}
	return pending, nil
}

func (r *ConnectorReconciler) injectBootstrapKey(
	ctx context.Context,
	resource *kodiakv1alpha1.Connector,
	secret *corev1.Secret,
	controlPlane *connectorControlPlane,
) error {
	value := controlPlane.authKey
	var remoteKeyID uint64
	if controlPlane.managed {
		remote, generated, err := controlPlane.client.CreateAuthKey(
			ctx,
			controlPlane.tailnetID,
			false,
			bootstrapKeyExpiry,
			append([]string(nil), resource.Spec.Tags...),
			true,
		)
		if err != nil {
			return fmt.Errorf("create Connector bootstrap key: %w", err)
		}
		remoteKeyID = remote.GetId()
		value = generated
	}

	original := secret.DeepCopy()
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[stateSecretAuthKey] = []byte(value)
	if remoteKeyID != 0 {
		if secret.Annotations == nil {
			secret.Annotations = map[string]string{}
		}
		secret.Annotations[stateSecretBootstrapAuthKeyIDAnnotation] = strconv.FormatUint(remoteKeyID, 10)
		secret.Annotations[stateSecretBootstrapExpiryAnnotation] = time.Now().Add(bootstrapKeyExpiry).UTC().Format(time.RFC3339)
	}
	if err := r.Patch(ctx, secret, client.MergeFrom(original)); err != nil {
		if remoteKeyID != 0 {
			_ = controlPlane.client.DeleteAuthKey(ctx, remoteKeyID)
		}
		return err
	}
	return nil
}

func bootstrapCredentialExpired(secret *corev1.Secret, now time.Time) bool {
	if secret.Annotations == nil || secret.Annotations[stateSecretBootstrapAuthKeyIDAnnotation] == "" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339, secret.Annotations[stateSecretBootstrapExpiryAnnotation])
	return err != nil || !expiresAt.After(now)
}

func (r *ConnectorReconciler) observe(
	ctx context.Context,
	resource *kodiakv1alpha1.Connector,
	controlPlane *connectorControlPlane,
	replicas int32,
) (kodiakv1alpha1.ConnectorStatus, []metav1.Condition, error) {
	status := kodiakv1alpha1.ConnectorStatus{
		ObservedGeneration: resource.Generation,
	}
	if controlPlane.managed {
		status.ManagedTailnetID = strconv.FormatUint(controlPlane.tailnetID, 10)
	}

	names := tsworkload.ChildNames(resource.Name)
	var statefulSet appsv1.StatefulSet
	if err := r.Get(ctx, types.NamespacedName{Namespace: resource.Namespace, Name: names.StatefulSet}, &statefulSet); err != nil {
		return status, nil, err
	}
	workloadReady := statefulSet.Status.ReadyReplicas == replicas

	devices := make([]kodiakv1alpha1.ConnectorDeviceStatus, 0, replicas)
	identityReady := true
	for ordinal := int32(0); ordinal < replicas; ordinal++ {
		device, err := r.deviceFromStateSecret(ctx, resource.Namespace, resource.Name, ordinal)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return status, nil, err
			}
			identityReady = false
			devices = append(devices, kodiakv1alpha1.ConnectorDeviceStatus{Ordinal: ordinal})
			continue
		}
		if device.DeviceID == "" || len(device.TailnetIPs) == 0 {
			identityReady = false
		}
		for _, observed := range resource.Status.Devices {
			if observed.Ordinal == ordinal &&
				observed.DeviceID != "" &&
				observed.DeviceID == device.DeviceID {
				device.MachineID = observed.MachineID
				break
			}
		}
		devices = append(devices, device)
	}

	controlPlaneReady := true
	routesReady := true
	if controlPlane.managed {
		machines, err := controlPlane.client.ListMachines(ctx, controlPlane.tailnetID)
		if err != nil {
			return status, nil, fmt.Errorf("list Connector machines: %w", err)
		}
		for i := range devices {
			machine := machineForDevice(machines, devices[i])
			if machine == nil {
				controlPlaneReady = false
				routesReady = false
				continue
			}
			connected := machine.GetConnected()
			devices[i].MachineID = strconv.FormatUint(machine.GetId(), 10)
			devices[i].Connected = &connected
			devices[i].Hostname = machine.GetName()
			devices[i].AdvertisedRoutes = sortedStrings(machine.GetAdvertisedRoutes())
			devices[i].EnabledRoutes = sortedStrings(machine.GetEnabledRoutes())
			if !machine.GetAuthorized() || !connected {
				controlPlaneReady = false
			}
			if !containsAllCIDRs(machine.GetAdvertisedRoutes(), resource.Spec.SubnetRouter.AdvertiseRoutes) ||
				!containsAllCIDRs(machine.GetEnabledRoutes(), resource.Spec.SubnetRouter.AdvertiseRoutes) {
				routesReady = false
			}
		}
	}
	status.Devices = devices

	conditions := []metav1.Condition{
		newConnectorCondition(resource, kodiakv1alpha1.ConnectorConditionWorkloadReady, boolStatus(workloadReady),
			choose(workloadReady, reasonWorkloadReady, reasonWorkloadNotReady),
			fmt.Sprintf("%d/%d StatefulSet replicas are ready", statefulSet.Status.ReadyReplicas, replicas)),
		newConnectorCondition(resource, kodiakv1alpha1.ConnectorConditionIdentityReady, boolStatus(identityReady),
			choose(identityReady, reasonIdentityReady, reasonIdentityNotReady),
			choose(identityReady, "Every replica has persistent Tailscale identity", "Waiting for replica identity state")),
	}

	if controlPlane.managed {
		conditions = append(conditions,
			newConnectorCondition(resource, kodiakv1alpha1.ConnectorConditionControlPlaneReady, boolStatus(controlPlaneReady),
				choose(controlPlaneReady, reasonControlPlaneReady, reasonControlPlaneNotReady),
				choose(controlPlaneReady, "Every replica is authorized and connected", "Waiting for managed control-plane machines")),
			newConnectorCondition(resource, kodiakv1alpha1.ConnectorConditionRoutesReady, boolStatus(routesReady),
				choose(routesReady, reasonRoutesReady, reasonRoutesNotReady),
				choose(routesReady, "Every advertised route is approved by control-plane policy", "Advertised routes are not fully approved")),
		)
	} else {
		conditions = append(conditions,
			newConnectorCondition(resource, kodiakv1alpha1.ConnectorConditionControlPlaneReady, metav1.ConditionUnknown,
				reasonExternalControlPlane, "External control-plane state is not observable"),
			newConnectorCondition(resource, kodiakv1alpha1.ConnectorConditionRoutesReady, metav1.ConditionUnknown,
				reasonExternalRoutes, "External route approval is not observable"),
		)
	}

	ready := workloadReady && identityReady
	if controlPlane.managed {
		ready = ready && controlPlaneReady && routesReady
	}
	conditions = append(conditions, newConnectorCondition(resource, kodiakv1alpha1.ConnectorConditionReady, boolStatus(ready),
		choose(ready, reasonConnectorReady, reasonConnectorError),
		choose(ready, "Connector is ready", "Connector is not ready")))

	return status, conditions, nil
}

func (r *ConnectorReconciler) deviceFromStateSecret(
	ctx context.Context,
	namespace, connectorName string,
	ordinal int32,
) (kodiakv1alpha1.ConnectorDeviceStatus, error) {
	device := kodiakv1alpha1.ConnectorDeviceStatus{Ordinal: ordinal}
	var secret corev1.Secret
	err := r.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      tsworkload.StateSecretName(connectorName, ordinal),
	}, &secret)
	if err != nil {
		return device, err
	}
	return connectorDeviceFromSecret(&secret, ordinal)
}

func connectorDeviceFromSecret(secret *corev1.Secret, ordinal int32) (kodiakv1alpha1.ConnectorDeviceStatus, error) {
	device := kodiakv1alpha1.ConnectorDeviceStatus{Ordinal: ordinal}
	device.DeviceID = string(secret.Data[kubetypes.KeyDeviceID])
	device.Hostname = string(secret.Data[kubetypes.KeyDeviceFQDN])
	if raw := secret.Data[kubetypes.KeyDeviceIPs]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &device.TailnetIPs); err != nil {
			return device, fmt.Errorf("decode device IPs from Secret %q: %w", secret.Name, err)
		}
	}
	device.TailnetIPs = sortedStrings(device.TailnetIPs)
	return device, nil
}

func (r *ConnectorReconciler) updateConnectorStatus(
	ctx context.Context,
	resource *kodiakv1alpha1.Connector,
	status kodiakv1alpha1.ConnectorStatus,
	conditions []metav1.Condition,
) error {
	original := resource.DeepCopy()
	status.Conditions = resource.Status.Conditions
	for _, condition := range conditions {
		upsertCondition(&status.Conditions, condition)
	}
	resource.Status = status
	if equality.Semantic.DeepEqual(original.Status, resource.Status) {
		return nil
	}
	return r.Status().Patch(ctx, resource, client.MergeFrom(original))
}

func (r *ConnectorReconciler) connectorNotReady(
	ctx context.Context,
	resource *kodiakv1alpha1.Connector,
	reason, message string,
) ctrl.Result {
	original := resource.DeepCopy()
	resource.Status.ObservedGeneration = resource.Generation
	upsertCondition(&resource.Status.Conditions, newConnectorCondition(
		resource,
		kodiakv1alpha1.ConnectorConditionReady,
		metav1.ConditionFalse,
		reason,
		message,
	))
	if err := r.Status().Patch(ctx, resource, client.MergeFrom(original)); err != nil {
		log.FromContext(ctx).Error(err, "update Connector status")
	}
	return ctrl.Result{RequeueAfter: connectorRetryRequeue}
}

func (r *ConnectorReconciler) connectorFail(
	ctx context.Context,
	resource *kodiakv1alpha1.Connector,
	reconcileErr error,
) (ctrl.Result, error) {
	result := r.connectorNotReady(ctx, resource, reasonConnectorError, reconcileErr.Error())
	return result, nil
}

func (r *ConnectorReconciler) handleDeletion(ctx context.Context, resource *kodiakv1alpha1.Connector) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(resource, kodiakFinalizer) {
		return ctrl.Result{}, nil
	}

	names := tsworkload.ChildNames(resource.Name)
	var statefulSet appsv1.StatefulSet
	err := r.Get(ctx, types.NamespacedName{Namespace: resource.Namespace, Name: names.StatefulSet}, &statefulSet)
	if err == nil {
		if statefulSet.DeletionTimestamp.IsZero() {
			if err := r.Delete(ctx, &statefulSet); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	if resource.Spec.TailnetRef != nil {
		if r.IonscaleEndpoint == "" || r.IonscaleAdminKey == "" {
			return ctrl.Result{RequeueAfter: connectorRetryRequeue}, nil
		}
		ctrlClient, err := r.controlClient()
		if err != nil {
			return ctrl.Result{RequeueAfter: connectorRetryRequeue}, nil
		}

		tailnetID, err := r.connectorTailnetIDForCleanup(ctx, resource)
		if err != nil {
			return ctrl.Result{RequeueAfter: connectorRetryRequeue}, nil
		}
		devices, bootstrapKeyIDs, err := r.connectorStateForCleanup(ctx, resource)
		if err != nil {
			return ctrl.Result{}, err
		}
		machines, err := ctrlClient.ListMachines(ctx, tailnetID)
		if err != nil && !isConnectNotFound(err) {
			return ctrl.Result{RequeueAfter: connectorRetryRequeue}, nil
		}
		for _, device := range devices {
			if machine := machineForDevice(machines, device); machine != nil {
				if err := ctrlClient.DeleteMachine(ctx, machine.GetId()); err != nil && !isConnectNotFound(err) {
					return ctrl.Result{RequeueAfter: connectorRetryRequeue}, nil
				}
			}
		}
		for _, keyID := range bootstrapKeyIDs {
			if err := deleteRemoteAuthKey(ctx, ctrlClient, keyID); err != nil {
				return ctrl.Result{RequeueAfter: connectorRetryRequeue}, nil
			}
		}
	}

	original := resource.DeepCopy()
	controllerutil.RemoveFinalizer(resource, kodiakFinalizer)
	return ctrl.Result{}, r.Patch(ctx, resource, client.MergeFrom(original))
}

func (r *ConnectorReconciler) connectorTailnetIDForCleanup(
	ctx context.Context,
	resource *kodiakv1alpha1.Connector,
) (uint64, error) {
	if resource.Status.ManagedTailnetID != "" {
		return strconv.ParseUint(resource.Status.ManagedTailnetID, 10, 64)
	}
	var tailnet kodiakv1alpha1.Tailnet
	err := r.Get(ctx, types.NamespacedName{
		Namespace: resource.Namespace,
		Name:      resource.Spec.TailnetRef.Name,
	}, &tailnet)
	if err != nil {
		return 0, err
	}
	if tailnet.Status.TailnetID == "" {
		return 0, fmt.Errorf("Tailnet %q has no remote ID", tailnet.Name)
	}
	return strconv.ParseUint(tailnet.Status.TailnetID, 10, 64)
}

func (r *ConnectorReconciler) connectorStateForCleanup(
	ctx context.Context,
	resource *kodiakv1alpha1.Connector,
) ([]kodiakv1alpha1.ConnectorDeviceStatus, []string, error) {
	devicesByOrdinal := make(map[int32]kodiakv1alpha1.ConnectorDeviceStatus, len(resource.Status.Devices))
	for _, device := range resource.Status.Devices {
		devicesByOrdinal[device.Ordinal] = device
	}

	var secrets corev1.SecretList
	if err := r.List(
		ctx,
		&secrets,
		client.InNamespace(resource.Namespace),
		client.MatchingLabels{tsworkload.LabelConnector: tsworkload.ConnectorLabelValue(resource.Name)},
	); err != nil {
		return nil, nil, err
	}
	prefix := tsworkload.ChildNames(resource.Name).StatefulSet + "-"
	var keyIDs []string
	for i := range secrets.Items {
		secret := &secrets.Items[i]
		rawOrdinal, found := strings.CutPrefix(secret.Name, prefix)
		if !found {
			continue
		}
		ordinal, err := strconv.ParseInt(rawOrdinal, 10, 32)
		if err != nil {
			continue
		}
		device, err := connectorDeviceFromSecret(secret, int32(ordinal))
		if err != nil {
			return nil, nil, err
		}
		if observed, found := devicesByOrdinal[int32(ordinal)]; found &&
			observed.DeviceID != "" &&
			observed.DeviceID == device.DeviceID {
			device.MachineID = observed.MachineID
		}
		devicesByOrdinal[int32(ordinal)] = device
		if keyID := secret.Annotations[stateSecretBootstrapAuthKeyIDAnnotation]; keyID != "" {
			keyIDs = append(keyIDs, keyID)
		}
	}

	ordinals := make([]int, 0, len(devicesByOrdinal))
	for ordinal := range devicesByOrdinal {
		ordinals = append(ordinals, int(ordinal))
	}
	sort.Ints(ordinals)
	devices := make([]kodiakv1alpha1.ConnectorDeviceStatus, 0, len(ordinals))
	for _, ordinal := range ordinals {
		devices = append(devices, devicesByOrdinal[int32(ordinal)])
	}
	sort.Strings(keyIDs)
	return devices, keyIDs, nil
}

func machineForDevice(machines []*pb.Machine, device kodiakv1alpha1.ConnectorDeviceStatus) *pb.Machine {
	if device.MachineID != "" {
		for _, machine := range machines {
			if device.MachineID == strconv.FormatUint(machine.GetId(), 10) {
				return machine
			}
		}
		return nil
	}
	for _, machine := range machines {
		if machineHasDeviceIP(machine, device.TailnetIPs) {
			return machine
		}
	}
	return nil
}

func machineHasDeviceIP(machine *pb.Machine, deviceIPs []string) bool {
	if len(deviceIPs) == 0 {
		return false
	}
	remote := []string{machine.GetIpv4(), machine.GetIpv6()}
	for _, left := range remote {
		leftIP, err := netip.ParseAddr(strings.TrimSpace(left))
		if err != nil {
			continue
		}
		for _, right := range deviceIPs {
			rightIP, err := netip.ParseAddr(strings.TrimSpace(right))
			if err == nil && leftIP == rightIP {
				return true
			}
		}
	}
	return false
}

func containsAllCIDRs(observed, desired []string) bool {
	set := make(map[string]struct{}, len(observed))
	for _, value := range observed {
		prefix, err := netip.ParsePrefix(value)
		if err == nil {
			set[prefix.Masked().String()] = struct{}{}
		}
	}
	for _, value := range desired {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return false
		}
		if _, ok := set[prefix.Masked().String()]; !ok {
			return false
		}
	}
	return true
}

func desiredReplicas(resource *kodiakv1alpha1.Connector) int32 {
	if resource.Spec.Replicas == nil {
		return 1
	}
	return *resource.Spec.Replicas
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func boolStatus(value bool) metav1.ConditionStatus {
	if value {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}

func choose[T any](condition bool, whenTrue, whenFalse T) T {
	if condition {
		return whenTrue
	}
	return whenFalse
}

func newConnectorCondition(
	resource *kodiakv1alpha1.Connector,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string,
) metav1.Condition {
	return metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: resource.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
}

func (r *ConnectorReconciler) connectorsForTailnet(ctx context.Context, object client.Object) []ctrl.Request {
	tailnet, ok := object.(*kodiakv1alpha1.Tailnet)
	if !ok {
		return nil
	}
	var connectors kodiakv1alpha1.ConnectorList
	if err := r.List(
		ctx,
		&connectors,
		client.InNamespace(tailnet.Namespace),
		client.MatchingFields{connectorTailnetIndex: tailnet.Name},
	); err != nil {
		return nil
	}
	requests := make([]ctrl.Request, 0, len(connectors.Items))
	for i := range connectors.Items {
		requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&connectors.Items[i])})
	}
	return requests
}

func (r *ConnectorReconciler) connectorsForSecret(ctx context.Context, object client.Object) []ctrl.Request {
	secret, ok := object.(*corev1.Secret)
	if !ok {
		return nil
	}
	for _, owner := range secret.OwnerReferences {
		if owner.APIVersion == kodiakv1alpha1.GroupVersion.String() && owner.Kind == "Connector" {
			return []ctrl.Request{{NamespacedName: types.NamespacedName{Namespace: secret.Namespace, Name: owner.Name}}}
		}
	}
	var connectors kodiakv1alpha1.ConnectorList
	if err := r.List(
		ctx,
		&connectors,
		client.InNamespace(secret.Namespace),
		client.MatchingFields{connectorAuthKeySecretIndex: secret.Name},
	); err != nil {
		return nil
	}
	requests := make([]ctrl.Request, 0, len(connectors.Items))
	for i := range connectors.Items {
		requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&connectors.Items[i])})
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConnectorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&kodiakv1alpha1.Connector{},
		connectorTailnetIndex,
		func(object client.Object) []string {
			connector := object.(*kodiakv1alpha1.Connector)
			if connector.Spec.TailnetRef == nil {
				return nil
			}
			return []string{connector.Spec.TailnetRef.Name}
		},
	); err != nil {
		return fmt.Errorf("index Connector tailnetRef: %w", err)
	}
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&kodiakv1alpha1.Connector{},
		connectorAuthKeySecretIndex,
		func(object client.Object) []string {
			connector := object.(*kodiakv1alpha1.Connector)
			if connector.Spec.AuthKeySecretRef == nil {
				return nil
			}
			return []string{connector.Spec.AuthKeySecretRef.Name}
		},
	); err != nil {
		return fmt.Errorf("index Connector authKeySecretRef: %w", err)
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&kodiakv1alpha1.Connector{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Watches(&kodiakv1alpha1.Tailnet{}, handler.EnqueueRequestsFromMapFunc(r.connectorsForTailnet)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.connectorsForSecret)).
		Named("connector").
		Complete(r)
}
