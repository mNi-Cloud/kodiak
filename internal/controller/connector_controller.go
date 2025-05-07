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
	"reflect"
	"strings"
	"time"

	"github.com/mNi-Cloud/kodiak/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	tailscaleDefaultImage = "tailscale/tailscale"
	deploymentNameSuffix  = "-ts-connector"
	secretName            = "tailscale-auth"

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
)

// ConnectorReconciler reconciles a Connector object
type ConnectorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ConnectorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	finalizer := "kodiak.mnicloud.jp/finalizer"
	logger := log.FromContext(ctx)

	var resource v1alpha1.Connector
	if err := r.Get(ctx, req.NamespacedName, &resource); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Unable to get Connector")
		return ctrl.Result{}, err
	}

	if !resource.ObjectMeta.DeletionTimestamp.IsZero() {
		// Handle finalizer and cleanup
		if controllerutil.ContainsFinalizer(&resource, finalizer) {
			// Cleanup resources
			deploy := r.deploymentForConnector(&resource)
			err := r.Delete(ctx, deploy)
			if err != nil && !errors.IsNotFound(err) {
				logger.Error(err, "Failed to delete deployment")
				return ctrl.Result{}, err
			}

			// Remove finalizer once cleanup is done
			controllerutil.RemoveFinalizer(&resource, finalizer)
			if err := r.Update(ctx, &resource); err != nil {
				logger.Error(err, "Failed to remove finalizer")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure finalizer
	if !controllerutil.ContainsFinalizer(&resource, finalizer) {
		controllerutil.AddFinalizer(&resource, finalizer)
		if err := r.Update(ctx, &resource); err != nil {
			logger.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
	}

	// Ensure the tailscale Secret exists
	if err := r.ensureTailscaleSecret(ctx, resource.Namespace, resource.Spec.Spec.Tailscale.AuthKey); err != nil {
		logger.Error(err, "Unable to ensure tailscale Secret")
		return ctrl.Result{}, err
	}

	// Reconcile the Deployment for Tailscale connector
	deployResult, err := r.reconcileDeployment(ctx, &resource)
	if err != nil {
		logger.Error(err, "Unable to reconcile Deployment")
		r.setCondition(&resource, conditionTypeDeploymentReady, metav1.ConditionFalse, reasonDeploymentError, err.Error())
		if updateErr := r.Status().Update(ctx, &resource); updateErr != nil {
			logger.Error(updateErr, "Unable to update Connector status")
		}
		return ctrl.Result{}, err
	}

	if deployResult.Requeue {
		logger.Info("Deployment not ready yet, requeuing")
		return deployResult, nil
	}

	r.setCondition(&resource, conditionTypeDeploymentReady, metav1.ConditionTrue, reasonDeploymentCreated, "Deployment created successfully")

	// Check Tailscale connection status and update CR status accordingly
	// In a real implementation, we would want to query Tailscale's API or the running pod
	// For now, we'll just set it to connected if the deployment is ready
	if r.isDeploymentReady(ctx, &resource) {
		r.setCondition(&resource, conditionTypeTailscaleConnected, metav1.ConditionTrue,
			reasonTailscaleConnected, "Tailscale node is connected")
		r.setCondition(&resource, conditionTypeReady, metav1.ConditionTrue,
			reasonTailscaleConnected, "Connector is ready")

		// In a real implementation, these values would come from querying Tailscale status
		// For demonstration purposes, we'll just set them to placeholder values
		resource.Status.NodeID = "tsnode-connector-" + resource.Name
		resource.Status.TailscaleIP = "100.x.y.z"
		resource.Status.AdvertisedRoutes = resource.Spec.Spec.Tailscale.AdvertiseRoutes
	} else {
		r.setCondition(&resource, conditionTypeTailscaleConnected, metav1.ConditionFalse,
			reasonTailscaleDisconnected, "Tailscale node is not connected")
		r.setCondition(&resource, conditionTypeReady, metav1.ConditionFalse,
			reasonTailscaleDisconnected, "Connector is not ready")
	}

	// Update status
	if err := r.Status().Update(ctx, &resource); err != nil {
		logger.Error(err, "Unable to update Connector status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

// ensureTailscaleSecret ensures that the tailscale Secret exists in the specified namespace
func (r *ConnectorReconciler) ensureTailscaleSecret(ctx context.Context, namespace string, authKey string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"TS_AUTH_KEY": authKey,
		},
	}

	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, &corev1.Secret{})
	if err != nil {
		if errors.IsNotFound(err) {
			// Create the secret
			err = r.Create(ctx, secret)
			if err != nil {
				return fmt.Errorf("failed to create tailscale Secret: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to check if tailscale Secret exists: %w", err)
	}

	// Secret exists, update it
	err = r.Update(ctx, secret)
	if err != nil {
		return fmt.Errorf("failed to update tailscale Secret: %w", err)
	}
	return nil
}

// reconcileDeployment creates or updates the Deployment for the Tailscale connector
func (r *ConnectorReconciler) reconcileDeployment(ctx context.Context, cr *v1alpha1.Connector) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	deploy := r.deploymentForConnector(cr)

	// Set controller reference
	if err := controllerutil.SetControllerReference(cr, deploy, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	// Check if the Deployment already exists
	found := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: deploy.Name, Namespace: deploy.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		logger.Info("Creating Deployment", "Name", deploy.Name)
		if err = r.Create(ctx, deploy); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Update Deployment if needed
	if !reflect.DeepEqual(deploy.Spec.Template.Spec.Containers[0].Env, found.Spec.Template.Spec.Containers[0].Env) ||
		!reflect.DeepEqual(deploy.Spec.Template.Spec.Containers[0].Resources, found.Spec.Template.Spec.Containers[0].Resources) {
		found.Spec.Template.Spec.Containers[0].Env = deploy.Spec.Template.Spec.Containers[0].Env
		found.Spec.Template.Spec.Containers[0].Resources = deploy.Spec.Template.Spec.Containers[0].Resources
		logger.Info("Updating Deployment", "Name", found.Name)
		if err = r.Update(ctx, found); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

// deploymentForConnector returns a Deployment for the Tailscale connector
func (r *ConnectorReconciler) deploymentForConnector(cr *v1alpha1.Connector) *appsv1.Deployment {
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
	env := []corev1.EnvVar{
		{
			Name: "TS_AUTH_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secretName,
					},
					Key: "TS_AUTH_KEY",
				},
			},
		},
		// Add POD_NAME and POD_UID for debugging Events
		{
			Name: "POD_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
		{
			Name: "POD_UID",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.uid",
				},
			},
		},
		{
			Name:  "TS_USERSPACE",
			Value: "true", // Always use USERSPACE networking mode
		},
		{
			Name:  "TS_HOSTNAME",
			Value: hostname,
		},
	}

	// Add control server URL if specified
	if cr.Spec.Spec.Tailscale.ControlServerUrl != "" {
		env = append(env, corev1.EnvVar{
			Name:  "TS_EXTRA_ARGS",
			Value: "--login-server=" + cr.Spec.Spec.Tailscale.ControlServerUrl,
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

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + deploymentNameSuffix,
			Namespace: cr.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: pointer.Int32(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "tailscale-connector",
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

// isDeploymentReady checks if the Deployment for the connector is ready
func (r *ConnectorReconciler) isDeploymentReady(ctx context.Context, cr *v1alpha1.Connector) bool {
	deployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      cr.Name + deploymentNameSuffix,
		Namespace: cr.Namespace,
	}, deployment)

	if err != nil {
		return false
	}

	return deployment.Status.ReadyReplicas == *deployment.Spec.Replicas
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

// SetupWithManager sets up the controller with the Manager.
func (r *ConnectorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Connector{}).
		Owns(&appsv1.Deployment{}).
		Named("connector").
		Complete(r)
}
