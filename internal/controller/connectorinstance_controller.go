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
	"fmt"
	"sort"
	"strconv"
	"time"

	pb "github.com/jsiebens/ionscale/pkg/gen/ionscale/v1"
	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
	controlclient "github.com/mNi-Cloud/kodiak/internal/client"
	tsworkload "github.com/mNi-Cloud/kodiak/internal/tailscale/workload"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	bootstrapSecretAuthKey          = "TS_AUTH_KEY"
	bootstrapAuthKeyIDAnnotation    = "kodiak.mnicloud.jp/bootstrap-auth-key-id"
	bootstrapExpiryAnnotation       = "kodiak.mnicloud.jp/bootstrap-auth-key-expires-at"
	bootstrapKeyExpiry              = 10 * time.Minute
	connectorInstanceRequeue        = 10 * time.Second
	connectorInstanceCleanupRequeue = time.Second
)

// ConnectorInstanceReconciler manages exactly one Pod incarnation and, for a
// managed Tailnet, exactly one corresponding external device.
type ConnectorInstanceReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	ClientFactory    controlclient.ClientFactory
	IonscaleEndpoint string
	IonscaleAdminKey string
	IonscaleSkipTLS  bool
}

func (r *ConnectorInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var instance kodiakv1alpha1.ConnectorInstance
	if err := r.Get(ctx, req.NamespacedName, &instance); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !instance.DeletionTimestamp.IsZero() {
		return r.deleteInstance(ctx, &instance)
	}
	if result, err := ensureFinalizer(ctx, r.Client, &instance); err != nil || result.Requeue {
		return result, err
	}

	hostname := tsworkload.RequestedHostname(&instance)
	if hostname == "kdk-" {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	authSecretName, authSecretKey, ctrlClient, err := r.prepareAuthentication(ctx, &instance)
	if err != nil {
		return ctrl.Result{}, err
	}
	if authSecretName == "" {
		return ctrl.Result{RequeueAfter: connectorInstanceRequeue}, nil
	}

	var pod corev1.Pod
	podKey := types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name}
	err = r.Get(ctx, podKey, &pod)
	switch {
	case apierrors.IsNotFound(err) && instance.Status.PodUID != "":
		return r.setReady(ctx, &instance, metav1.ConditionFalse, "PodLost", "the Pod incarnation was deleted")
	case apierrors.IsNotFound(err):
		pod = *tsworkload.Pod(&instance, authSecretName, authSecretKey)
		if err := controllerutil.SetControllerReference(&instance, &pod, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, &pod); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second}, nil
	case err != nil:
		return ctrl.Result{}, err
	}

	if instance.Status.PodUID == "" || instance.Status.PodName == "" || instance.Status.RequestedHostname == "" {
		before := instance.Status.DeepCopy()
		instance.Status.PodName = pod.Name
		instance.Status.PodUID = string(pod.UID)
		instance.Status.RequestedHostname = hostname
		instance.Status.ObservedGeneration = instance.Generation
		if !equality.Semantic.DeepEqual(before, &instance.Status) {
			if err := r.Status().Update(ctx, &instance); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
	}

	if instance.Spec.ManagedTailnetID == "" {
		if podReady(&pod) {
			return r.setReady(ctx, &instance, metav1.ConditionTrue, "PodReady", "external Connector Pod is ready")
		}
		return r.setReady(ctx, &instance, metav1.ConditionFalse, "PodNotReady", "external Connector Pod is not ready")
	}
	return r.observeManaged(ctx, &instance, &pod, ctrlClient)
}

func (r *ConnectorInstanceReconciler) prepareAuthentication(
	ctx context.Context,
	instance *kodiakv1alpha1.ConnectorInstance,
) (string, string, controlclient.ControlServerClientInterface, error) {
	if instance.Spec.ManagedTailnetID == "" {
		if instance.Spec.AuthKeySecretRef == nil {
			return "", "", nil, fmt.Errorf("ConnectorInstance has neither managedTailnetID nor authKeySecretRef")
		}
		key := instance.Spec.AuthKeySecretRef.Key
		if key == "" {
			key = defaultExternalKeyName
		}
		return instance.Spec.AuthKeySecretRef.Name, key, nil, nil
	}

	ctrlClient, err := r.controlClient()
	if err != nil {
		return "", "", nil, err
	}
	secretName := tsworkload.BootstrapSecretName(instance.Name)
	var secret corev1.Secret
	err = r.Get(ctx, types.NamespacedName{Namespace: instance.Namespace, Name: secretName}, &secret)
	if err == nil {
		if expiry, parseErr := time.Parse(time.RFC3339, secret.Annotations[bootstrapExpiryAnnotation]); parseErr == nil &&
			time.Now().After(expiry) && instance.Status.Device.MachineID == "" {
			if deleteErr := deleteRemoteAuthKey(ctx, ctrlClient, secret.Annotations[bootstrapAuthKeyIDAnnotation]); deleteErr != nil {
				return "", "", nil, deleteErr
			}
			if deleteErr := r.Delete(ctx, &secret); client.IgnoreNotFound(deleteErr) != nil {
				return "", "", nil, deleteErr
			}
			return "", "", ctrlClient, nil
		}
		return secretName, bootstrapSecretAuthKey, ctrlClient, nil
	}
	if !apierrors.IsNotFound(err) {
		return "", "", nil, err
	}
	if instance.Status.Device.MachineID != "" {
		return secretName, bootstrapSecretAuthKey, ctrlClient, nil
	}

	tailnetID, err := strconv.ParseUint(instance.Spec.ManagedTailnetID, 10, 64)
	if err != nil {
		return "", "", nil, fmt.Errorf("invalid managedTailnetID %q: %w", instance.Spec.ManagedTailnetID, err)
	}
	remote, value, err := ctrlClient.CreateAuthKey(ctx, tailnetID, true, bootstrapKeyExpiry, instance.Spec.Tags, true)
	if err != nil {
		return "", "", nil, err
	}
	secret = corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: instance.Namespace,
			Labels:    tsworkload.InstanceLabels(instance),
			Annotations: map[string]string{
				bootstrapAuthKeyIDAnnotation: strconv.FormatUint(remote.GetId(), 10),
				bootstrapExpiryAnnotation:    time.Now().Add(bootstrapKeyExpiry).UTC().Format(time.RFC3339),
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{bootstrapSecretAuthKey: []byte(value)},
	}
	if err := controllerutil.SetControllerReference(instance, &secret, r.Scheme); err != nil {
		_ = deleteRemoteAuthKey(ctx, ctrlClient, strconv.FormatUint(remote.GetId(), 10))
		return "", "", nil, err
	}
	if err := r.Create(ctx, &secret); err != nil {
		_ = deleteRemoteAuthKey(ctx, ctrlClient, strconv.FormatUint(remote.GetId(), 10))
		return "", "", nil, err
	}
	return secretName, bootstrapSecretAuthKey, ctrlClient, nil
}

func (r *ConnectorInstanceReconciler) observeManaged(
	ctx context.Context,
	instance *kodiakv1alpha1.ConnectorInstance,
	pod *corev1.Pod,
	ctrlClient controlclient.ControlServerClientInterface,
) (ctrl.Result, error) {
	tailnetID, err := strconv.ParseUint(instance.Spec.ManagedTailnetID, 10, 64)
	if err != nil {
		return ctrl.Result{}, err
	}
	machines, err := ctrlClient.ListMachines(ctx, tailnetID)
	if err != nil {
		return ctrl.Result{RequeueAfter: connectorInstanceRequeue}, nil
	}
	machine := findInstanceMachine(machines, instance)
	if machine == nil {
		return r.setReady(ctx, instance, metav1.ConditionFalse, "DeviceNotRegistered", "managed device has not registered")
	}

	device := kodiakv1alpha1.ConnectorDeviceStatus{
		Ordinal:          instance.Spec.Slot,
		MachineID:        strconv.FormatUint(machine.GetId(), 10),
		Hostname:         machine.GetName(),
		Connected:        pointer(machine.GetConnected()),
		AdvertisedRoutes: sortedCopy(machine.GetAdvertisedRoutes()),
		EnabledRoutes:    sortedCopy(machine.GetEnabledRoutes()),
	}
	if machine.GetIpv4() != "" {
		device.TailnetIPs = append(device.TailnetIPs, machine.GetIpv4())
	}
	if machine.GetIpv6() != "" {
		device.TailnetIPs = append(device.TailnetIPs, machine.GetIpv6())
	}
	sort.Strings(device.TailnetIPs)

	before := instance.Status.DeepCopy()
	instance.Status.Device = device
	instance.Status.ObservedGeneration = instance.Generation
	ready := podReady(pod) && machine.GetAuthorized() && machine.GetConnected() &&
		containsAll(machine.GetAdvertisedRoutes(), instance.Spec.AdvertiseRoutes) &&
		containsAll(machine.GetEnabledRoutes(), instance.Spec.AdvertiseRoutes)
	reason := "ManagedDeviceReady"
	message := "Pod is ready and all advertised routes are approved"
	status := metav1.ConditionTrue
	if !ready {
		reason = "ManagedDeviceNotReady"
		message = "waiting for Pod connectivity and route approval"
		status = metav1.ConditionFalse
	}
	meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               kodiakv1alpha1.ConnectorInstanceConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: instance.Generation,
	})
	if !equality.Semantic.DeepEqual(before, &instance.Status) {
		if err := r.Status().Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// Once the machine identity is recorded, the one-time bootstrap credential
	// is no longer needed by the running Pod.
	if err := r.deleteBootstrapSecret(ctx, instance, ctrlClient); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: connectorInstanceRequeue}, nil
}

func (r *ConnectorInstanceReconciler) setReady(
	ctx context.Context,
	instance *kodiakv1alpha1.ConnectorInstance,
	status metav1.ConditionStatus,
	reason, message string,
) (ctrl.Result, error) {
	before := instance.Status.DeepCopy()
	instance.Status.ObservedGeneration = instance.Generation
	meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               kodiakv1alpha1.ConnectorInstanceConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: instance.Generation,
	})
	if !equality.Semantic.DeepEqual(before, &instance.Status) {
		if err := r.Status().Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: connectorInstanceRequeue}, nil
}

func (r *ConnectorInstanceReconciler) deleteInstance(ctx context.Context, instance *kodiakv1alpha1.ConnectorInstance) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(instance, kodiakFinalizer) {
		return ctrl.Result{}, nil
	}
	var pod corev1.Pod
	err := r.Get(ctx, types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name}, &pod)
	if err == nil {
		if err := r.Delete(ctx, &pod); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: connectorInstanceCleanupRequeue}, nil
	}
	if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	if instance.Spec.ManagedTailnetID != "" {
		ctrlClient, err := r.controlClient()
		if err != nil {
			return ctrl.Result{}, err
		}
		tailnetID, err := strconv.ParseUint(instance.Spec.ManagedTailnetID, 10, 64)
		if err != nil {
			return ctrl.Result{}, err
		}
		machines, err := ctrlClient.ListMachines(ctx, tailnetID)
		if err != nil {
			return ctrl.Result{}, err
		}
		if machine := findInstanceMachine(machines, instance); machine != nil {
			if err := ctrlClient.DeleteMachine(ctx, machine.GetId()); err != nil && !isConnectNotFound(err) {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: connectorInstanceCleanupRequeue}, nil
		}
		if err := r.deleteBootstrapSecret(ctx, instance, ctrlClient); err != nil {
			return ctrl.Result{}, err
		}
	}
	controllerutil.RemoveFinalizer(instance, kodiakFinalizer)
	return ctrl.Result{}, r.Update(ctx, instance)
}

func (r *ConnectorInstanceReconciler) deleteBootstrapSecret(
	ctx context.Context,
	instance *kodiakv1alpha1.ConnectorInstance,
	ctrlClient controlclient.ControlServerClientInterface,
) error {
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: instance.Namespace, Name: tsworkload.BootstrapSecretName(instance.Name)}
	if err := r.Get(ctx, key, &secret); err != nil {
		return client.IgnoreNotFound(err)
	}
	if err := deleteRemoteAuthKey(ctx, ctrlClient, secret.Annotations[bootstrapAuthKeyIDAnnotation]); err != nil {
		return err
	}
	return client.IgnoreNotFound(r.Delete(ctx, &secret))
}

func (r *ConnectorInstanceReconciler) controlClient() (controlclient.ControlServerClientInterface, error) {
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

func findInstanceMachine(machines []*pb.Machine, instance *kodiakv1alpha1.ConnectorInstance) *pb.Machine {
	if instance.Status.Device.MachineID != "" {
		for _, machine := range machines {
			if strconv.FormatUint(machine.GetId(), 10) == instance.Status.Device.MachineID {
				return machine
			}
		}
	}
	hostname := instance.Status.RequestedHostname
	if hostname == "" {
		hostname = tsworkload.RequestedHostname(instance)
	}
	for _, machine := range machines {
		if machine.GetName() == hostname {
			return machine
		}
	}
	return nil
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func containsAll(observed, desired []string) bool {
	values := make(map[string]struct{}, len(observed))
	for _, value := range observed {
		values[value] = struct{}{}
	}
	for _, value := range desired {
		if _, ok := values[value]; !ok {
			return false
		}
	}
	return true
}

func sortedCopy(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func pointer(value bool) *bool {
	return &value
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConnectorInstanceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kodiakv1alpha1.ConnectorInstance{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.Secret{}).
		Named("connectorinstance").
		Complete(r)
}
