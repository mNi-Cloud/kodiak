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
	"net"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
)

// ControlServerReconciler reconciles a ControlServer object
type ControlServerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kodiak.mnicloud.jp,resources=controlservers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kodiak.mnicloud.jp,resources=controlservers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kodiak.mnicloud.jp,resources=controlservers/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete

func (r *ControlServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("controlserver", req.NamespacedName)

	var resource kodiakv1alpha1.ControlServer
	if err := r.Get(ctx, req.NamespacedName, &resource); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("unable to fetch ControlServer %s: %w", req.NamespacedName, err)
	}

	if !resource.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &resource)
	}

	if result, err := r.ensureFinalizer(ctx, &resource); err != nil || result.Requeue {
		return result, err
	}

	adminSecret, err := r.resolveAdminSecret(ctx, &resource)
	if err != nil {
		logger.Error(err, "unable to resolve admin secret")
		return ctrl.Result{}, err
	}

	var oidcSecret *corev1.Secret
	if resource.Spec.Config.Auth != nil && resource.Spec.Config.Auth.OIDC != nil {
		secret := &corev1.Secret{}
		secretRef := resource.Spec.Config.Auth.OIDC.ClientSecretRef
		if secretRef.Name != "" {
			if err := r.Get(ctx, types.NamespacedName{Name: secretRef.Name, Namespace: resource.Namespace}, secret); err != nil {
				if !errors.IsNotFound(err) {
					logger.Error(err, "failed to fetch OIDC client secret", "secret", secretRef.Name)
					return ctrl.Result{}, err
				}
			} else {
				oidcSecret = secret
			}
		}
	}

	portCfg := extractPortConfiguration(&resource)

	configData, err := renderControlServerConfig(&resource, adminSecret != nil, resource.Spec.Config.Auth, oidcSecret != nil)
	if err != nil {
		logger.Error(err, "failed to render ionscale configuration")
		return ctrl.Result{}, err
	}

	configHash := hashString(configData)

	if err := r.reconcileConfigMap(ctx, &resource, configData); err != nil {
		logger.Error(err, "failed to reconcile configmap")
		r.updateStatusWithError(ctx, &resource, "ConfigurationError", err)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	if err := r.reconcilePVC(ctx, &resource); err != nil {
		logger.Error(err, "failed to reconcile persistent volume claim")
		r.updateStatusWithError(ctx, &resource, "StorageError", err)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	if err := r.reconcileService(ctx, &resource, portCfg); err != nil {
		logger.Error(err, "failed to reconcile service")
		r.updateStatusWithError(ctx, &resource, "ServiceError", err)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	if err := r.reconcileDeployment(ctx, &resource, portCfg, configHash, adminSecret, oidcSecret); err != nil {
		logger.Error(err, "failed to reconcile deployment")
		r.updateStatusWithError(ctx, &resource, "DeploymentError", err)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	if err := r.refreshStatus(ctx, &resource, portCfg); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ControlServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kodiakv1alpha1.ControlServer{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&appsv1.Deployment{}).
		Named("controlserver").
		Complete(r)
}

func (r *ControlServerReconciler) ensureFinalizer(ctx context.Context, resource *kodiakv1alpha1.ControlServer) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(resource, kodiakFinalizer) {
		return ctrl.Result{}, nil
	}

	controllerutil.AddFinalizer(resource, kodiakFinalizer)
	if err := r.Update(ctx, resource); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to add finalizer: %w", err)
	}
	return ctrl.Result{Requeue: true}, nil
}

func (r *ControlServerReconciler) handleDeletion(ctx context.Context, resource *kodiakv1alpha1.ControlServer) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(resource, kodiakFinalizer) {
		return ctrl.Result{}, nil
	}

	logger := log.FromContext(ctx)

	// Best-effort cleanup: delete the managed deployment/service/configmap/pvc.
	objects := []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: deploymentName(resource), Namespace: resource.Namespace}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: serviceName(resource), Namespace: resource.Namespace}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: configMapName(resource), Namespace: resource.Namespace}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: pvcName(resource), Namespace: resource.Namespace}},
	}

	for _, obj := range objects {
		if err := r.Delete(ctx, obj); err != nil && !errors.IsNotFound(err) {
			logger.Error(err, "failed to delete managed object during finalizer cleanup", "object", obj.GetObjectKind().GroupVersionKind().Kind)
			return ctrl.Result{}, err
		}
	}

	controllerutil.RemoveFinalizer(resource, kodiakFinalizer)
	if err := r.Update(ctx, resource); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to remove finalizer: %w", err)
	}

	return ctrl.Result{}, nil
}

func (r *ControlServerReconciler) resolveAdminSecret(ctx context.Context, resource *kodiakv1alpha1.ControlServer) (*corev1.Secret, error) {
	secretName := adminSecretNameFromAnnotations(resource)
	if secretName == "" {
		return nil, nil
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: resource.Namespace}, secret); err != nil {
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("unable to get admin secret %s: %w", secretName, err)
	}

	return secret, nil
}

func (r *ControlServerReconciler) reconcileConfigMap(ctx context.Context, resource *kodiakv1alpha1.ControlServer, configData string) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName(resource),
			Namespace: resource.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		if err := controllerutil.SetControllerReference(resource, cm, r.Scheme); err != nil {
			return err
		}
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data["config.yaml"] = configData
		return nil
	})
	return err
}

func (r *ControlServerReconciler) reconcilePVC(ctx context.Context, resource *kodiakv1alpha1.ControlServer) error {
	if resource.Spec.Storage == nil {
		return nil
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName(resource),
			Namespace: resource.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, pvc, func() error {
		if err := controllerutil.SetControllerReference(resource, pvc, r.Scheme); err != nil {
			return err
		}

		if pvc.Spec.AccessModes == nil {
			pvc.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
		}

		if pvc.Spec.Resources.Requests == nil {
			pvc.Spec.Resources.Requests = corev1.ResourceList{}
		}

		size := resource.Spec.Storage.Size
		if size == "" {
			size = "10Gi"
		}
		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = apiresource.MustParse(size)
		if class := resource.Spec.Storage.StorageClassName; class != "" {
			pvc.Spec.StorageClassName = ptr.To(class)
		}

		return nil
	})

	return err
}

func (r *ControlServerReconciler) reconcileService(ctx context.Context, resource *kodiakv1alpha1.ControlServer, portCfg portConfiguration) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName(resource),
			Namespace: resource.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := controllerutil.SetControllerReference(resource, svc, r.Scheme); err != nil {
			return err
		}

		if svc.Labels == nil {
			svc.Labels = map[string]string{}
		}
		svc.Labels["app.kubernetes.io/name"] = resource.Name
		svc.Labels["app.kubernetes.io/component"] = "controlserver"

		svc.Spec.Selector = deploymentLabels(resource)
		svc.Spec.Type = corev1.ServiceTypeClusterIP

		svc.Spec.Ports = []corev1.ServicePort{
			{
				Name:       "api",
				Port:       portCfg.API,
				TargetPort: intstr.FromInt(int(portCfg.API)),
				Protocol:   corev1.ProtocolTCP,
			},
			{
				Name:       "metrics",
				Port:       portCfg.Metrics,
				TargetPort: intstr.FromInt(int(portCfg.Metrics)),
				Protocol:   corev1.ProtocolTCP,
			},
			{
				Name:       "stun",
				Port:       portCfg.Stun,
				TargetPort: intstr.FromInt(int(portCfg.Stun)),
				Protocol:   corev1.ProtocolUDP,
			},
		}

		return nil
	})

	return err
}

func (r *ControlServerReconciler) reconcileDeployment(ctx context.Context, resource *kodiakv1alpha1.ControlServer, ports portConfiguration, configHash string, adminSecret *corev1.Secret, oidcSecret *corev1.Secret) error {
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName(resource),
			Namespace: resource.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		if err := controllerutil.SetControllerReference(resource, deploy, r.Scheme); err != nil {
			return err
		}

		replicas := resource.Spec.Replicas
		if replicas == nil || *replicas < 1 {
			replicas = ptr.To(int32(1))
		}
		deploy.Spec.Replicas = replicas
		deploy.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: deploymentLabels(resource),
		}

		if deploy.Spec.Template.Labels == nil {
			deploy.Spec.Template.Labels = map[string]string{}
		}
		for k, v := range deploymentLabels(resource) {
			deploy.Spec.Template.Labels[k] = v
		}
		deploy.Spec.Template.Annotations = map[string]string{
			"kodiak.mnicloud.jp/config-hash": configHash,
		}

		volumes := []corev1.Volume{
			{
				Name: "config",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: configMapName(resource),
						},
					},
				},
			},
		}

		if resource.Spec.Storage != nil {
			volumes = append(volumes, corev1.Volume{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pvcName(resource),
					},
				},
			})
		} else {
			volumes = append(volumes, corev1.Volume{
				Name:         "data",
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			})
		}

		hasTLS := resource.Spec.Config.TLS != nil && resource.Spec.Config.TLS.CertSecretName != ""
		if hasTLS {
			volumes = append(volumes, corev1.Volume{
				Name: "tls",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: resource.Spec.Config.TLS.CertSecretName,
					},
				},
			})
		}

		deploy.Spec.Template.Spec.Volumes = volumes

		envVars := []corev1.EnvVar{}
		if adminSecret != nil {
			envVars = append(envVars, corev1.EnvVar{
				Name: ionscaleAdminKeyEnv,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: adminSecret.Name,
						},
						Key: secretKeyOrDefault(adminSecret, "systemAdminKey"),
					},
				},
			})
		}

		if resource.Spec.Config.Auth != nil && resource.Spec.Config.Auth.OIDC != nil && resource.Spec.Config.Auth.OIDC.ClientSecretRef.Name != "" {
			envVars = append(envVars, corev1.EnvVar{
				Name: oidcClientSecretEnv,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &resource.Spec.Config.Auth.OIDC.ClientSecretRef,
				},
			})
		}

		container := corev1.Container{
			Name:            "ionscale",
			Image:           resource.Spec.Image,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Args:            []string{"server", "--config", "/etc/ionscale/config.yaml"},
			Ports: []corev1.ContainerPort{
				{Name: "api", ContainerPort: ports.API, Protocol: corev1.ProtocolTCP},
				{Name: "metrics", ContainerPort: ports.Metrics, Protocol: corev1.ProtocolTCP},
				{Name: "stun", ContainerPort: ports.Stun, Protocol: corev1.ProtocolUDP},
			},
			Env: envVars,
			VolumeMounts: []corev1.VolumeMount{
				{Name: "config", MountPath: "/etc/ionscale/config.yaml", SubPath: "config.yaml", ReadOnly: true},
				{Name: "data", MountPath: "/var/lib/ionscale"},
			},
		}

		if hasTLS {
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      "tls",
				MountPath: "/etc/ionscale/tls",
				ReadOnly:  true,
			})
		}

		container.Resources = buildResourceRequirements(resource.Spec.Resources)

		deploy.Spec.Template.Spec.Containers = []corev1.Container{container}

		return nil
	})

	return err
}

func (r *ControlServerReconciler) refreshStatus(ctx context.Context, resource *kodiakv1alpha1.ControlServer, ports portConfiguration) error {
	current := resource.DeepCopy()

	deploy := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: deploymentName(resource), Namespace: resource.Namespace}, deploy)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	ready := false
	if err == nil {
		ready = deploy.Status.ReadyReplicas >= ptr.Deref(resource.Spec.Replicas, 1)
	}

	now := metav1.Now()
	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		ObservedGeneration: resource.Generation,
		LastTransitionTime: now,
		Reason:             "Progressing",
		Message:            "Waiting for deployment to become ready",
	}

	if ready {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "DeploymentReady"
		condition.Message = "Ionscale control server deployment is ready"
	}

	upsertCondition(&resource.Status.Conditions, condition)
	resource.Status.Ready = ready
	if ready {
		resource.Status.Phase = "Ready"
	} else {
		resource.Status.Phase = "Progressing"
	}

	resource.Status.Endpoint = controlServerEndpoint(resource, ports)

	if !statusesEqual(current.Status, resource.Status) {
		return r.Status().Update(ctx, resource)
	}

	return nil
}

func (r *ControlServerReconciler) updateStatusWithError(ctx context.Context, resource *kodiakv1alpha1.ControlServer, reason string, reconcileErr error) {
	current := resource.DeepCopy()
	now := metav1.Now()
	upsertCondition(&resource.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		ObservedGeneration: resource.Generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            reconcileErr.Error(),
	})
	resource.Status.Ready = false
	resource.Status.Phase = "Error"

	if !statusesEqual(current.Status, resource.Status) {
		_ = r.Status().Update(ctx, resource)
	}
}

func configMapName(resource *kodiakv1alpha1.ControlServer) string {
	return fmt.Sprintf("%s-config", resource.Name)
}

func deploymentName(resource *kodiakv1alpha1.ControlServer) string {
	return fmt.Sprintf("%s-controller", resource.Name)
}

func serviceName(resource *kodiakv1alpha1.ControlServer) string {
	return fmt.Sprintf("%s-service", resource.Name)
}

func pvcName(resource *kodiakv1alpha1.ControlServer) string {
	return fmt.Sprintf("%s-data", resource.Name)
}

func deploymentLabels(resource *kodiakv1alpha1.ControlServer) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       resource.Name,
		"app.kubernetes.io/component":  "controlserver",
		"app.kubernetes.io/managed-by": "kodiak",
	}
}

func hashString(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

type portConfiguration struct {
	API     int32
	Metrics int32
	Stun    int32
}

func extractPortConfiguration(resource *kodiakv1alpha1.ControlServer) portConfiguration {
	cfg := resource.Spec.Config
	return portConfiguration{
		API:     extractPort(cfg.ListenAddr, 8080),
		Metrics: extractPort(cfg.MetricsListenAddr, 9091),
		Stun:    extractPort(cfg.StunListenAddr, 3478),
	}
}

func extractPort(addr string, fallback int32) int32 {
	if addr == "" {
		return fallback
	}

	if strings.Contains(addr, ":") {
		normalized := addr
		if strings.HasPrefix(normalized, ":") {
			normalized = "0.0.0.0" + normalized
		}
		_, portStr, err := net.SplitHostPort(normalized)
		if err == nil {
			if portVal, convErr := strconv.Atoi(portStr); convErr == nil {
				return int32(portVal)
			}
		}
	}

	if portVal, err := strconv.Atoi(strings.TrimPrefix(addr, ":")); err == nil {
		return int32(portVal)
	}

	return fallback
}

func renderControlServerConfig(resource *kodiakv1alpha1.ControlServer, includeAdminKey bool, auth *kodiakv1alpha1.AuthConfig, includeOIDCSecret bool) (string, error) {
	spec := resource.Spec

	cfg := controlServerConfig{
		ListenAddr:        spec.Config.ListenAddr,
		MetricsListenAddr: spec.Config.MetricsListenAddr,
		StunListenAddr:    spec.Config.StunListenAddr,
	}

	if spec.Config.Database.URL != "" || spec.Config.Database.Type != "" {
		cfg.Database = &controlServerDatabaseConfig{
			Type: spec.Config.Database.Type,
			URL:  spec.Config.Database.URL,
		}
	}

	if spec.Config.TLS != nil {
		tls := &controlServerTLSConfig{
			Disable:    spec.Config.TLS.Disable,
			ForceHttps: spec.Config.TLS.ForceHTTPS,
			CertFile:   spec.Config.TLS.CertFile,
			KeyFile:    spec.Config.TLS.KeyFile,
			Acme:       spec.Config.TLS.AcmeEnabled,
			AcmeEmail:  spec.Config.TLS.AcmeEmail,
			AcmeCA:     spec.Config.TLS.AcmeCA,
			AcmePath:   spec.Config.TLS.AcmePath,
		}
		if spec.Config.TLS.CertSecretName != "" {
			if tls.CertFile == "" {
				tls.CertFile = "/etc/ionscale/tls/tls.crt"
			}
			if tls.KeyFile == "" {
				tls.KeyFile = "/etc/ionscale/tls/tls.key"
			}
		}
		cfg.TLS = tls
	}

	if spec.Config.DERP != nil {
		derp := &controlServerDERPConfig{
			Sources: spec.Config.DERP.Sources,
		}
		if spec.Config.DERP.Server != nil {
			derp.Server = &controlServerDERPServerConfig{
				Disabled:   spec.Config.DERP.Server.Disabled,
				RegionID:   spec.Config.DERP.Server.RegionID,
				RegionCode: spec.Config.DERP.Server.RegionCode,
				RegionName: spec.Config.DERP.Server.RegionName,
			}
		}
		cfg.DERP = derp
	}

	if spec.Config.DNS != nil {
		dns := &controlServerDNSConfig{
			MagicDNSSuffix: spec.Config.DNS.MagicDNSSuffix,
		}
		if spec.Config.DNS.Provider != nil {
			dns.Provider = &controlServerDNSProviderConfig{
				Name:   spec.Config.DNS.Provider.Name,
				Zone:   spec.Config.DNS.Provider.Zone,
				Config: spec.Config.DNS.Provider.Config,
			}
		}
		cfg.DNS = dns
	}

	var keys *controlServerKeysConfig
	if includeAdminKey {
		keys = &controlServerKeysConfig{
			SystemAdminKey: fmt.Sprintf("${%s}", ionscaleAdminKeyEnv),
		}
	}
	if spec.Config.Keys != nil && spec.Config.Keys.SystemAdminKey != "" {
		if keys == nil {
			keys = &controlServerKeysConfig{}
		}
		keys.SystemAdminKey = spec.Config.Keys.SystemAdminKey
	}
	if keys != nil && keys.SystemAdminKey != "" {
		cfg.Keys = keys
	}

	if spec.Config.PollNet != nil {
		cfg.PollNet = &controlServerPollNetConfig{
			KeepAliveInterval: spec.Config.PollNet.KeepAliveInterval,
		}
	}

	if spec.Config.Logging != nil {
		cfg.Logging = &controlServerLoggingConfig{
			Format: spec.Config.Logging.Format,
			Level:  spec.Config.Logging.Level,
			File:   spec.Config.Logging.File,
		}
	}

	if auth != nil {
		authCfg := &controlServerAuthConfig{}

		if auth.OIDC != nil {
			oidc := &controlServerOIDCConfig{
				Issuer:           auth.OIDC.Issuer,
				ClientID:         auth.OIDC.ClientID,
				AdditionalScopes: auth.OIDC.AdditionalScopes,
			}
			if includeOIDCSecret {
				oidc.ClientSecret = fmt.Sprintf("${%s}", oidcClientSecretEnv)
			}
			authCfg.Provider = oidc
		}

		if auth.SystemAdmins != nil {
			authCfg.SystemAdmins = &controlServerSystemAdminsConfig{
				Emails:  auth.SystemAdmins.Emails,
				Subs:    auth.SystemAdmins.Subs,
				Filters: auth.SystemAdmins.Filters,
			}
		}

		if authCfg.Provider != nil || authCfg.SystemAdmins != nil {
			cfg.Auth = authCfg
		}
	}

	publicAddr, stunAddr := computeControlServerAddresses(resource)
	cfg.PublicAddr = publicAddr
	if spec.Config.StunPublicAddr != "" {
		cfg.StunPublicAddr = spec.Config.StunPublicAddr
	} else {
		cfg.StunPublicAddr = stunAddr
	}

	content, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

func controlServerEndpoint(resource *kodiakv1alpha1.ControlServer, ports portConfiguration) string {
	scheme := "https"
	if resource.Spec.Config.TLS != nil && resource.Spec.Config.TLS.Disable {
		scheme = "http"
	}

	if resource.Spec.Config.PublicAddr != "" {
		if strings.Contains(resource.Spec.Config.PublicAddr, "://") {
			return resource.Spec.Config.PublicAddr
		}
		return fmt.Sprintf("%s://%s", scheme, resource.Spec.Config.PublicAddr)
	}

	host := fmt.Sprintf("%s.%s.svc.cluster.local", serviceName(resource), resource.Namespace)
	return fmt.Sprintf("%s://%s:%d", scheme, host, ports.API)
}

func statusesEqual(lhs, rhs kodiakv1alpha1.ControlServerStatus) bool {
	return lhs.Ready == rhs.Ready &&
		lhs.Endpoint == rhs.Endpoint &&
		lhs.Phase == rhs.Phase &&
		conditionsEqual(lhs.Conditions, rhs.Conditions)
}

func conditionsEqual(a, b []metav1.Condition) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i].Type != b[i].Type ||
			a[i].Status != b[i].Status ||
			a[i].Reason != b[i].Reason ||
			a[i].Message != b[i].Message {
			return false
		}
	}

	return true
}

func secretKeyOrDefault(secret *corev1.Secret, fallback string) string {
	if secret == nil || len(secret.Data) == 0 {
		return fallback
	}

	candidates := []string{"systemAdminKey", "system-admin-key", "token", "value"}
	for _, key := range candidates {
		if _, exists := secret.Data[key]; exists {
			return key
		}
	}

	for key := range secret.Data {
		return key
	}
	return fallback
}

func buildResourceRequirements(spec kodiakv1alpha1.ResourceRequirements) corev1.ResourceRequirements {
	requests := corev1.ResourceList{}
	if spec.Requests.CPU != "" {
		requests[corev1.ResourceCPU] = apiresource.MustParse(spec.Requests.CPU)
	}
	if spec.Requests.Memory != "" {
		requests[corev1.ResourceMemory] = apiresource.MustParse(spec.Requests.Memory)
	}

	limits := corev1.ResourceList{}
	if spec.Limits.CPU != "" {
		limits[corev1.ResourceCPU] = apiresource.MustParse(spec.Limits.CPU)
	}
	if spec.Limits.Memory != "" {
		limits[corev1.ResourceMemory] = apiresource.MustParse(spec.Limits.Memory)
	}

	return corev1.ResourceRequirements{
		Requests: requests,
		Limits:   limits,
	}
}

type controlServerConfig struct {
	ListenAddr        string                       `json:"listen_addr,omitempty" yaml:"listen_addr,omitempty"`
	MetricsListenAddr string                       `json:"metrics_listen_addr,omitempty" yaml:"metrics_listen_addr,omitempty"`
	StunListenAddr    string                       `json:"stun_listen_addr,omitempty" yaml:"stun_listen_addr,omitempty"`
	PublicAddr        string                       `json:"public_addr,omitempty" yaml:"public_addr,omitempty"`
	StunPublicAddr    string                       `json:"stun_public_addr,omitempty" yaml:"stun_public_addr,omitempty"`
	Database          *controlServerDatabaseConfig `json:"database,omitempty" yaml:"database,omitempty"`
	TLS               *controlServerTLSConfig      `json:"tls,omitempty" yaml:"tls,omitempty"`
	DERP              *controlServerDERPConfig     `json:"derp,omitempty" yaml:"derp,omitempty"`
	DNS               *controlServerDNSConfig      `json:"dns,omitempty" yaml:"dns,omitempty"`
	Auth              *controlServerAuthConfig     `json:"auth,omitempty" yaml:"auth,omitempty"`
	Keys              *controlServerKeysConfig     `json:"keys,omitempty" yaml:"keys,omitempty"`
	PollNet           *controlServerPollNetConfig  `json:"poll_net,omitempty" yaml:"poll_net,omitempty"`
	Logging           *controlServerLoggingConfig  `json:"logging,omitempty" yaml:"logging,omitempty"`
}

type controlServerDatabaseConfig struct {
	Type string `json:"type,omitempty" yaml:"type,omitempty"`
	URL  string `json:"url,omitempty" yaml:"url,omitempty"`
}

type controlServerTLSConfig struct {
	Disable    bool   `json:"disable" yaml:"disable"`
	ForceHttps bool   `json:"force_https" yaml:"force_https"`
	CertFile   string `json:"cert_file,omitempty" yaml:"cert_file,omitempty"`
	KeyFile    string `json:"key_file,omitempty" yaml:"key_file,omitempty"`
	Acme       bool   `json:"acme,omitempty" yaml:"acme,omitempty"`
	AcmeEmail  string `json:"acme_email,omitempty" yaml:"acme_email,omitempty"`
	AcmeCA     string `json:"acme_ca,omitempty" yaml:"acme_ca,omitempty"`
	AcmePath   string `json:"acme_path,omitempty" yaml:"acme_path,omitempty"`
}

type controlServerDERPConfig struct {
	Server  *controlServerDERPServerConfig `json:"server,omitempty" yaml:"server,omitempty"`
	Sources []string                       `json:"sources,omitempty" yaml:"sources,omitempty"`
}

type controlServerDERPServerConfig struct {
	Disabled   bool   `json:"disabled,omitempty" yaml:"disabled,omitempty"`
	RegionID   int    `json:"region_id,omitempty" yaml:"region_id,omitempty"`
	RegionCode string `json:"region_code,omitempty" yaml:"region_code,omitempty"`
	RegionName string `json:"region_name,omitempty" yaml:"region_name,omitempty"`
}

type controlServerDNSConfig struct {
	MagicDNSSuffix string                          `json:"magic_dns_suffix,omitempty" yaml:"magic_dns_suffix,omitempty"`
	Provider       *controlServerDNSProviderConfig `json:"provider,omitempty" yaml:"provider,omitempty"`
}

type controlServerDNSProviderConfig struct {
	Name   string            `json:"name,omitempty" yaml:"name,omitempty"`
	Zone   string            `json:"zone,omitempty" yaml:"zone,omitempty"`
	Config map[string]string `json:"config,omitempty" yaml:"config,omitempty"`
}

type controlServerAuthConfig struct {
	Provider     *controlServerOIDCConfig         `json:"provider,omitempty" yaml:"provider,omitempty"`
	SystemAdmins *controlServerSystemAdminsConfig `json:"system_admins,omitempty" yaml:"system_admins,omitempty"`
}

type controlServerOIDCConfig struct {
	Issuer           string   `json:"issuer,omitempty" yaml:"issuer,omitempty"`
	ClientID         string   `json:"client_id,omitempty" yaml:"client_id,omitempty"`
	ClientSecret     string   `json:"client_secret,omitempty" yaml:"client_secret,omitempty"`
	AdditionalScopes []string `json:"additional_scopes,omitempty" yaml:"additional_scopes,omitempty"`
}

type controlServerKeysConfig struct {
	SystemAdminKey string `json:"system_admin_key,omitempty" yaml:"system_admin_key,omitempty"`
}

type controlServerSystemAdminsConfig struct {
	Emails  []string `json:"emails,omitempty" yaml:"emails,omitempty"`
	Subs    []string `json:"subs,omitempty" yaml:"subs,omitempty"`
	Filters []string `json:"filters,omitempty" yaml:"filters,omitempty"`
}

type controlServerPollNetConfig struct {
	KeepAliveInterval string `json:"keep_alive_interval,omitempty" yaml:"keep_alive_interval,omitempty"`
}

type controlServerLoggingConfig struct {
	Format string `json:"format,omitempty" yaml:"format,omitempty"`
	Level  string `json:"level,omitempty" yaml:"level,omitempty"`
	File   string `json:"file,omitempty" yaml:"file,omitempty"`
}

func computeControlServerAddresses(resource *kodiakv1alpha1.ControlServer) (string, string) {
	spec := resource.Spec

	listenPort := parseAddressPort(spec.Config.ListenAddr, 8080)
	stunPort := parseAddressPort(spec.Config.StunListenAddr, 3478)

	publicAddr := sanitizePublicAddr(spec.Config.PublicAddr, spec.Config.TLS, resource.Namespace, serviceName(resource), listenPort)

	hostForStun := trimScheme(publicAddr)
	if idx := strings.Index(hostForStun, ":"); idx != -1 {
		hostForStun = hostForStun[:idx]
	}

	stunAddr := fmt.Sprintf("%s:%d", hostForStun, stunPort)
	return publicAddr, stunAddr
}

func sanitizePublicAddr(candidate string, tlsConfig *kodiakv1alpha1.TLSConfig, namespace, svcName string, listenPort int32) string {
	if candidate == "" {
		host := fmt.Sprintf("%s.%s.svc.cluster.local", svcName, namespace)
		return fmt.Sprintf("%s:%d", host, listenPort)
	}

	scheme := "https"
	if tlsConfig != nil && tlsConfig.Disable {
		scheme = "http"
	}

	addr := trimScheme(candidate)

	if _, _, err := net.SplitHostPort(addr); err != nil {
		defaultPort := 443
		if scheme == "http" {
			defaultPort = 80
		}
		addr = fmt.Sprintf("%s:%d", addr, defaultPort)
	}

	return addr
}

func trimScheme(value string) string {
	if strings.HasPrefix(value, "http://") {
		return strings.TrimPrefix(value, "http://")
	}
	if strings.HasPrefix(value, "https://") {
		return strings.TrimPrefix(value, "https://")
	}
	return value
}

func parseAddressPort(addr string, fallback int32) int32 {
	if addr == "" {
		return fallback
	}

	if strings.Contains(addr, ":") {
		normalized := addr
		if strings.HasPrefix(normalized, ":") {
			normalized = "0.0.0.0" + normalized
		}
		_, portStr, err := net.SplitHostPort(normalized)
		if err == nil {
			if portValue, convErr := strconv.Atoi(portStr); convErr == nil {
				return int32(portValue)
			}
		}
	}

	if portValue, err := strconv.Atoi(strings.TrimPrefix(addr, ":")); err == nil {
		return int32(portValue)
	}

	return fallback
}
