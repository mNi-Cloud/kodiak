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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	pb "github.com/jsiebens/ionscale/pkg/gen/ionscale/v1"
	"github.com/mNi-Cloud/kodiak/api/v1alpha1"
	controlclient "github.com/mNi-Cloud/kodiak/internal/client"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	tailscaleDefaultImage = "tailscale/tailscale"
	deploymentNameSuffix  = "-ts-connector"
	caBundleVolumeName    = "tailscale-custom-ca"
	caBundleMountPath     = "/etc/tailscale/custom-ca"
	authKeySecretKey      = "TS_AUTH_KEY"

	// Condition types
	conditionTypeReady              = "Ready"
	conditionTypeDeploymentReady    = "DeploymentReady"
	conditionTypeTailscaleConnected = "TailscaleConnected"

	// Condition reasons
	reasonProcessing            = "Processing"
	reasonDeploymentCreated     = "DeploymentCreated"
	reasonDeploymentUpdated     = "DeploymentUpdated"
	reasonDeploymentError       = "DeploymentError"
	reasonTailscaleConnected    = "TailscaleConnected"
	reasonTailscaleDisconnected = "TailscaleDisconnected"
	reasonAuthKeyNotFound       = "AuthKeyNotFound"
	reasonAuthKeyNotReady       = "AuthKeyNotReady"
	reasonAuthKeySecretError    = "AuthKeySecretUnavailable"
	reasonAuthKeyNotConfigured  = "AuthKeyNotConfigured"
)

var (
	authKeyRequeueDelay = 10 * time.Second
)

type resolvedAuthKey struct {
	Env         corev1.EnvVar
	TailnetID   uint64
	TailnetName string
	AuthKeyName string
	HasTailnet  bool
}

// ConnectorReconciler reconciles a Connector object
type ConnectorReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	RESTClient       rest.Interface
	Config           *rest.Config
	ClientFactory    controlclient.ClientFactory
	IonscaleEndpoint string
	IonscaleAdminKey string
	IonscaleSkipTLS  bool
}

// +kubebuilder:rbac:groups=kodiak.mnicloud.jp,resources=connectors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kodiak.mnicloud.jp,resources=connectors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kodiak.mnicloud.jp,resources=connectors/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=get;create;patch
// +kubebuilder:rbac:groups=core,resources=pods/exec,verbs=create

func (r *ConnectorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var resource v1alpha1.Connector
	if err := r.Get(ctx, req.NamespacedName, &resource); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Unable to get Connector")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !resource.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &resource)
	}

	// Ensure finalizer
	if result, err := r.ensureFinalizer(ctx, &resource); err != nil || result.Requeue {
		return result, err
	}

	// Use explicit ControlServerUrl if specified, otherwise use controller's default endpoint
	loginServer := resource.Spec.Spec.Tailscale.ControlServerUrl
	if loginServer == "" {
		loginServer = r.IonscaleEndpoint
	}

	resolvedAuth, authKeyResult, err := r.resolveAuthKey(ctx, &resource)
	if err != nil {
		logger.Error(err, "Failed to resolve auth key for connector")
		return ctrl.Result{}, err
	}
	if authKeyResult.Requeue || authKeyResult.RequeueAfter != 0 {
		return authKeyResult, nil
	}
	if resolvedAuth == nil {
		resolvedAuth = &resolvedAuthKey{}
	}

	// Check if Pod is running
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(resource.Namespace),
		client.MatchingLabels(map[string]string{"app": resource.Name + "-connector"}),
	}

	podExists := false
	podRunning := false

	if err := r.List(ctx, podList, listOpts...); err != nil {
		logger.Error(err, "Failed to list connector pods")
	} else {
		if len(podList.Items) > 0 {
			podExists = true
			for _, pod := range podList.Items {
				if pod.Status.Phase == corev1.PodRunning {
					podRunning = true
					logger.Info("Found running connector pod", "pod", pod.Name)
					break
				}
			}
		}
	}

	// Reconcile the Deployment for Tailscale connector
	deployResult, err := r.reconcileDeployment(ctx, &resource, loginServer, resolvedAuth.Env)
	if err != nil {
		logger.Error(err, "Unable to reconcile Deployment")
		r.setCondition(&resource, conditionTypeDeploymentReady, metav1.ConditionFalse, reasonDeploymentError, err.Error())
		if updateErr := r.Status().Update(ctx, &resource); updateErr != nil {
			logger.Error(updateErr, "Unable to update Connector status")
		}
		return ctrl.Result{}, err
	}

	// If no pods exist or deployment needs requeuing, update status and requeue
	if !podExists || deployResult.Requeue {
		r.setCondition(&resource, conditionTypeDeploymentReady, metav1.ConditionFalse, reasonProcessing, "Waiting for pod to be ready")
		r.setCondition(&resource, conditionTypeReady, metav1.ConditionFalse, reasonProcessing, "Connector is not ready yet")

		// Update status even if we're just waiting
		if updateErr := r.Status().Update(ctx, &resource); updateErr != nil {
			logger.Error(updateErr, "Unable to update Connector status while waiting for pod")
		}

		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Pod exists and deployment is done reconciling
	r.setCondition(&resource, conditionTypeDeploymentReady, metav1.ConditionTrue, reasonDeploymentCreated, "Deployment ready")

	// Reconcile Tailscale status
	if err := r.reconcileTailscaleStatus(ctx, &resource, podRunning, resolvedAuth); err != nil {
		return ctrl.Result{Requeue: true}, nil
	}

	logger.Info("Successfully reconciled connector and updated status")
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

// handleDeletion handles the resource deletion process, including Tailscale logout and finalizer removal
func (r *ConnectorReconciler) handleDeletion(ctx context.Context, resource *v1alpha1.Connector) (ctrl.Result, error) {
	finalizer := "kodiak.mnicloud.jp/finalizer"
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(resource, finalizer) {
		return ctrl.Result{}, nil
	}

	// Run 'tailscale logout' to disconnect from Tailnet before cleaning up
	// Find the running pods first
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(resource.Namespace),
		client.MatchingLabels(map[string]string{"app": resource.Name + "-connector"}),
	}

	if err := r.List(ctx, podList, listOpts...); err == nil && len(podList.Items) > 0 {
		// Find a running pod
		for i := range podList.Items {
			if podList.Items[i].Status.Phase == corev1.PodRunning {
				podName := podList.Items[i].Name
				logger.Info("Running 'tailscale logout' in connector pod before deleting", "pod", podName)

				// Execute tailscale logout with --accept-risk=lose-data to force logout even if there are connection issues
				stdout, stderr, err := r.execCommandInPod(ctx, podName, resource.Namespace, "tailscale", "tailscale", "logout", "--accept-risk=lose-data")
				if err != nil {
					logger.Error(err, "Failed to run 'tailscale logout' command",
						"pod", podName,
						"stderr", stderr,
						"stdout", stdout)
					// Try alternative approach - using reset instead of logout
					logger.Info("Attempting 'tailscale reset' as fallback", "pod", podName)
					resetStdout, resetStderr, resetErr := r.execCommandInPod(ctx, podName, resource.Namespace, "tailscale", "tailscale", "reset", "--accept-risk=lose-data", "--force")
					if resetErr != nil {
						logger.Error(resetErr, "Failed to run 'tailscale reset' command",
							"pod", podName,
							"stderr", resetStderr,
							"stdout", resetStdout)
					} else {
						logger.Info("Successfully reset Tailscale state", "pod", podName)
					}
					// We don't return here, continue with deletion even if tailscale commands fail
				} else {
					logger.Info("Successfully disconnected from Tailnet", "pod", podName)
				}

				break
			}
		}
	}

	// Cleanup resources
	deploy := r.deploymentForConnector(resource, "", corev1.EnvVar{Name: "TS_AUTH_KEY", Value: ""})
	err := r.Delete(ctx, deploy)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to delete deployment")
		return ctrl.Result{}, err
	}

	// Retrieve the latest resource state before removing finalizer
	var latestResource v1alpha1.Connector
	if err := r.Get(ctx, types.NamespacedName{Name: resource.Name, Namespace: resource.Namespace}, &latestResource); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Resource already deleted, skipping finalizer removal")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get latest resource state for finalizer removal")
		return ctrl.Result{}, err
	}

	// Only attempt to remove finalizer if it exists in the latest resource
	if controllerutil.ContainsFinalizer(&latestResource, finalizer) {
		controllerutil.RemoveFinalizer(&latestResource, finalizer)
		if err := r.Update(ctx, &latestResource); err != nil {
			logger.Error(err, "Failed to remove finalizer",
				"resourceUID", latestResource.UID,
				"resourceVersion", latestResource.ResourceVersion)

			if apierrors.IsConflict(err) {
				logger.Info("Conflict detected when removing finalizer, will retry")
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
		logger.Info("Successfully removed finalizer")
	}

	return ctrl.Result{}, nil
}

func (r *ConnectorReconciler) resolveAuthKey(ctx context.Context, resource *v1alpha1.Connector) (*resolvedAuthKey, ctrl.Result, error) {
	resolved := &resolvedAuthKey{}

	if resource.Spec.Spec.Tailscale.AuthKey != "" {
		resolved.Env = corev1.EnvVar{
			Name:  "TS_AUTH_KEY",
			Value: resource.Spec.Spec.Tailscale.AuthKey,
		}
		return resolved, ctrl.Result{}, nil
	}

	if resource.Spec.Spec.Tailscale.AuthKeySecretRef != nil && resource.Spec.Spec.Tailscale.AuthKeySecretRef.Name != "" {
		selector := resource.Spec.Spec.Tailscale.AuthKeySecretRef.DeepCopy()
		if selector.Key == "" {
			selector.Key = authKeySecretKey
		}
		resolved.Env = corev1.EnvVar{
			Name: "TS_AUTH_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: selector,
			},
		}
		return resolved, ctrl.Result{}, nil
	}

	if resource.Spec.Spec.Tailscale.AuthKeyRef != nil && resource.Spec.Spec.Tailscale.AuthKeyRef.Name != "" {
		var authKey v1alpha1.AuthKey
		if err := r.Get(ctx, types.NamespacedName{Name: resource.Spec.Spec.Tailscale.AuthKeyRef.Name, Namespace: resource.Namespace}, &authKey); err != nil {
			if apierrors.IsNotFound(err) {
				r.setCondition(resource, conditionTypeReady, metav1.ConditionFalse, reasonAuthKeyNotFound, fmt.Sprintf("AuthKey %q not found", resource.Spec.Spec.Tailscale.AuthKeyRef.Name))
				if updateErr := r.Status().Update(ctx, resource); updateErr != nil {
					log.FromContext(ctx).Error(updateErr, "Unable to update connector status after auth key not found")
				}
				return nil, ctrl.Result{RequeueAfter: authKeyRequeueDelay}, nil
			}
			return nil, ctrl.Result{}, err
		}

		if !authKey.Status.Ready {
			r.setCondition(resource, conditionTypeReady, metav1.ConditionFalse, reasonAuthKeyNotReady, fmt.Sprintf("AuthKey %q is not ready", authKey.Name))
			if updateErr := r.Status().Update(ctx, resource); updateErr != nil {
				log.FromContext(ctx).Error(updateErr, "Unable to update connector status while waiting for auth key readiness")
			}
			return nil, ctrl.Result{RequeueAfter: authKeyRequeueDelay}, nil
		}

		if authKey.Status.SecretRef == nil || authKey.Status.SecretRef.Name == "" {
			r.setCondition(resource, conditionTypeReady, metav1.ConditionFalse, reasonAuthKeySecretError, fmt.Sprintf("AuthKey %q does not provide a secret reference", authKey.Name))
			if updateErr := r.Status().Update(ctx, resource); updateErr != nil {
				log.FromContext(ctx).Error(updateErr, "Unable to update connector status after missing auth key secret reference")
			}
			return nil, ctrl.Result{RequeueAfter: authKeyRequeueDelay}, nil
		}

		resolved.Env = corev1.EnvVar{
			Name: "TS_AUTH_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: authKey.Status.SecretRef.Name},
					Key:                  authKeySecretKey,
				},
			},
		}
		resolved.AuthKeyName = authKey.Name

		tailnetName := ""
		if authKey.Spec.TailnetRef.Name != "" {
			tailnetName = authKey.Spec.TailnetRef.Name
		}

		if tailnetName != "" {
			var tailnet v1alpha1.Tailnet
			if err := r.Get(ctx, types.NamespacedName{Name: tailnetName, Namespace: resource.Namespace}, &tailnet); err != nil {
				if apierrors.IsNotFound(err) {
					r.setCondition(resource, conditionTypeReady, metav1.ConditionFalse, reasonTailnetNotReady, fmt.Sprintf("Tailnet %q not found", tailnetName))
					if updateErr := r.Status().Update(ctx, resource); updateErr != nil {
						log.FromContext(ctx).Error(updateErr, "Unable to update connector status after tailnet missing")
					}
					return nil, ctrl.Result{RequeueAfter: authKeyRequeueDelay}, nil
				}
				return nil, ctrl.Result{}, err
			}

			if !tailnet.Status.Ready || tailnet.Status.TailnetID == 0 {
				r.setCondition(resource, conditionTypeReady, metav1.ConditionFalse, reasonTailnetNotReady, fmt.Sprintf("Tailnet %q is not ready", tailnet.Name))
				if updateErr := r.Status().Update(ctx, resource); updateErr != nil {
					log.FromContext(ctx).Error(updateErr, "Unable to update connector status while waiting for tailnet readiness")
				}
				return nil, ctrl.Result{RequeueAfter: authKeyRequeueDelay}, nil
			}

			resolved.TailnetID = tailnet.Status.TailnetID
			resolved.TailnetName = tailnet.Name
			resolved.HasTailnet = true
		}

		return resolved, ctrl.Result{}, nil
	}

	r.setCondition(resource, conditionTypeReady, metav1.ConditionFalse, reasonAuthKeyNotConfigured, "Provide spec.spec.tailscale.authKey, authKeySecretRef, or authKeyRef")
	if updateErr := r.Status().Update(ctx, resource); updateErr != nil {
		log.FromContext(ctx).Error(updateErr, "Unable to update connector status after missing auth key configuration")
	}
	return nil, ctrl.Result{RequeueAfter: authKeyRequeueDelay}, nil
}

// reconcileDeployment creates or updates the Deployment for the Tailscale connector
func (r *ConnectorReconciler) reconcileDeployment(ctx context.Context, cr *v1alpha1.Connector, loginServer string, authKeyEnv corev1.EnvVar) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	deploy := r.deploymentForConnector(cr, loginServer, authKeyEnv)

	// Set controller reference
	if err := controllerutil.SetControllerReference(cr, deploy, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	// Check if the Deployment already exists
	found := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: deploy.Name, Namespace: deploy.Namespace}, found)
	if err != nil && apierrors.IsNotFound(err) {
		logger.Info("Creating Deployment", "Name", deploy.Name)
		if err = r.Create(ctx, deploy); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Check if Pod corresponding to this deployment is running
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(cr.Namespace),
		client.MatchingLabels(map[string]string{"app": cr.Name + "-connector"}),
	}

	podRunning := false
	if err := r.List(ctx, podList, listOpts...); err == nil {
		for _, pod := range podList.Items {
			if pod.Status.Phase == corev1.PodRunning {
				podRunning = true
				break
			}
		}
	}

	// Log deployment status
	if podRunning && found.Status.ReadyReplicas > 0 {
		logger.Info("Deployment already exists with running pod",
			"Name", found.Name,
			"ReadyReplicas", found.Status.ReadyReplicas)
	}

	// Determine if Deployment needs updates by comparing specs
	needsUpdate := false
	desiredContainer := deploy.Spec.Template.Spec.Containers[0]
	currentContainer := found.Spec.Template.Spec.Containers[0]

	if desiredContainer.Image != currentContainer.Image {
		needsUpdate = true
		logger.Info("Container image needs update", "current", currentContainer.Image, "desired", desiredContainer.Image)
	}

	if !reflect.DeepEqual(desiredContainer.Env, currentContainer.Env) {
		needsUpdate = true
		logger.Info("Environment variables need update")
	}

	if !reflect.DeepEqual(desiredContainer.Resources, currentContainer.Resources) {
		needsUpdate = true
		logger.Info("Resources need update")
	}

	// Check if pod template annotations need update
	if !reflect.DeepEqual(deploy.Spec.Template.Annotations, found.Spec.Template.Annotations) {
		needsUpdate = true
		logger.Info("Pod template annotations need update")
	}

	// Only update if needed to avoid conflicts
	if needsUpdate {
		logger.Info("Updating Deployment", "Name", found.Name)
		// Create a copy to update
		updatedDeploy := found.DeepCopy()
		updatedContainer := &updatedDeploy.Spec.Template.Spec.Containers[0]
		updatedContainer.Image = desiredContainer.Image
		updatedContainer.Env = desiredContainer.Env
		updatedContainer.Resources = desiredContainer.Resources
		updatedDeploy.Spec.Template.Annotations = deploy.Spec.Template.Annotations

		if err = r.Update(ctx, updatedDeploy); err != nil {
			// If conflict occurred, log but don't return error
			if apierrors.IsConflict(err) {
				logger.Info("Deployment update conflict, will retry on next reconcile")
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Check if Deployment has any replicas ready
	if found.Status.ReadyReplicas > 0 {
		logger.Info("Deployment has ready replicas", "ReadyReplicas", found.Status.ReadyReplicas)
		// Continue if the deployment has at least one ready replica
		return ctrl.Result{}, nil
	}

	// Requeue after 5 seconds to check again
	logger.Info("Waiting for Deployment to have ready replicas", "ReadyReplicas", found.Status.ReadyReplicas,
		"DesiredReplicas", *found.Spec.Replicas)
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// deploymentForConnector returns a Deployment for the Tailscale connector
func (r *ConnectorReconciler) deploymentForConnector(cr *v1alpha1.Connector, loginServer string, authKeyEnv corev1.EnvVar) *appsv1.Deployment {
	version := cr.Spec.Spec.Tailscale.Version
	if version == "" {
		version = "stable"
	}

	image := fmt.Sprintf("%s:%s", tailscaleDefaultImage, version)

	// Build advertised routes string
	var routesStr string
	if len(cr.Spec.Spec.Tailscale.AdvertiseRoutes) > 0 {
		routesStr = strings.Join(cr.Spec.Spec.Tailscale.AdvertiseRoutes, ",")
	}

	// Set hostname if provided
	hostname := cr.Spec.Spec.Tailscale.Hostname
	if hostname == "" {
		hostname = cr.Name + "-connector"
	}

	// Create environment variables for the container
	env := []corev1.EnvVar{authKeyEnv}

	env = append(env, corev1.EnvVar{
		Name:  "TS_USERSPACE",
		Value: "true", // Always use USERSPACE networking mode
	})

	env = append(env, corev1.EnvVar{
		Name:  "TS_HOSTNAME",
		Value: hostname,
	})

	// Disable state storage in Kubernetes secrets
	env = append(env, corev1.EnvVar{
		Name:  "TS_KUBE_SECRET",
		Value: "",
	})

	// Add control server URL if specified
	effectiveLoginServer := cr.Spec.Spec.Tailscale.ControlServerUrl
	if effectiveLoginServer == "" {
		effectiveLoginServer = loginServer
	}

	if effectiveLoginServer != "" {
		env = append(env, corev1.EnvVar{
			Name:  "TS_EXTRA_ARGS",
			Value: "--login-server=" + effectiveLoginServer,
		})
	}

	// Add advertised routes if specified
	if routesStr != "" {
		env = append(env, corev1.EnvVar{
			Name:  "TS_ROUTES",
			Value: routesStr,
		})
	}

	// Add accept DNS config if specified
	if cr.Spec.Spec.Tailscale.AcceptDNS {
		env = append(env, corev1.EnvVar{
			Name:  "TS_ACCEPT_DNS",
			Value: "true",
		})
	}

	// Configure resource requirements if specified
	var resources corev1.ResourceRequirements
	if cr.Spec.Spec.Resources.Requests.CPU != "" || cr.Spec.Spec.Resources.Requests.Memory != "" {
		requests := corev1.ResourceList{}
		if cr.Spec.Spec.Resources.Requests.CPU != "" {
			requests[corev1.ResourceCPU] = resource.MustParse(cr.Spec.Spec.Resources.Requests.CPU)
		}
		if cr.Spec.Spec.Resources.Requests.Memory != "" {
			requests[corev1.ResourceMemory] = resource.MustParse(cr.Spec.Spec.Resources.Requests.Memory)
		}
		resources.Requests = requests
	}

	if cr.Spec.Spec.Resources.Limits.CPU != "" || cr.Spec.Spec.Resources.Limits.Memory != "" {
		limits := corev1.ResourceList{}
		if cr.Spec.Spec.Resources.Limits.CPU != "" {
			limits[corev1.ResourceCPU] = resource.MustParse(cr.Spec.Spec.Resources.Limits.CPU)
		}
		if cr.Spec.Spec.Resources.Limits.Memory != "" {
			limits[corev1.ResourceMemory] = resource.MustParse(cr.Spec.Spec.Resources.Limits.Memory)
		}
		resources.Limits = limits
	}

	// Get labels for deployment
	labels := map[string]string{
		"app": cr.Name + "-connector",
	}
	if cr.Spec.Metadata != nil && cr.Spec.Metadata.Labels != nil {
		for k, v := range cr.Spec.Metadata.Labels {
			labels[k] = v
		}
	}

	// Get annotations for pods
	var annotations map[string]string
	if cr.Spec.Metadata != nil && cr.Spec.Metadata.Annotations != nil {
		annotations = make(map[string]string)
		for k, v := range cr.Spec.Metadata.Annotations {
			annotations[k] = v
		}
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + deploymentNameSuffix,
			Namespace: cr.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: annotations,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "tailscale",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env:             env,
							Resources:       resources,
							SecurityContext: &corev1.SecurityContext{
								Capabilities: &corev1.Capabilities{
									Add: []corev1.Capability{"NET_ADMIN"},
								},
							},
						},
					},
				},
			},
		},
	}

	return deploy
}

// setCondition updates the condition in the status
func (r *ConnectorReconciler) setCondition(cr *v1alpha1.Connector, conditionType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	condition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: cr.Generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	}

	// Find and update the condition or append a new one
	for i, cond := range cr.Status.Conditions {
		if cond.Type == conditionType {
			// Don't update if nothing has changed
			if cond.Status == status && cond.Reason == reason && cond.Message == message {
				return
			}

			cr.Status.Conditions[i] = condition
			return
		}
	}

	// Condition doesn't exist, so append it
	cr.Status.Conditions = append(cr.Status.Conditions, condition)
}

// TailscaleStatus contains status information retrieved from a Tailscale pod
type TailscaleStatus struct {
	Connected        bool
	IP               string
	NodeID           string
	AdvertisedRoutes []string
}

// TailscaleStatusJSON represents the output of the 'tailscale status --json' command
type TailscaleStatusJSON struct {
	TailscaleIPs []string `json:"TailscaleIPs"`
	Self         struct {
		ID              string   `json:"ID"`
		DNSName         string   `json:"DNSName"`
		Addresses       []string `json:"Addresses"`
		OS              string   `json:"OS"`
		User            string   `json:"User"`
		HostName        string   `json:"HostName"`
		ClientVersion   string   `json:"ClientVersion"`
		UpdateAvailable bool     `json:"UpdateAvailable"`
	} `json:"Self"`
	Peer map[string]interface{} `json:"Peer"`
}

// execCommandInPod executes a command in a pod and returns the results
func (r *ConnectorReconciler) execCommandInPod(ctx context.Context, podName, namespace, containerName string, command ...string) (string, string, error) {
	req := r.RESTClient.Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec")

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return "", "", fmt.Errorf("error adding to scheme: %v", err)
	}

	parameterCodec := runtime.NewParameterCodec(scheme)
	req.VersionedParams(&corev1.PodExecOptions{
		Container: containerName,
		Command:   command,
		Stdout:    true,
		Stderr:    true,
	}, parameterCodec)

	logger := log.FromContext(ctx)
	logger.Info("Executing command in pod",
		"pod", podName,
		"container", containerName,
		"namespace", namespace,
		"command", strings.Join(command, " "))

	var stdout, stderr bytes.Buffer
	exec, err := remotecommand.NewSPDYExecutor(r.Config, "POST", req.URL())
	if err != nil {
		return "", "", fmt.Errorf("error creating executor: %v", err)
	}

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if err != nil {
		logger.Error(err, "Command execution failed",
			"stderr", stderr.String(),
			"stdout", stdout.String())
	} else {
		logger.Info("Command executed successfully",
			"stdout_length", stdout.Len(),
			"stderr_length", stderr.Len())

		// Display part of output for debugging
		outStr := stdout.String()
		if len(outStr) > 100 {
			logger.Info("Command output preview", "preview", outStr[:100]+"...")
		} else if len(outStr) > 0 {
			logger.Info("Command output preview", "preview", outStr)
		}
	}

	return stdout.String(), stderr.String(), err
}

// getTailscaleStatus gets status information from a Tailscale pod
func (r *ConnectorReconciler) getTailscaleStatus(ctx context.Context, cr *v1alpha1.Connector) (TailscaleStatus, error) {
	logger := log.FromContext(ctx)

	// First, get the pod associated with the deployment
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(cr.Namespace),
		client.MatchingLabels(map[string]string{"app": cr.Name + "-connector"}),
	}

	if err := r.List(ctx, podList, listOpts...); err != nil {
		logger.Error(err, "Failed to list connector pods")
		return TailscaleStatus{}, fmt.Errorf("failed to list connector pods: %w", err)
	}

	if len(podList.Items) == 0 {
		// If pod not found by label, search without labels for debugging
		allPods := &corev1.PodList{}
		if err := r.List(ctx, allPods, client.InNamespace(cr.Namespace)); err == nil {
			logger.Info("Checking all pods in namespace",
				"namespace", cr.Namespace,
				"pod_count", len(allPods.Items))

			// Log labels of each pod to identify the issue
			for _, pod := range allPods.Items {
				if strings.Contains(pod.Name, "connector") {
					labels := make([]string, 0, len(pod.Labels))
					for k, v := range pod.Labels {
						labels = append(labels, fmt.Sprintf("%s=%s", k, v))
					}
					logger.Info("Found potential connector pod",
						"pod", pod.Name,
						"labels", strings.Join(labels, ", "))
				}
			}
		}

		logger.Info("No connector pods found")
		return TailscaleStatus{}, fmt.Errorf("no connector pods found")
	}

	// Find a running pod
	var runningPod *corev1.Pod
	for i := range podList.Items {
		if podList.Items[i].Status.Phase == corev1.PodRunning {
			runningPod = &podList.Items[i]
			break
		}
	}

	if runningPod == nil {
		logger.Info("No running pods found for connector")
		return TailscaleStatus{
			Connected:        false,
			IP:               "",
			NodeID:           "",
			AdvertisedRoutes: []string{},
		}, nil
	}

	// Log details about the running pod
	logger.Info("Found running pod for Tailscale connector",
		"podName", runningPod.Name,
		"podPhase", runningPod.Status.Phase,
		"podIP", runningPod.Status.PodIP,
		"containers", getContainerNames(runningPod))

	// Execute "tailscale status --json" command inside the Tailscale container
	stdout, stderr, err := r.execCommandInPod(ctx, runningPod.Name, runningPod.Namespace, "tailscale", "tailscale", "status", "--json")
	if err != nil {
		logger.Error(err, "Failed to execute tailscale status command",
			"stderr", stderr,
			"stdout", stdout,
			"podName", runningPod.Name,
			"namespace", runningPod.Namespace)
		return TailscaleStatus{
			Connected:        false,
			IP:               "",
			NodeID:           "",
			AdvertisedRoutes: cr.Spec.Spec.Tailscale.AdvertiseRoutes,
		}, fmt.Errorf("failed to run tailscale status command: %w", err)
	}

	if stdout == "" {
		logger.Info("Empty output from tailscale status command")
		return TailscaleStatus{
			Connected:        false,
			IP:               "",
			NodeID:           "",
			AdvertisedRoutes: cr.Spec.Spec.Tailscale.AdvertiseRoutes,
		}, nil
	}

	// Parse the JSON
	var tsStatus TailscaleStatusJSON
	if err := json.Unmarshal([]byte(stdout), &tsStatus); err != nil {
		logger.Error(err, "Failed to parse tailscale status JSON", "output", stdout)
		return TailscaleStatus{
			Connected:        false,
			IP:               "",
			NodeID:           "",
			AdvertisedRoutes: cr.Spec.Spec.Tailscale.AdvertiseRoutes,
		}, fmt.Errorf("failed to parse tailscale status output: %w", err)
	}

	// Get the actual Tailscale IP from the container
	tailscaleIP := ""
	if len(tsStatus.TailscaleIPs) > 0 {
		tailscaleIP = tsStatus.TailscaleIPs[0]
		logger.Info("Retrieved Tailscale IP from container", "ip", tailscaleIP)
	} else {
		tailscaleIP = ""
		logger.Info("No Tailscale IPs found in status output - using empty string")
	}

	// Use Self.ID as NodeID
	nodeID := tsStatus.Self.ID
	if nodeID == "" {
		// Fallback to DNS name
		nodeID = tsStatus.Self.DNSName
		if nodeID == "" {
			// If still empty, use hostname
			nodeID = tsStatus.Self.HostName
		}
	}

	// Check if routes are advertised
	connected := tailscaleIP != ""

	// Log the results
	logger.Info("Retrieved Tailscale status from pod",
		"connected", connected,
		"tailscaleIP", tailscaleIP,
		"nodeID", nodeID,
		"advertisedRoutes", cr.Spec.Spec.Tailscale.AdvertiseRoutes)

	return TailscaleStatus{
		Connected:        connected,
		IP:               tailscaleIP,
		NodeID:           nodeID,
		AdvertisedRoutes: cr.Spec.Spec.Tailscale.AdvertiseRoutes,
	}, nil
}

// getContainerNames returns a list of container names in a pod
func getContainerNames(pod *corev1.Pod) []string {
	names := make([]string, len(pod.Spec.Containers))
	for i, container := range pod.Spec.Containers {
		names[i] = container.Name
	}
	return names
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConnectorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Connector{}).
		Owns(&appsv1.Deployment{}).
		Named("connector").
		Complete(r)
}

// ensureFinalizer ensures the finalizer is added to the resource
func (r *ConnectorReconciler) ensureFinalizer(ctx context.Context, resource *v1alpha1.Connector) (ctrl.Result, error) {
	finalizer := "kodiak.mnicloud.jp/finalizer"
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(resource, finalizer) {
		controllerutil.AddFinalizer(resource, finalizer)
		if err := r.Update(ctx, resource); err != nil {
			logger.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

// reconcileTailscaleStatus checks Tailscale status and updates conditions
func (r *ConnectorReconciler) reconcileTailscaleStatus(ctx context.Context, resource *v1alpha1.Connector, podRunning bool, resolvedAuth *resolvedAuthKey) error {
	logger := log.FromContext(ctx)

	// If pod is running, get Tailscale status
	if podRunning {
		status, err := r.getTailscaleStatus(ctx, resource)
		if err != nil {
			logger.Error(err, "Failed to get Tailscale status")
			r.setCondition(resource, conditionTypeTailscaleConnected, metav1.ConditionFalse,
				reasonTailscaleDisconnected, fmt.Sprintf("Error getting Tailscale status: %v", err))
			r.setCondition(resource, conditionTypeReady, metav1.ConditionFalse,
				reasonTailscaleDisconnected, "Connector is not ready")
		} else if status.Connected {
			logger.Info("Setting Tailscale status to connected")
			r.setCondition(resource, conditionTypeTailscaleConnected, metav1.ConditionTrue,
				reasonTailscaleConnected, "Tailscale node is connected")
			r.setCondition(resource, conditionTypeReady, metav1.ConditionTrue,
				reasonTailscaleConnected, "Connector is ready")

			// Set the actual status fields
			resource.Status.NodeID = status.NodeID
			resource.Status.TailscaleIP = status.IP
			resource.Status.AdvertisedRoutes = status.AdvertisedRoutes

			logger.Info("Updated connector status with Tailscale information",
				"NodeID", status.NodeID,
				"TailscaleIP", status.IP,
				"AdvertisedRoutes", status.AdvertisedRoutes)

			r.enableRoutesIfNeeded(ctx, resource, resolvedAuth)
		} else {
			logger.Info("Tailscale is not connected")
			r.setCondition(resource, conditionTypeTailscaleConnected, metav1.ConditionFalse,
				reasonTailscaleDisconnected, "Tailscale node is not connected")
			r.setCondition(resource, conditionTypeReady, metav1.ConditionFalse,
				reasonTailscaleDisconnected, "Connector is not ready")
		}
	} else {
		logger.Info("Pod exists but not running yet")
		r.setCondition(resource, conditionTypeTailscaleConnected, metav1.ConditionFalse,
			reasonTailscaleDisconnected, "Tailscale pod is not running yet")
		r.setCondition(resource, conditionTypeReady, metav1.ConditionFalse,
			reasonTailscaleDisconnected, "Connector is not ready")
	}

	// Update status
	if err := r.Status().Update(ctx, resource); err != nil {
		logger.Error(err, "Unable to update Connector status",
			"resourceVersion", resource.ResourceVersion)
		return err
	}

	return nil
}

func (r *ConnectorReconciler) enableRoutesIfNeeded(ctx context.Context, resource *v1alpha1.Connector, resolvedAuth *resolvedAuthKey) {
	logger := log.FromContext(ctx)

	if resolvedAuth == nil || !resolvedAuth.HasTailnet {
		return
	}

	desiredRoutes := uniqueRoutes(resource.Spec.Spec.Tailscale.AdvertiseRoutes)
	if len(desiredRoutes) == 0 {
		return
	}

	if resource.Status.NodeID == "" && resource.Status.TailscaleIP == "" {
		logger.V(1).Info("Skipping route enablement; connector does not yet have node identity")
		return
	}

	if r.IonscaleEndpoint == "" || r.IonscaleAdminKey == "" {
		logger.V(1).Info("Skipping route enablement; ionscale configuration not available")
		return
	}

	clientFactory := r.ClientFactory
	if clientFactory == nil {
		clientFactory = controlclient.DefaultClientFactory()
	}
	ctrlClient, err := clientFactory(r.IonscaleEndpoint, r.IonscaleAdminKey, r.IonscaleSkipTLS)
	if err != nil {
		logger.Error(err, "Failed to create control server client", "endpoint", r.IonscaleEndpoint)
		return
	}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	machines, err := ctrlClient.ListMachines(ctxWithTimeout, resolvedAuth.TailnetID)
	if err != nil {
		logger.Error(err, "Failed to list machines for tailnet", "tailnetID", resolvedAuth.TailnetID)
		return
	}

	machine := findConnectorMachine(machines, resource)
	if machine == nil {
		logger.Info("Unable to locate connector machine for route enablement",
			"tailnetID", resolvedAuth.TailnetID,
			"nodeID", resource.Status.NodeID,
			"tailscaleIP", resource.Status.TailscaleIP)
		return
	}

	if routesAlreadyEnabled(machine.GetEnabledRoutes(), desiredRoutes) {
		return
	}

	if _, err := ctrlClient.EnableMachineRoutes(ctxWithTimeout, machine.GetId(), desiredRoutes, false); err != nil {
		logger.Error(err, "Failed to enable machine routes",
			"machineID", machine.GetId(),
			"routes", desiredRoutes)
		return
	}

	logger.Info("Enabled advertised routes for connector",
		"machineID", machine.GetId(),
		"routes", strings.Join(desiredRoutes, ","),
		"tailnetID", resolvedAuth.TailnetID)
}

func findConnectorMachine(machines []*pb.Machine, resource *v1alpha1.Connector) *pb.Machine {
	if len(machines) == 0 {
		return nil
	}

	desiredNames := []string{}
	if resource.Status.NodeID != "" {
		desiredNames = append(desiredNames, strings.ToLower(resource.Status.NodeID))
	}
	if resource.Spec.Spec.Tailscale.Hostname != "" {
		desiredNames = append(desiredNames, strings.ToLower(resource.Spec.Spec.Tailscale.Hostname))
	}
	desiredNames = append(desiredNames, strings.ToLower(resource.Name), strings.ToLower(resource.Name+"-connector"))

	desiredIP := strings.TrimSpace(resource.Status.TailscaleIP)

	for _, machine := range machines {
		if machine == nil {
			continue
		}
		if desiredIP != "" && (machine.GetIpv4() == desiredIP || machine.GetIpv6() == desiredIP) {
			return machine
		}
		name := strings.ToLower(machine.GetName())
		for _, ident := range desiredNames {
			if ident == "" {
				continue
			}
			if name == ident || strings.HasPrefix(name, ident+".") || strings.HasPrefix(name, ident+"-") {
				return machine
			}
		}
	}

	return nil
}

func routesAlreadyEnabled(enabled, desired []string) bool {
	if len(desired) == 0 {
		return true
	}

	enabledSet := make(map[string]struct{}, len(enabled))
	for _, route := range enabled {
		if route == "" {
			continue
		}
		enabledSet[route] = struct{}{}
	}

	for _, route := range desired {
		if _, ok := enabledSet[route]; !ok {
			return false
		}
	}

	return true
}

func uniqueRoutes(routes []string) []string {
	if len(routes) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(routes))
	result := make([]string, 0, len(routes))
	for _, r := range routes {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if _, exists := seen[r]; exists {
			continue
		}
		seen[r] = struct{}{}
		result = append(result, r)
	}
	return result
}
