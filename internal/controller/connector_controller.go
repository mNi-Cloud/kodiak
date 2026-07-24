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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

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
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

const (
	connectorRequeue       = 30 * time.Second
	defaultExternalKeyName = "TS_AUTH_KEY"

	connectorTailnetIndex       = "spec.tailnetRef.name"
	connectorAuthKeySecretIndex = "spec.authKeySecretRef.name"
)

// ConnectorReconciler manages a logical subnet-router service. Each replica
// slot is fulfilled by a replaceable ConnectorInstance.
type ConnectorReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	ClientFactory    controlclient.ClientFactory
	IonscaleEndpoint string
	IonscaleAdminKey string
	IonscaleSkipTLS  bool
	TailscaleImage   string
}

type resolvedConnector struct {
	managedTailnetID string
	loginURL         string
	authKeySecretRef *corev1.SecretKeySelector
	image            string
}

func (r *ConnectorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var connector kodiakv1alpha1.Connector
	if err := r.Get(ctx, req.NamespacedName, &connector); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !connector.DeletionTimestamp.IsZero() {
		return r.deleteConnector(ctx, &connector)
	}
	if result, err := ensureFinalizer(ctx, r.Client, &connector); err != nil || result.Requeue {
		return result, err
	}

	resolved, message, err := r.resolve(ctx, &connector)
	if err != nil {
		return ctrl.Result{}, err
	}
	if resolved == nil {
		return r.updateNotReady(ctx, &connector, "ConfigurationMissing", message)
	}

	var instances kodiakv1alpha1.ConnectorInstanceList
	if err := r.List(ctx, &instances,
		client.InNamespace(connector.Namespace),
		client.MatchingLabels{tsworkload.LabelConnector: tsworkload.ConnectorLabelValue(connector.Name)},
	); err != nil {
		return ctrl.Result{}, err
	}

	revision, err := connectorRevision(&connector, resolved)
	if err != nil {
		return ctrl.Result{}, err
	}
	replicas := desiredReplicas(&connector)
	changed, err := r.reconcileInstances(ctx, &connector, resolved, revision, replicas, instances.Items)
	if err != nil {
		return ctrl.Result{}, err
	}
	if changed {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	if err := r.updateStatus(ctx, &connector, resolved, revision, replicas, instances.Items); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: connectorRequeue}, nil
}

func (r *ConnectorReconciler) resolve(ctx context.Context, connector *kodiakv1alpha1.Connector) (*resolvedConnector, string, error) {
	image := r.TailscaleImage
	if image == "" {
		image = "tailscale/tailscale:v1.98.9"
	}
	if connector.Spec.TailnetRef != nil {
		if r.IonscaleEndpoint == "" || r.IonscaleAdminKey == "" {
			return nil, "Ionscale API endpoint or admin key is not configured", nil
		}
		var tailnet kodiakv1alpha1.Tailnet
		key := types.NamespacedName{Namespace: connector.Namespace, Name: connector.Spec.TailnetRef.Name}
		if err := r.Get(ctx, key, &tailnet); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, fmt.Sprintf("Tailnet %q does not exist", key.Name), nil
			}
			return nil, "", err
		}
		if !meta.IsStatusConditionTrue(tailnet.Status.Conditions, kodiakv1alpha1.TailnetConditionReady) ||
			tailnet.Status.TailnetID == "" || tailnet.Status.LoginURL == "" {
			return nil, fmt.Sprintf("Tailnet %q is not ready", tailnet.Name), nil
		}
		if _, err := strconv.ParseUint(tailnet.Status.TailnetID, 10, 64); err != nil {
			return nil, "", fmt.Errorf("invalid Tailnet status.tailnetID %q: %w", tailnet.Status.TailnetID, err)
		}
		return &resolvedConnector{
			managedTailnetID: tailnet.Status.TailnetID,
			loginURL:         tailnet.Status.LoginURL,
			image:            image,
		}, "", nil
	}

	if connector.Spec.AuthKeySecretRef == nil {
		return nil, "tailnetRef or authKeySecretRef is required", nil
	}
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: connector.Namespace, Name: connector.Spec.AuthKeySecretRef.Name}
	if err := r.Get(ctx, key, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Sprintf("Secret %q does not exist", key.Name), nil
		}
		return nil, "", err
	}
	dataKey := connector.Spec.AuthKeySecretRef.Key
	if dataKey == "" {
		dataKey = defaultExternalKeyName
	}
	if len(secret.Data[dataKey]) == 0 {
		return nil, fmt.Sprintf("Secret %q does not contain key %q", key.Name, dataKey), nil
	}
	return &resolvedConnector{
		loginURL:         connector.Spec.LoginURL,
		authKeySecretRef: connector.Spec.AuthKeySecretRef.DeepCopy(),
		image:            image,
	}, "", nil
}

func connectorRevision(connector *kodiakv1alpha1.Connector, resolved *resolvedConnector) (string, error) {
	value := struct {
		Resolved *resolvedConnector
		Tags     []string
		Routes   []string
		Workload kodiakv1alpha1.ConnectorWorkloadSpec
	}{
		Resolved: resolved,
		Tags:     connector.Spec.Tags,
		Routes:   connector.Spec.SubnetRouter.AdvertiseRoutes,
		Workload: connector.Spec.Workload,
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode Connector revision: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8]), nil
}

func (r *ConnectorReconciler) reconcileInstances(
	ctx context.Context,
	connector *kodiakv1alpha1.Connector,
	resolved *resolvedConnector,
	revision string,
	replicas int32,
	instances []kodiakv1alpha1.ConnectorInstance,
) (bool, error) {
	bySlot := make(map[int32][]*kodiakv1alpha1.ConnectorInstance)
	for i := range instances {
		instance := &instances[i]
		bySlot[instance.Spec.Slot] = append(bySlot[instance.Spec.Slot], instance)
		if instance.Spec.Slot >= replicas {
			if err := r.Delete(ctx, instance); client.IgnoreNotFound(err) != nil {
				return false, err
			}
			return true, nil
		}
	}

	for slot := int32(0); slot < replicas; slot++ {
		slotInstances := bySlot[slot]
		var readyCurrent *kodiakv1alpha1.ConnectorInstance
		var currentExists bool
		for _, instance := range slotInstances {
			if instance.Spec.Revision == revision {
				condition := meta.FindStatusCondition(instance.Status.Conditions, kodiakv1alpha1.ConnectorInstanceConditionReady)
				if condition != nil && condition.Status == metav1.ConditionFalse && condition.Reason == "PodLost" {
					if err := r.Delete(ctx, instance); client.IgnoreNotFound(err) != nil {
						return false, err
					}
					return true, nil
				}
				currentExists = true
				if meta.IsStatusConditionTrue(instance.Status.Conditions, kodiakv1alpha1.ConnectorInstanceConditionReady) {
					readyCurrent = instance
				}
			}
		}
		if !currentExists {
			instance := &kodiakv1alpha1.ConnectorInstance{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: tsworkload.InstanceGenerateName(connector.Name, slot),
					Namespace:    connector.Namespace,
					Labels: map[string]string{
						tsworkload.LabelManaged:   "true",
						tsworkload.LabelConnector: tsworkload.ConnectorLabelValue(connector.Name),
						tsworkload.LabelSlot:      strconv.Itoa(int(slot)),
						tsworkload.LabelRevision:  revision,
					},
				},
				Spec: kodiakv1alpha1.ConnectorInstanceSpec{
					ConnectorRef:     corev1.LocalObjectReference{Name: connector.Name},
					Slot:             slot,
					Revision:         revision,
					ManagedTailnetID: resolved.managedTailnetID,
					LoginURL:         resolved.loginURL,
					AuthKeySecretRef: resolved.authKeySecretRef,
					Tags:             append([]string(nil), connector.Spec.Tags...),
					AdvertiseRoutes:  append([]string(nil), connector.Spec.SubnetRouter.AdvertiseRoutes...),
					Image:            resolved.image,
					Workload:         *connector.Spec.Workload.DeepCopy(),
				},
			}
			if err := controllerutil.SetControllerReference(connector, instance, r.Scheme); err != nil {
				return false, err
			}
			if err := r.Create(ctx, instance); err != nil {
				return false, err
			}
			return true, nil
		}
		if readyCurrent != nil {
			for _, instance := range slotInstances {
				if instance.Name == readyCurrent.Name {
					continue
				}
				if err := r.Delete(ctx, instance); client.IgnoreNotFound(err) != nil {
					return false, err
				}
				return true, nil
			}
		}
	}
	return false, nil
}

func (r *ConnectorReconciler) updateStatus(
	ctx context.Context,
	connector *kodiakv1alpha1.Connector,
	resolved *resolvedConnector,
	revision string,
	replicas int32,
	instances []kodiakv1alpha1.ConnectorInstance,
) error {
	before := connector.Status.DeepCopy()
	devices := make([]kodiakv1alpha1.ConnectorDeviceStatus, 0, replicas)
	readyCount := int32(0)
	for slot := int32(0); slot < replicas; slot++ {
		for i := range instances {
			instance := &instances[i]
			if instance.Spec.Slot != slot || instance.Spec.Revision != revision ||
				!meta.IsStatusConditionTrue(instance.Status.Conditions, kodiakv1alpha1.ConnectorInstanceConditionReady) {
				continue
			}
			device := *instance.Status.Device.DeepCopy()
			device.Ordinal = slot
			devices = append(devices, device)
			readyCount++
			break
		}
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Ordinal < devices[j].Ordinal })
	connector.Status.ObservedGeneration = connector.Generation
	connector.Status.ManagedTailnetID = resolved.managedTailnetID
	connector.Status.Devices = devices
	setConnectorCondition(connector, kodiakv1alpha1.ConnectorConditionAvailable, boolStatus(readyCount > 0),
		"InstancesAvailable", fmt.Sprintf("%d of %d instances are available", readyCount, replicas))
	setConnectorCondition(connector, kodiakv1alpha1.ConnectorConditionWorkloadReady, boolStatus(readyCount == replicas),
		"InstancesReady", fmt.Sprintf("%d of %d instances are ready", readyCount, replicas))
	if resolved.managedTailnetID != "" {
		setConnectorCondition(connector, kodiakv1alpha1.ConnectorConditionControlPlaneReady, boolStatus(readyCount == replicas),
			"ManagedDevicesReady", fmt.Sprintf("%d of %d managed devices are ready", readyCount, replicas))
		setConnectorCondition(connector, kodiakv1alpha1.ConnectorConditionRoutesReady, boolStatus(readyCount == replicas),
			"RoutesApproved", fmt.Sprintf("%d of %d instances have approved routes", readyCount, replicas))
	} else {
		setConnectorCondition(connector, kodiakv1alpha1.ConnectorConditionControlPlaneReady, metav1.ConditionUnknown,
			"ExternalControlPlaneUnverified", "external control-plane state is not observable")
		setConnectorCondition(connector, kodiakv1alpha1.ConnectorConditionRoutesReady, metav1.ConditionUnknown,
			"ExternalRoutesUnverified", "external route approval is not observable")
	}
	setConnectorCondition(connector, kodiakv1alpha1.ConnectorConditionReady, boolStatus(readyCount == replicas),
		"DesiredInstancesReady", fmt.Sprintf("%d of %d desired instances are ready", readyCount, replicas))
	if equality.Semantic.DeepEqual(before, &connector.Status) {
		return nil
	}
	return r.Status().Update(ctx, connector)
}

func (r *ConnectorReconciler) updateNotReady(ctx context.Context, connector *kodiakv1alpha1.Connector, reason, message string) (ctrl.Result, error) {
	before := connector.Status.DeepCopy()
	connector.Status.ObservedGeneration = connector.Generation
	setConnectorCondition(connector, kodiakv1alpha1.ConnectorConditionReady, metav1.ConditionFalse, reason, message)
	setConnectorCondition(connector, kodiakv1alpha1.ConnectorConditionAvailable, metav1.ConditionFalse, reason, message)
	if !equality.Semantic.DeepEqual(before, &connector.Status) {
		if err := r.Status().Update(ctx, connector); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *ConnectorReconciler) deleteConnector(ctx context.Context, connector *kodiakv1alpha1.Connector) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(connector, kodiakFinalizer) {
		return ctrl.Result{}, nil
	}
	var instances kodiakv1alpha1.ConnectorInstanceList
	if err := r.List(ctx, &instances,
		client.InNamespace(connector.Namespace),
		client.MatchingLabels{tsworkload.LabelConnector: tsworkload.ConnectorLabelValue(connector.Name)},
	); err != nil {
		return ctrl.Result{}, err
	}
	if len(instances.Items) != 0 {
		for i := range instances.Items {
			if err := r.Delete(ctx, &instances.Items[i]); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	controllerutil.RemoveFinalizer(connector, kodiakFinalizer)
	return ctrl.Result{}, r.Update(ctx, connector)
}

func desiredReplicas(connector *kodiakv1alpha1.Connector) int32 {
	if connector.Spec.Replicas == nil {
		return 1
	}
	return *connector.Spec.Replicas
}

func setConnectorCondition(connector *kodiakv1alpha1.Connector, conditionType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&connector.Status.Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: connector.Generation,
	})
}

func boolStatus(value bool) metav1.ConditionStatus {
	if value {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}

func (r *ConnectorReconciler) connectorsForTailnet(ctx context.Context, object client.Object) []ctrl.Request {
	var connectors kodiakv1alpha1.ConnectorList
	if err := r.List(ctx, &connectors,
		client.InNamespace(object.GetNamespace()),
		client.MatchingFields{connectorTailnetIndex: object.GetName()},
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
	var connectors kodiakv1alpha1.ConnectorList
	if err := r.List(ctx, &connectors,
		client.InNamespace(object.GetNamespace()),
		client.MatchingFields{connectorAuthKeySecretIndex: object.GetName()},
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
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &kodiakv1alpha1.Connector{}, connectorTailnetIndex,
		func(object client.Object) []string {
			connector := object.(*kodiakv1alpha1.Connector)
			if connector.Spec.TailnetRef == nil {
				return nil
			}
			return []string{connector.Spec.TailnetRef.Name}
		}); err != nil {
		return fmt.Errorf("index Connector tailnetRef: %w", err)
	}
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &kodiakv1alpha1.Connector{}, connectorAuthKeySecretIndex,
		func(object client.Object) []string {
			connector := object.(*kodiakv1alpha1.Connector)
			if connector.Spec.AuthKeySecretRef == nil {
				return nil
			}
			return []string{connector.Spec.AuthKeySecretRef.Name}
		}); err != nil {
		return fmt.Errorf("index Connector authKeySecretRef: %w", err)
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&kodiakv1alpha1.Connector{}).
		Owns(&kodiakv1alpha1.ConnectorInstance{}).
		Watches(&kodiakv1alpha1.Tailnet{}, handler.EnqueueRequestsFromMapFunc(r.connectorsForTailnet)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.connectorsForSecret)).
		Named("connector").
		Complete(r)
}
