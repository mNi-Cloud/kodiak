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
	"strconv"
	"strings"
	"time"

	pb "github.com/jsiebens/ionscale/pkg/gen/ionscale/v1"
	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
	controlclient "github.com/mNi-Cloud/kodiak/internal/client"
	"google.golang.org/protobuf/types/known/timestamppb"
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
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	authKeySecretDataKey = "TS_AUTH_KEY"

	reasonAuthKeyReady                = "AuthKeyReady"
	reasonAuthKeyIssuing              = "AuthKeyIssuing"
	reasonAuthKeyRotating             = "AuthKeyRotating"
	reasonAuthKeyError                = "AuthKeyError"
	reasonAuthKeyTailnetNotReady      = "TailnetNotReady"
	reasonAuthKeyConfigurationMissing = "ConfigurationMissing"

	defaultAuthKeyRequeue = time.Minute
	authKeyRetryRequeue   = 10 * time.Second
)

// AuthKeyReconciler reconciles user and manual enrollment credentials.
type AuthKeyReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	ClientFactory    controlclient.ClientFactory
	IonscaleEndpoint string
	IonscaleAdminKey string
	IonscaleSkipTLS  bool
}

func (r *AuthKeyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("authkey", req.NamespacedName)

	var resource kodiakv1alpha1.AuthKey
	if err := r.Get(ctx, req.NamespacedName, &resource); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !resource.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &resource)
	}
	if result, err := ensureFinalizer(ctx, r.Client, &resource); err != nil || result.Requeue {
		return result, err
	}

	if r.IonscaleEndpoint == "" || r.IonscaleAdminKey == "" {
		return r.notReady(ctx, &resource, reasonAuthKeyConfigurationMissing, "Ionscale API endpoint or admin key is not configured")
	}

	var tailnet kodiakv1alpha1.Tailnet
	if err := r.Get(ctx, types.NamespacedName{Namespace: resource.Namespace, Name: resource.Spec.TailnetRef.Name}, &tailnet); err != nil {
		if apierrors.IsNotFound(err) {
			return r.notReady(ctx, &resource, reasonAuthKeyTailnetNotReady, fmt.Sprintf("Tailnet %q does not exist", resource.Spec.TailnetRef.Name))
		}
		return ctrl.Result{}, err
	}
	if !meta.IsStatusConditionTrue(tailnet.Status.Conditions, kodiakv1alpha1.TailnetConditionReady) || tailnet.Status.TailnetID == "" {
		return r.notReady(ctx, &resource, reasonAuthKeyTailnetNotReady, fmt.Sprintf("Tailnet %q is not ready", tailnet.Name))
	}
	tailnetID, err := strconv.ParseUint(tailnet.Status.TailnetID, 10, 64)
	if err != nil {
		return r.notReady(ctx, &resource, reasonAuthKeyTailnetNotReady, fmt.Sprintf("Tailnet %q has invalid status.tailnetID", tailnet.Name))
	}

	ctrlClient, err := r.controlClient()
	if err != nil {
		return r.fail(ctx, &resource, err)
	}

	if resource.Status.RetiringKeyID != "" {
		if err := deleteRemoteAuthKey(ctx, ctrlClient, resource.Status.RetiringKeyID); err != nil {
			return r.fail(ctx, &resource, fmt.Errorf("delete retiring auth key: %w", err))
		}
		original := resource.DeepCopy()
		resource.Status.RetiringKeyID = ""
		if err := r.Status().Patch(ctx, &resource, client.MergeFrom(original)); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	secretName := resource.Spec.SecretName
	if secretName == "" {
		secretName = defaultAuthKeySecretName(resource.Name)
	}

	currentID, err := parseOptionalID(resource.Status.KeyID)
	if err != nil {
		return r.fail(ctx, &resource, err)
	}

	remote, err := findRemoteAuthKey(ctx, ctrlClient, tailnetID, currentID)
	if err != nil {
		return r.fail(ctx, &resource, err)
	}
	secretReady := r.authKeySecretReady(ctx, resource.Namespace, secretName)
	rotationRequested := resource.Status.IssuedRotationNonce != resource.Spec.RotationNonce
	remoteExpired := remote != nil && remote.GetExpiresAt() != nil && !remote.GetExpiresAt().AsTime().After(time.Now())

	if currentID == 0 || remote == nil || !secretReady || rotationRequested || remoteExpired {
		reason := reasonAuthKeyIssuing
		if currentID != 0 {
			reason = reasonAuthKeyRotating
		}
		if err := r.setCondition(ctx, &resource, metav1.ConditionFalse, reason, "Issuing a replacement enrollment key"); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("issuing auth key", "rotation", currentID != 0)
		return r.issue(ctx, &resource, ctrlClient, tailnetID, currentID, secretName)
	}

	if err := r.setReady(ctx, &resource, remote, secretName); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: defaultAuthKeyRequeue}, nil
}

func (r *AuthKeyReconciler) controlClient() (controlclient.ControlServerClientInterface, error) {
	factory := r.ClientFactory
	if factory == nil {
		factory = controlclient.DefaultClientFactory()
	}
	client, err := factory(r.IonscaleEndpoint, r.IonscaleAdminKey, r.IonscaleSkipTLS)
	if err != nil {
		return nil, fmt.Errorf("construct Ionscale client: %w", err)
	}
	return client, nil
}

func (r *AuthKeyReconciler) issue(
	ctx context.Context,
	resource *kodiakv1alpha1.AuthKey,
	ctrlClient controlclient.ControlServerClientInterface,
	tailnetID, oldID uint64,
	secretName string,
) (ctrl.Result, error) {
	var expiry time.Duration
	if resource.Spec.Expiry != nil {
		expiry = resource.Spec.Expiry.Duration
	}

	remote, value, err := ctrlClient.CreateAuthKey(
		ctx,
		tailnetID,
		resource.Spec.Ephemeral,
		expiry,
		append([]string(nil), resource.Spec.Tags...),
		resource.Spec.PreAuthorized,
	)
	if err != nil {
		return r.fail(ctx, resource, fmt.Errorf("create auth key: %w", err))
	}

	if err := r.writeSecret(ctx, resource, secretName, value); err != nil {
		_ = ctrlClient.DeleteAuthKey(ctx, remote.GetId())
		return r.fail(ctx, resource, fmt.Errorf("store auth key: %w", err))
	}

	original := resource.DeepCopy()
	resource.Status.ObservedGeneration = resource.Generation
	resource.Status.KeyID = strconv.FormatUint(remote.GetId(), 10)
	resource.Status.SecretRef = &corev1.LocalObjectReference{Name: secretName}
	resource.Status.CreatedAt = convertTimestamp(remote.GetCreatedAt())
	resource.Status.ExpiresAt = convertTimestamp(remote.GetExpiresAt())
	resource.Status.IssuedRotationNonce = resource.Spec.RotationNonce
	if oldID != 0 && oldID != remote.GetId() {
		resource.Status.RetiringKeyID = strconv.FormatUint(oldID, 10)
	}
	upsertCondition(&resource.Status.Conditions, metav1.Condition{
		Type:               kodiakv1alpha1.AuthKeyConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: resource.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reasonAuthKeyReady,
		Message:            "Enrollment key is ready",
	})
	if err := r.Status().Patch(ctx, resource, client.MergeFrom(original)); err != nil {
		_ = ctrlClient.DeleteAuthKey(ctx, remote.GetId())
		return ctrl.Result{}, err
	}

	if resource.Status.RetiringKeyID != "" {
		return ctrl.Result{Requeue: true}, nil
	}
	return ctrl.Result{RequeueAfter: defaultAuthKeyRequeue}, nil
}

func (r *AuthKeyReconciler) handleDeletion(ctx context.Context, resource *kodiakv1alpha1.AuthKey) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(resource, kodiakFinalizer) {
		return ctrl.Result{}, nil
	}
	hasRemoteKeys := resource.Status.KeyID != "" || resource.Status.RetiringKeyID != ""
	if hasRemoteKeys && (r.IonscaleEndpoint == "" || r.IonscaleAdminKey == "") {
		return ctrl.Result{RequeueAfter: authKeyRetryRequeue}, nil
	}
	if hasRemoteKeys {
		ctrlClient, err := r.controlClient()
		if err != nil {
			return ctrl.Result{RequeueAfter: authKeyRetryRequeue}, nil
		}
		for _, raw := range []string{resource.Status.KeyID, resource.Status.RetiringKeyID} {
			if err := deleteRemoteAuthKey(ctx, ctrlClient, raw); err != nil {
				return ctrl.Result{RequeueAfter: authKeyRetryRequeue}, nil
			}
		}
	}

	secretName := resource.Spec.SecretName
	if secretName == "" {
		secretName = defaultAuthKeySecretName(resource.Name)
	}
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: resource.Namespace, Name: secretName}, &secret); err == nil {
		if err := r.Delete(ctx, &secret); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	} else if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	original := resource.DeepCopy()
	controllerutil.RemoveFinalizer(resource, kodiakFinalizer)
	return ctrl.Result{}, r.Patch(ctx, resource, client.MergeFrom(original))
}

func (r *AuthKeyReconciler) authKeySecretReady(ctx context.Context, namespace, name string) bool {
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &secret); err != nil {
		return false
	}
	return len(secret.Data[authKeySecretDataKey]) != 0
}

func (r *AuthKeyReconciler) writeSecret(ctx context.Context, owner *kodiakv1alpha1.AuthKey, name, value string) error {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: owner.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if err := controllerutil.SetControllerReference(owner, secret, r.Scheme); err != nil {
			return err
		}
		secret.Type = corev1.SecretTypeOpaque
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data[authKeySecretDataKey] = []byte(strings.TrimSpace(value))
		return nil
	})
	return err
}

func (r *AuthKeyReconciler) setReady(ctx context.Context, resource *kodiakv1alpha1.AuthKey, remote *pb.AuthKey, secretName string) error {
	original := resource.DeepCopy()
	resource.Status.ObservedGeneration = resource.Generation
	resource.Status.SecretRef = &corev1.LocalObjectReference{Name: secretName}
	resource.Status.CreatedAt = convertTimestamp(remote.GetCreatedAt())
	resource.Status.ExpiresAt = convertTimestamp(remote.GetExpiresAt())
	upsertCondition(&resource.Status.Conditions, metav1.Condition{
		Type:               kodiakv1alpha1.AuthKeyConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: resource.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reasonAuthKeyReady,
		Message:            "Enrollment key is ready",
	})
	if equality.Semantic.DeepEqual(original.Status, resource.Status) {
		return nil
	}
	return r.Status().Patch(ctx, resource, client.MergeFrom(original))
}

func (r *AuthKeyReconciler) setCondition(ctx context.Context, resource *kodiakv1alpha1.AuthKey, status metav1.ConditionStatus, reason, message string) error {
	original := resource.DeepCopy()
	resource.Status.ObservedGeneration = resource.Generation
	upsertCondition(&resource.Status.Conditions, metav1.Condition{
		Type:               kodiakv1alpha1.AuthKeyConditionReady,
		Status:             status,
		ObservedGeneration: resource.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	})
	if equality.Semantic.DeepEqual(original.Status, resource.Status) {
		return nil
	}
	return r.Status().Patch(ctx, resource, client.MergeFrom(original))
}

func (r *AuthKeyReconciler) notReady(
	ctx context.Context,
	resource *kodiakv1alpha1.AuthKey,
	reason, message string,
) (ctrl.Result, error) {
	if err := r.setCondition(ctx, resource, metav1.ConditionFalse, reason, message); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: authKeyRetryRequeue}, nil
}

func (r *AuthKeyReconciler) fail(ctx context.Context, resource *kodiakv1alpha1.AuthKey, reconcileErr error) (ctrl.Result, error) {
	if err := r.setCondition(ctx, resource, metav1.ConditionFalse, reasonAuthKeyError, reconcileErr.Error()); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: authKeyRetryRequeue}, nil
}

func findRemoteAuthKey(ctx context.Context, ctrlClient controlclient.ControlServerClientInterface, tailnetID, keyID uint64) (*pb.AuthKey, error) {
	if keyID == 0 {
		return nil, nil
	}
	keys, err := ctrlClient.ListAuthKeys(ctx, tailnetID)
	if err != nil {
		return nil, fmt.Errorf("list auth keys: %w", err)
	}
	for _, key := range keys {
		if key.GetId() == keyID {
			return key, nil
		}
	}
	return nil, nil
}

func deleteRemoteAuthKey(ctx context.Context, ctrlClient controlclient.ControlServerClientInterface, raw string) error {
	if raw == "" {
		return nil
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid auth key ID %q: %w", raw, err)
	}
	if err := ctrlClient.DeleteAuthKey(ctx, id); err != nil && !isConnectNotFound(err) {
		return err
	}
	return nil
}

func parseOptionalID(raw string) (uint64, error) {
	if raw == "" {
		return 0, nil
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid remote ID %q: %w", raw, err)
	}
	return id, nil
}

func defaultAuthKeySecretName(name string) string {
	return "authkey-" + name
}

func convertTimestamp(ts *timestamppb.Timestamp) *metav1.Time {
	if ts == nil {
		return nil
	}
	value := ts.AsTime()
	if value.IsZero() {
		return nil
	}
	result := metav1.NewTime(value)
	return &result
}

func ensureFinalizer(ctx context.Context, kubeClient client.Client, resource client.Object) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(resource, kodiakFinalizer) {
		return ctrl.Result{}, nil
	}
	original := resource.DeepCopyObject().(client.Object)
	controllerutil.AddFinalizer(resource, kodiakFinalizer)
	if err := kubeClient.Patch(ctx, resource, client.MergeFrom(original)); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{Requeue: true}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AuthKeyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kodiakv1alpha1.AuthKey{}).
		Owns(&corev1.Secret{}).
		Named("authkey").
		Complete(r)
}
