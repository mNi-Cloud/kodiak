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

	"github.com/mNi-Cloud/kodiak/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
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
	Scheme     *runtime.Scheme
	RESTClient rest.Interface
	Config     *rest.Config
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

			// ファイナライザーを削除する前に、最新のリソースを再取得
			var latestResource v1alpha1.Connector
			if err := r.Get(ctx, req.NamespacedName, &latestResource); err != nil {
				if errors.IsNotFound(err) {
					// すでに削除されている場合は何もしない
					logger.Info("Resource already deleted, skipping finalizer removal")
					return ctrl.Result{}, nil
				}
				logger.Error(err, "Failed to get latest resource state for finalizer removal")
				return ctrl.Result{}, err
			}

			// 最新のリソースにファイナライザーが存在する場合のみ削除を試みる
			if controllerutil.ContainsFinalizer(&latestResource, finalizer) {
				controllerutil.RemoveFinalizer(&latestResource, finalizer)
				if err := r.Update(ctx, &latestResource); err != nil {
					// UID競合や他のエラーが発生した場合、詳細をログに記録
					logger.Error(err, "Failed to remove finalizer",
						"resourceUID", latestResource.UID,
						"resourceVersion", latestResource.ResourceVersion)

					if errors.IsConflict(err) {
						// 競合が発生した場合は再試行させる
						logger.Info("Conflict detected when removing finalizer, will retry")
						return ctrl.Result{Requeue: true}, nil
					}
					return ctrl.Result{}, err
				}
				logger.Info("Successfully removed finalizer")
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
		// Return here to avoid conflict with the status update below
		return ctrl.Result{Requeue: true}, nil
	}

	// Ensure the tailscale Secret exists
	if err := r.ensureTailscaleSecret(ctx, resource.Namespace, resource.Spec.Spec.Tailscale.AuthKey); err != nil {
		logger.Error(err, "Unable to ensure tailscale Secret")
		// Update status to reflect the error
		r.setCondition(&resource, conditionTypeDeploymentReady, metav1.ConditionFalse, reasonDeploymentError, fmt.Sprintf("Secret error: %v", err))
		if updateErr := r.Status().Update(ctx, &resource); updateErr != nil {
			logger.Error(updateErr, "Unable to update Connector status after Secret error")
		}
		return ctrl.Result{}, err
	}

	// Check if Pod is running directly - this is more reliable than checking Deployment status
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
	deployResult, err := r.reconcileDeployment(ctx, &resource)
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

	// If pod is running, get Tailscale status
	if podRunning {
		status, err := r.getTailscaleStatus(ctx, &resource)
		if err != nil {
			logger.Error(err, "Failed to get Tailscale status")
			r.setCondition(&resource, conditionTypeTailscaleConnected, metav1.ConditionFalse,
				reasonTailscaleDisconnected, fmt.Sprintf("Error getting Tailscale status: %v", err))
			r.setCondition(&resource, conditionTypeReady, metav1.ConditionFalse,
				reasonTailscaleDisconnected, "Connector is not ready")
		} else if status.Connected {
			logger.Info("Setting Tailscale status to connected")
			r.setCondition(&resource, conditionTypeTailscaleConnected, metav1.ConditionTrue,
				reasonTailscaleConnected, "Tailscale node is connected")
			r.setCondition(&resource, conditionTypeReady, metav1.ConditionTrue,
				reasonTailscaleConnected, "Connector is ready")

			// 実際のステータスフィールドを設定
			resource.Status.NodeID = status.NodeID
			resource.Status.TailscaleIP = status.IP
			resource.Status.AdvertisedRoutes = status.AdvertisedRoutes

			logger.Info("Updated connector status with Tailscale information",
				"NodeID", status.NodeID,
				"TailscaleIP", status.IP,
				"AdvertisedRoutes", status.AdvertisedRoutes)
		} else {
			logger.Info("Tailscale is not connected")
			r.setCondition(&resource, conditionTypeTailscaleConnected, metav1.ConditionFalse,
				reasonTailscaleDisconnected, "Tailscale node is not connected")
			r.setCondition(&resource, conditionTypeReady, metav1.ConditionFalse,
				reasonTailscaleDisconnected, "Connector is not ready")
		}
	} else {
		logger.Info("Pod exists but not running yet")
		r.setCondition(&resource, conditionTypeTailscaleConnected, metav1.ConditionFalse,
			reasonTailscaleDisconnected, "Tailscale pod is not running yet")
		r.setCondition(&resource, conditionTypeReady, metav1.ConditionFalse,
			reasonTailscaleDisconnected, "Connector is not ready")
	}

	// Update status
	if err := r.Status().Update(ctx, &resource); err != nil {
		logger.Error(err, "Unable to update Connector status",
			"resourceVersion", resource.ResourceVersion)

		// Status update失敗時も再キューする
		return ctrl.Result{Requeue: true}, nil
	}

	logger.Info("Successfully reconciled connector and updated status")
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

	// このデプロイメントに対応するPodが実行中（Running）かどうかチェック
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

	// 実行中のPodが存在し、かつDeploymentがReadyReplicasを持っている場合は更新不要
	if podRunning && found.Status.ReadyReplicas > 0 {
		// デプロイメントが既に作成されており、Podも実行中なので何もしない
		logger.Info("Deployment already exists with running pod",
			"Name", found.Name,
			"ReadyReplicas", found.Status.ReadyReplicas)
		return ctrl.Result{}, nil
	}

	// Determine if Deployment needs updates by comparing specs
	needsUpdate := false
	if !reflect.DeepEqual(deploy.Spec.Template.Spec.Containers[0].Env, found.Spec.Template.Spec.Containers[0].Env) {
		needsUpdate = true
		logger.Info("Environment variables need update")
	}

	if !reflect.DeepEqual(deploy.Spec.Template.Spec.Containers[0].Resources, found.Spec.Template.Spec.Containers[0].Resources) {
		needsUpdate = true
		logger.Info("Resources need update")
	}

	// Only update if needed to avoid conflicts
	if needsUpdate {
		logger.Info("Updating Deployment", "Name", found.Name)
		// Create a copy to update
		updatedDeploy := found.DeepCopy()
		updatedDeploy.Spec.Template.Spec.Containers[0].Env = deploy.Spec.Template.Spec.Containers[0].Env
		updatedDeploy.Spec.Template.Spec.Containers[0].Resources = deploy.Spec.Template.Spec.Containers[0].Resources

		if err = r.Update(ctx, updatedDeploy); err != nil {
			// If conflict occurred, log but don't return error
			if errors.IsConflict(err) {
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
		// デプロイメントが少なくとも1つのレプリカを準備完了している場合は続行
		return ctrl.Result{}, nil
	}

	// 5秒後に再キューして再確認
	logger.Info("Waiting for Deployment to have ready replicas", "ReadyReplicas", found.Status.ReadyReplicas,
		"DesiredReplicas", *found.Spec.Replicas)
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
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
	logger := log.FromContext(ctx)

	// 1. Check if the Deployment exists and is ready
	deployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      cr.Name + deploymentNameSuffix,
		Namespace: cr.Namespace,
	}, deployment)

	if err != nil {
		logger.Error(err, "Failed to get deployment")
		return false
	}

	if deployment.Status.ReadyReplicas != *deployment.Spec.Replicas {
		logger.Info("Deployment not ready yet",
			"Ready", deployment.Status.ReadyReplicas,
			"Expected", *deployment.Spec.Replicas)
		return false
	}

	// 2. Check if any Pod is running for this Deployment
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(cr.Namespace),
		client.MatchingLabels(map[string]string{"app": cr.Name + "-connector"}),
	}

	if err := r.List(ctx, podList, listOpts...); err != nil {
		logger.Error(err, "Failed to list pods")
		return false
	}

	if len(podList.Items) == 0 {
		logger.Info("No pods found for connector")
		return false
	}

	// Check if at least one pod is running
	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodRunning {
			// Found at least one running pod
			logger.Info("Deployment is ready with running pod", "pod", pod.Name)
			return true
		}
	}

	logger.Info("No running pods found for connector")
	return false
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

// TailscaleStatusJSON は、tailscale status --json の出力を表します
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

// execCommandInPod はPod内でコマンドを実行し、結果を返します
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

	err = exec.Stream(remotecommand.StreamOptions{
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

		// デバッグのために出力の一部を表示
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
		// ラベルでPodが見つからない場合、デバッグ目的でラベルなしで検索
		allPods := &corev1.PodList{}
		if err := r.List(ctx, allPods, client.InNamespace(cr.Namespace)); err == nil {
			logger.Info("Checking all pods in namespace",
				"namespace", cr.Namespace,
				"pod_count", len(allPods.Items))

			// 各Podのラベルをログに出力して問題を特定
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

	// Log details about the running pod for debugging
	logger.Info("Found running pod for Tailscale connector",
		"podName", runningPod.Name,
		"podPhase", runningPod.Status.Phase,
		"podIP", runningPod.Status.PodIP,
		"containers", getContainerNames(runningPod))

	// Tailscaleコンテナ内で "tailscale status --json" コマンドを実行
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

	// デバッグのために完全なJSON出力をログに記録
	logger.Info("Raw tailscale status JSON output", "json", stdout)

	// JSONをパース
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

	// コンテナから実際のTailscale IPを取得
	tailscaleIP := ""
	if len(tsStatus.TailscaleIPs) > 0 {
		tailscaleIP = tsStatus.TailscaleIPs[0]
		logger.Info("Retrieved Tailscale IP from container", "ip", tailscaleIP)
	} else {
		// IPが取得できない場合は空白にする
		tailscaleIP = ""
		logger.Info("No Tailscale IPs found in status output - using empty string")
	}

	// Self.IDをNodeIDとして使用
	nodeID := tsStatus.Self.ID
	if nodeID == "" {
		// フォールバックとしてDNS名を使用
		nodeID = tsStatus.Self.DNSName
		if nodeID == "" {
			// それでも空ならホスト名を使用
			nodeID = tsStatus.Self.HostName
		}
	}

	// ルートが公開されているかどうかを確認
	connected := tailscaleIP != ""

	// 結果をログに出力
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
