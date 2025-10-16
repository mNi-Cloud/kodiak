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
	"fmt"
	"sort"
	"strings"
	"time"

	pb "github.com/jsiebens/ionscale/pkg/gen/ionscale/v1"
	controlclient "github.com/mNi-Cloud/kodiak/internal/client"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
)

const (
	authKeyConditionReady     = "Ready"
	reasonMissingTailnetRef   = "MissingTailnetRef"
	reasonTailnetNotFound     = "TailnetNotFound"
	reasonTailnetNotReady     = "TailnetNotReady"
	reasonMissingAdminToken   = "MissingAdminKeySecret"
	reasonInvalidSpec         = "InvalidSpec"
	reasonAuthKeyCreated      = "AuthKeyCreated"
	reasonAuthKeyRotated      = "AuthKeyRotated"
	reasonAuthKeySynced       = "AuthKeySynced"
	reasonAuthKeyError        = "AuthKeyError"
	authKeySpecHashAnnotation = "kodiak.mnicloud.jp/authkey-spec-hash"
	authKeySecretDataKey      = "TS_AUTH_KEY"
	defaultAuthKeyRequeue     = 6 * time.Hour
	dependentNotReadyRequeue  = 30 * time.Second
	rotationRetryRequeue      = 30 * time.Second

	phaseReady   = "Ready"
	phasePending = "Pending"
	phaseError   = "Error"
)

// AuthKeyReconciler reconciles a AuthKey object
type AuthKeyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kodiak.mnicloud.jp,resources=authkeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kodiak.mnicloud.jp,resources=authkeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kodiak.mnicloud.jp,resources=authkeys/finalizers,verbs=update

// nolint:gocyclo // the reconciliation flow is complex and already factored into helpers where practical
func (r *AuthKeyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("authkey", req.NamespacedName)

	var authKey kodiakv1alpha1.AuthKey
	if err := r.Get(ctx, req.NamespacedName, &authKey); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("unable to fetch AuthKey %s: %w", req.NamespacedName, err)
	}

	if !authKey.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &authKey)
	}

	if result, err := r.ensureFinalizer(ctx, &authKey); err != nil || result.Requeue {
		return result, err
	}

	if authKey.Spec.TailnetRef.Name == "" {
		logger.Info("tailnet reference not specified")
		if err := r.setPendingStatus(ctx, &authKey, reasonMissingTailnetRef, "spec.tailnetRef.name must be provided"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: dependentNotReadyRequeue}, nil
	}

	tailnet := &kodiakv1alpha1.Tailnet{}
	if err := r.Get(ctx, types.NamespacedName{Name: authKey.Spec.TailnetRef.Name, Namespace: authKey.Namespace}, tailnet); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("referenced tailnet not found", "tailnet", authKey.Spec.TailnetRef.Name)
			if err := r.setPendingStatus(ctx, &authKey, reasonTailnetNotFound,
				fmt.Sprintf("tailnet %q not found", authKey.Spec.TailnetRef.Name)); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: dependentNotReadyRequeue}, nil
		}
		return ctrl.Result{}, fmt.Errorf("unable to fetch tailnet %s: %w", authKey.Spec.TailnetRef.Name, err)
	}

	if !tailnet.Status.Ready || tailnet.Status.TailnetID == 0 {
		logger.Info("tailnet not ready", "tailnet", tailnet.Name)
		if err := r.setPendingStatus(ctx, &authKey, reasonTailnetNotReady,
			fmt.Sprintf("tailnet %q is not ready", tailnet.Name)); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: dependentNotReadyRequeue}, nil
	}

	if tailnet.Spec.ControlServerRef.Name == "" {
		logger.Info("tailnet missing control server reference", "tailnet", tailnet.Name)
		if err := r.setPendingStatus(ctx, &authKey, reasonControlServerNotFound,
			fmt.Sprintf("tailnet %q does not reference a control server", tailnet.Name)); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: dependentNotReadyRequeue}, nil
	}

	controlServer := &kodiakv1alpha1.ControlServer{}
	if err := r.Get(ctx, types.NamespacedName{Name: tailnet.Spec.ControlServerRef.Name, Namespace: authKey.Namespace}, controlServer); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("control server not found", "controlServer", tailnet.Spec.ControlServerRef.Name)
			if err := r.setPendingStatus(ctx, &authKey, reasonControlServerNotFound,
				fmt.Sprintf("control server %q not found", tailnet.Spec.ControlServerRef.Name)); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: dependentNotReadyRequeue}, nil
		}
		return ctrl.Result{}, fmt.Errorf("unable to fetch control server %s: %w", tailnet.Spec.ControlServerRef.Name, err)
	}

	adminKey, secretName, err := readAdminKey(ctx, r.Client, controlServer)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("system admin secret not found", "secret", secretName)
			if err := r.setPendingStatus(ctx, &authKey, reasonMissingAdminToken,
				fmt.Sprintf("admin key secret %q not found", secretName)); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: dependentNotReadyRequeue}, nil
		}
		return ctrl.Result{}, fmt.Errorf("unable to read admin token: %w", err)
	}
	if adminKey == "" {
		logger.Info("admin token is empty", "secret", secretName)
		if err := r.setPendingStatus(ctx, &authKey, reasonMissingAdminToken,
			fmt.Sprintf("admin key secret %q does not contain a value", secretName)); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: dependentNotReadyRequeue}, nil
	}

	endpoint, skipVerify := deriveControlServerEndpoint(controlServer)
	ctrlClient, err := controlclient.NewControlServerClient(endpoint, adminKey, skipVerify)
	if err != nil {
		logger.Error(err, "failed to create control server client", "endpoint", endpoint)
		if err := r.setErrorStatus(ctx, &authKey, reasonAuthKeyError, fmt.Errorf("unable to construct control server client: %w", err)); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: dependentNotReadyRequeue}, nil
	}

	desiredSecretName := authKey.Spec.SecretName
	if desiredSecretName == "" {
		desiredSecretName = defaultAuthKeySecretName(authKey.Name)
	}

	specHash := hashAuthKeySpec(authKey.Spec)
	currentHash := authKey.GetAnnotations()[authKeySpecHashAnnotation]

	if authKey.Status.KeyID != 0 && currentHash != specHash {
		logger.Info("auth key spec changed, rotating key")
		if err := r.rotateAuthKey(ctx, &authKey, ctrlClient, desiredSecretName, specHash); err != nil {
			if err := r.setErrorStatus(ctx, &authKey, reasonAuthKeyError, err); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: rotationRetryRequeue}, nil
		}
	}

	if authKey.Status.KeyID == 0 {
		return r.provisionNewAuthKey(ctx, &authKey, ctrlClient, tailnet.Status.TailnetID, desiredSecretName, specHash)
	}

	remoteKey, err := r.findRemoteAuthKey(ctx, ctrlClient, tailnet.Status.TailnetID, authKey.Status.KeyID)
	if err != nil {
		logger.Error(err, "failed to list auth keys")
		if err := r.setErrorStatus(ctx, &authKey, reasonAuthKeyError, err); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: dependentNotReadyRequeue}, nil
	}

	if remoteKey == nil {
		logger.Info("remote auth key no longer present, scheduling rotation")
		if err := r.resetAuthKeyStatus(ctx, &authKey, reasonAuthKeyRotated, "Remote auth key missing; a new key will be generated"); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.deleteSecretIfExists(ctx, authKey.Namespace, desiredSecretName); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.ensureSpecHashAnnotation(ctx, &authKey, specHash); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: dependentNotReadyRequeue}, nil
	}

	if err := r.ensureAuthKeySecretPresent(ctx, &authKey, desiredSecretName); err != nil {
		logger.Error(err, "auth key secret missing, rotating key")
		if err := r.rotateAuthKey(ctx, &authKey, ctrlClient, desiredSecretName, specHash); err != nil {
			if err := r.setErrorStatus(ctx, &authKey, reasonAuthKeyError, err); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: rotationRetryRequeue}, nil
		}
		return ctrl.Result{RequeueAfter: dependentNotReadyRequeue}, nil
	}

	if err := r.setReadyStatus(ctx, &authKey, remoteKey, desiredSecretName, reasonAuthKeySynced, "Auth key is valid and ready"); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.ensureSpecHashAnnotation(ctx, &authKey, specHash); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: defaultAuthKeyRequeue}, nil
}

func (r *AuthKeyReconciler) ensureFinalizer(ctx context.Context, resource *kodiakv1alpha1.AuthKey) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(resource, kodiakFinalizer) {
		return ctrl.Result{}, nil
	}

	controllerutil.AddFinalizer(resource, kodiakFinalizer)
	if err := r.Update(ctx, resource); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to add finalizer: %w", err)
	}
	return ctrl.Result{Requeue: true}, nil
}

func (r *AuthKeyReconciler) handleDeletion(ctx context.Context, resource *kodiakv1alpha1.AuthKey) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(resource, kodiakFinalizer) {
		return ctrl.Result{}, nil
	}

	logger := log.FromContext(ctx).WithValues("authkey", resource.Name)

	if resource.Status.KeyID != 0 && resource.Spec.TailnetRef.Name != "" {
		tailnet := &kodiakv1alpha1.Tailnet{}
		err := r.Get(ctx, types.NamespacedName{Name: resource.Spec.TailnetRef.Name, Namespace: resource.Namespace}, tailnet)
		if err == nil && tailnet.Spec.ControlServerRef.Name != "" {
			controlServer := &kodiakv1alpha1.ControlServer{}
			if err := r.Get(ctx, types.NamespacedName{Name: tailnet.Spec.ControlServerRef.Name, Namespace: resource.Namespace}, controlServer); err == nil {
				adminKey, _, tokenErr := readAdminKey(ctx, r.Client, controlServer)
				if tokenErr == nil && adminKey != "" {
					endpoint, skipVerify := deriveControlServerEndpoint(controlServer)
					ctrlClient, clientErr := controlclient.NewControlServerClient(endpoint, adminKey, skipVerify)
					if clientErr == nil {
						if err := ctrlClient.DeleteAuthKey(ctx, resource.Status.KeyID); err != nil && !isConnectNotFound(err) {
							logger.Error(err, "failed to delete auth key on control server")
							return ctrl.Result{RequeueAfter: rotationRetryRequeue}, nil
						}
					}
				}
			}
		}
	}

	if resource.Spec.SecretName != "" {
		_ = r.deleteSecretIfExists(ctx, resource.Namespace, resource.Spec.SecretName)
	} else {
		_ = r.deleteSecretIfExists(ctx, resource.Namespace, defaultAuthKeySecretName(resource.Name))
	}

	controllerutil.RemoveFinalizer(resource, kodiakFinalizer)
	if err := r.Update(ctx, resource); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to remove finalizer: %w", err)
	}

	return ctrl.Result{}, nil
}

func (r *AuthKeyReconciler) provisionNewAuthKey(ctx context.Context, resource *kodiakv1alpha1.AuthKey, client *controlclient.ControlServerClient, tailnetID uint64, secretName, specHash string) (ctrl.Result, error) {
	var expiryDuration time.Duration
	if resource.Spec.Expiry != "" {
		parsed, err := time.ParseDuration(resource.Spec.Expiry)
		if err != nil {
			if err := r.setErrorStatus(ctx, resource, reasonInvalidSpec, fmt.Errorf("invalid expiry duration: %w", err)); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		expiryDuration = parsed
	}

	createdKey, value, err := client.CreateAuthKey(ctx, tailnetID, resource.Spec.Ephemeral, expiryDuration, append([]string(nil), resource.Spec.Tags...), resource.Spec.PreAuthorized)
	if err != nil {
		if err := r.setErrorStatus(ctx, resource, reasonAuthKeyError, err); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: dependentNotReadyRequeue}, nil
	}

	if err := r.createOrUpdateSecret(ctx, resource, secretName, value); err != nil {
		if err := r.setErrorStatus(ctx, resource, reasonAuthKeyError, err); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: dependentNotReadyRequeue}, nil
	}

	if err := r.ensureSpecHashAnnotation(ctx, resource, specHash); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.setReadyStatus(ctx, resource, createdKey, secretName, reasonAuthKeyCreated, "Auth key created on control server"); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: defaultAuthKeyRequeue}, nil
}

func (r *AuthKeyReconciler) findRemoteAuthKey(ctx context.Context, client *controlclient.ControlServerClient, tailnetID, keyID uint64) (*pb.AuthKey, error) {
	keys, err := client.ListAuthKeys(ctx, tailnetID)
	if err != nil {
		return nil, err
	}

	for _, key := range keys {
		if key.GetId() == keyID {
			return key, nil
		}
	}
	return nil, nil
}

func (r *AuthKeyReconciler) ensureAuthKeySecretPresent(ctx context.Context, resource *kodiakv1alpha1.AuthKey, secretName string) error {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: resource.Namespace}, secret); err != nil {
		return err
	}

	if len(secret.Data[authKeySecretDataKey]) == 0 {
		return fmt.Errorf("secret %q missing key %q", secretName, authKeySecretDataKey)
	}

	return nil
}

func (r *AuthKeyReconciler) setReadyStatus(ctx context.Context, resource *kodiakv1alpha1.AuthKey, remote *pb.AuthKey, secretName, reason, message string) error {
	current := resource.DeepCopy()

	now := metav1.Now()
	upsertCondition(&resource.Status.Conditions, metav1.Condition{
		Type:               authKeyConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: resource.Generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	})
	resource.Status.Ready = true
	resource.Status.Phase = phaseReady
	resource.Status.KeyID = remote.GetId()
	resource.Status.SecretRef = &corev1.LocalObjectReference{Name: secretName}
	resource.Status.CreatedAt = convertTimestamp(remote.GetCreatedAt())
	resource.Status.ExpiresAt = convertTimestamp(remote.GetExpiresAt())

	if equality.Semantic.DeepEqual(current.Status, resource.Status) {
		return nil
	}
	return r.Status().Update(ctx, resource)
}

func (r *AuthKeyReconciler) setPendingStatus(ctx context.Context, resource *kodiakv1alpha1.AuthKey, reason, message string) error {
	current := resource.DeepCopy()

	now := metav1.Now()
	upsertCondition(&resource.Status.Conditions, metav1.Condition{
		Type:               authKeyConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: resource.Generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	})
	resource.Status.Ready = false
	resource.Status.Phase = phasePending

	if equality.Semantic.DeepEqual(current.Status, resource.Status) {
		return nil
	}
	return r.Status().Update(ctx, resource)
}

func (r *AuthKeyReconciler) setErrorStatus(ctx context.Context, resource *kodiakv1alpha1.AuthKey, reason string, reconcileErr error) error {
	current := resource.DeepCopy()

	message := reasonAuthKeyError
	if reconcileErr != nil {
		message = reconcileErr.Error()
	}

	now := metav1.Now()
	upsertCondition(&resource.Status.Conditions, metav1.Condition{
		Type:               authKeyConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: resource.Generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	})
	resource.Status.Ready = false
	resource.Status.Phase = phaseError

	if equality.Semantic.DeepEqual(current.Status, resource.Status) {
		return nil
	}
	return r.Status().Update(ctx, resource)
}

func (r *AuthKeyReconciler) rotateAuthKey(ctx context.Context, resource *kodiakv1alpha1.AuthKey, client *controlclient.ControlServerClient, secretName, specHash string) error {
	if resource.Status.KeyID != 0 {
		if err := client.DeleteAuthKey(ctx, resource.Status.KeyID); err != nil && !isConnectNotFound(err) {
			return fmt.Errorf("failed to delete existing auth key %d: %w", resource.Status.KeyID, err)
		}
	}

	if err := r.deleteSecretIfExists(ctx, resource.Namespace, secretName); err != nil {
		return err
	}

	if err := r.resetAuthKeyStatus(ctx, resource, reasonAuthKeyRotated, "Auth key rotated; provisioning a new key"); err != nil {
		return err
	}

	if err := r.ensureSpecHashAnnotation(ctx, resource, specHash); err != nil {
		return err
	}

	resource.Status.KeyID = 0

	return nil
}

func (r *AuthKeyReconciler) resetAuthKeyStatus(ctx context.Context, resource *kodiakv1alpha1.AuthKey, reason, message string) error {
	current := resource.DeepCopy()

	now := metav1.Now()
	upsertCondition(&resource.Status.Conditions, metav1.Condition{
		Type:               authKeyConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: resource.Generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	})
	resource.Status.Ready = false
	resource.Status.Phase = phasePending
	resource.Status.KeyID = 0
	resource.Status.SecretRef = nil
	resource.Status.CreatedAt = nil
	resource.Status.ExpiresAt = nil

	if equality.Semantic.DeepEqual(current.Status, resource.Status) {
		return nil
	}
	return r.Status().Update(ctx, resource)
}

func (r *AuthKeyReconciler) deleteSecretIfExists(ctx context.Context, namespace, name string) error {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	return client.IgnoreNotFound(r.Delete(ctx, secret))
}

func (r *AuthKeyReconciler) createOrUpdateSecret(ctx context.Context, resource *kodiakv1alpha1.AuthKey, secretName, value string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: resource.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if err := controllerutil.SetControllerReference(resource, secret, r.Scheme); err != nil {
			return err
		}
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Type = corev1.SecretTypeOpaque
		secret.Data[authKeySecretDataKey] = []byte(strings.TrimSpace(value))
		return nil
	})
	return err
}

func (r *AuthKeyReconciler) ensureSpecHashAnnotation(ctx context.Context, resource *kodiakv1alpha1.AuthKey, hash string) error {
	if resource.Annotations != nil && resource.Annotations[authKeySpecHashAnnotation] == hash {
		return nil
	}

	original := resource.DeepCopy()
	if resource.Annotations == nil {
		resource.Annotations = map[string]string{}
	}
	resource.Annotations[authKeySpecHashAnnotation] = hash
	return r.Patch(ctx, resource, client.MergeFrom(original))
}

func hashAuthKeySpec(spec kodiakv1alpha1.AuthKeySpec) string {
	tags := append([]string(nil), spec.Tags...)
	sort.Strings(tags)
	data := fmt.Sprintf("%s|%t|%s|%t|%s|%v", spec.TailnetRef.Name, spec.Ephemeral, spec.Expiry, spec.PreAuthorized, spec.SecretName, tags)
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func defaultAuthKeySecretName(name string) string {
	return fmt.Sprintf("authkey-%s", name)
}

func convertTimestamp(ts *timestamppb.Timestamp) *metav1.Time {
	if ts == nil {
		return nil
	}
	timeValue := ts.AsTime()
	if timeValue.IsZero() {
		return nil
	}
	result := metav1.NewTime(timeValue)
	return &result
}

// SetupWithManager sets up the controller with the Manager.
func (r *AuthKeyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kodiakv1alpha1.AuthKey{}).
		Named("authkey").
		Complete(r)
}
