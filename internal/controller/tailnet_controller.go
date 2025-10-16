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
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bufbuild/connect-go"
	pb "github.com/jsiebens/ionscale/pkg/gen/ionscale/v1"
	controlclient "github.com/mNi-Cloud/kodiak/internal/client"
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
	tailnetConditionReady           = "Ready"
	reasonMissingControlServer      = "MissingControlServerRef"
	reasonControlServerNotFound     = "ControlServerNotFound"
	reasonControlServerNotReady     = "ControlServerNotReady"
	reasonMissingAdminKey           = "MissingAdminKeySecret"
	reasonTailnetCreated            = "TailnetCreated"
	reasonTailnetUpdated            = "TailnetUpdated"
	reasonTailnetSynced             = "TailnetSynced"
	reasonTailnetError              = "TailnetError"
	defaultTailnetRequeue           = time.Minute
	controlServerNotReadyRequeue    = 15 * time.Second
	missingDependencyRequeue        = 30 * time.Second
	machineListFailureRequeue       = time.Minute
	finalizerCleanupRetryRequeue    = 10 * time.Second
	defaultMachineListFallbackCount = 0
)

// TailnetReconciler reconciles a Tailnet object
type TailnetReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kodiak.mnicloud.jp,resources=tailnets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kodiak.mnicloud.jp,resources=tailnets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kodiak.mnicloud.jp,resources=tailnets/finalizers,verbs=update

func (r *TailnetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("tailnet", req.NamespacedName)

	var tailnet kodiakv1alpha1.Tailnet
	if err := r.Get(ctx, req.NamespacedName, &tailnet); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("unable to fetch Tailnet %s: %w", req.NamespacedName, err)
	}

	if !tailnet.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &tailnet)
	}

	if result, err := r.ensureFinalizer(ctx, &tailnet); err != nil || result.Requeue {
		return result, err
	}

	if tailnet.Spec.ControlServerRef.Name == "" {
		logger.Info("control server reference not specified on tailnet")
		if err := r.setPendingStatus(ctx, &tailnet, reasonMissingControlServer, "spec.controlServerRef.name must be provided"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: missingDependencyRequeue}, nil
	}

	controlServer := &kodiakv1alpha1.ControlServer{}
	if err := r.Get(ctx, types.NamespacedName{Name: tailnet.Spec.ControlServerRef.Name, Namespace: tailnet.Namespace}, controlServer); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("referenced control server not found", "controlServer", tailnet.Spec.ControlServerRef.Name)
			if err := r.setPendingStatus(ctx, &tailnet, reasonControlServerNotFound,
				fmt.Sprintf("control server %q not found", tailnet.Spec.ControlServerRef.Name)); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: missingDependencyRequeue}, nil
		}
		return ctrl.Result{}, fmt.Errorf("unable to fetch control server %s: %w", tailnet.Spec.ControlServerRef.Name, err)
	}

	if !controlServer.Status.Ready {
		logger.Info("control server not ready yet", "controlServer", controlServer.Name)
		if err := r.setPendingStatus(ctx, &tailnet, reasonControlServerNotReady,
			fmt.Sprintf("control server %q is not ready", controlServer.Name)); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: controlServerNotReadyRequeue}, nil
	}

	adminKey, secretName, err := readAdminKey(ctx, r.Client, controlServer)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("system admin secret not found", "secret", secretName)
			if err := r.setPendingStatus(ctx, &tailnet, reasonMissingAdminKey,
				fmt.Sprintf("admin key secret %q not found", secretName)); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: missingDependencyRequeue}, nil
		}
		return ctrl.Result{}, fmt.Errorf("unable to read admin token: %w", err)
	}
	if adminKey == "" {
		logger.Info("admin key secret empty", "secret", secretName)
		if err := r.setPendingStatus(ctx, &tailnet, reasonMissingAdminKey,
			fmt.Sprintf("admin key secret %q does not contain a value", secretName)); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: missingDependencyRequeue}, nil
	}

	endpoint, skipVerify := deriveControlServerEndpoint(controlServer)
	ctrlClient, err := controlclient.NewControlServerClient(endpoint, adminKey, skipVerify)
	if err != nil {
		logger.Error(err, "failed to create control server client", "endpoint", endpoint, "skipVerify", skipVerify)
		if err := r.setErrorStatus(ctx, &tailnet, reasonTailnetError, fmt.Errorf("unable to construct control server client: %w", err)); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: missingDependencyRequeue}, nil
	}

	remoteTailnet, syncReason, syncMessage, err := r.syncTailnet(ctx, &tailnet, ctrlClient)
	if err != nil {
		logger.Error(err, "failed to synchronise tailnet state")
		if err := r.setErrorStatus(ctx, &tailnet, reasonTailnetError, err); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: missingDependencyRequeue}, nil
	}

	machineCount, err := r.fetchMachineCount(ctx, ctrlClient, remoteTailnet.GetId())
	if err != nil {
		logger.Error(err, "failed to list machines for tailnet", "tailnetID", remoteTailnet.GetId())
		machineCount = defaultMachineListFallbackCount
	}

	if err := r.setReadyStatus(ctx, &tailnet, remoteTailnet.GetId(), machineCount, syncReason, syncMessage); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: defaultTailnetRequeue}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TailnetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kodiakv1alpha1.Tailnet{}).
		Named("tailnet").
		Complete(r)
}

func (r *TailnetReconciler) ensureFinalizer(ctx context.Context, resource *kodiakv1alpha1.Tailnet) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(resource, kodiakFinalizer) {
		return ctrl.Result{}, nil
	}

	controllerutil.AddFinalizer(resource, kodiakFinalizer)
	if err := r.Update(ctx, resource); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to add finalizer: %w", err)
	}
	return ctrl.Result{Requeue: true}, nil
}

func (r *TailnetReconciler) handleDeletion(ctx context.Context, resource *kodiakv1alpha1.Tailnet) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(resource, kodiakFinalizer) {
		return ctrl.Result{}, nil
	}

	logger := log.FromContext(ctx).WithValues("tailnet", resource.Name)

	if resource.Status.TailnetID != 0 && resource.Spec.ControlServerRef.Name != "" {
		controlServer := &kodiakv1alpha1.ControlServer{}
		err := r.Get(ctx, types.NamespacedName{Name: resource.Spec.ControlServerRef.Name, Namespace: resource.Namespace}, controlServer)
		if err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("unable to fetch control server during finalization: %w", err)
		}

		if err == nil {
			if err := r.deleteRemoteTailnet(ctx, controlServer, resource.Status.TailnetID); err != nil {
				logger.Error(err, "failed to delete tailnet from control server, will retry")
				return ctrl.Result{RequeueAfter: finalizerCleanupRetryRequeue}, nil
			}
		}
	}

	controllerutil.RemoveFinalizer(resource, kodiakFinalizer)
	if err := r.Update(ctx, resource); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to remove finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *TailnetReconciler) deleteRemoteTailnet(ctx context.Context, controlServer *kodiakv1alpha1.ControlServer, tailnetID uint64) error {
	adminKey, _, err := readAdminKey(ctx, r.Client, controlServer)
	if err != nil {
		return fmt.Errorf("unable to read admin token for deletion: %w", err)
	}
	if adminKey == "" {
		return errors.New("admin key is empty, cannot delete remote tailnet")
	}

	endpoint, skipVerify := deriveControlServerEndpoint(controlServer)
	ctrlClient, err := controlclient.NewControlServerClient(endpoint, adminKey, skipVerify)
	if err != nil {
		return fmt.Errorf("failed to create control server client for deletion: %w", err)
	}

	if err := ctrlClient.DeleteTailnet(ctx, tailnetID, true); err != nil && !isConnectNotFound(err) {
		return fmt.Errorf("failed to delete tailnet %d: %w", tailnetID, err)
	}
	return nil
}

func (r *TailnetReconciler) syncTailnet(ctx context.Context, resource *kodiakv1alpha1.Tailnet, client *controlclient.ControlServerClient) (*pb.Tailnet, string, string, error) {
	dnsConfig := buildDNSConfig(resource.Spec.DNSConfig)
	createReq := buildCreateTailnetRequest(resource.Spec, dnsConfig)

	var remote *pb.Tailnet
	tailnetID := resource.Status.TailnetID

	if tailnetID != 0 {
		t, err := client.GetTailnet(ctx, tailnetID)
		if err != nil {
			if isConnectNotFound(err) {
				tailnetID = 0
			} else {
				return nil, "", "", fmt.Errorf("failed to fetch tailnet %d: %w", tailnetID, err)
			}
		} else {
			remote = t
		}
	}

	if remote == nil {
		tailnets, err := client.ListTailnets(ctx)
		if err != nil {
			return nil, "", "", fmt.Errorf("unable to list tailnets: %w", err)
		}
		for _, candidate := range tailnets {
			if candidate.GetName() == resource.Spec.Name {
				remote = candidate
				tailnetID = candidate.GetId()
				break
			}
		}
	}

	if remote == nil {
		created, err := client.CreateTailnet(ctx, createReq)
		if err != nil {
			return nil, "", "", fmt.Errorf("failed to create tailnet %q: %w", resource.Spec.Name, err)
		}
		return created, reasonTailnetCreated, "Tailnet created on control server", nil
	}

	updateReq := buildUpdateTailnetRequest(tailnetID, resource.Spec, dnsConfig)
	if tailnetNeedsUpdate(remote, resource.Spec, dnsConfig) {
		updated, err := client.UpdateTailnet(ctx, updateReq)
		if err != nil {
			return nil, "", "", fmt.Errorf("failed to update tailnet %d: %w", tailnetID, err)
		}
		return updated, reasonTailnetUpdated, "Tailnet configuration updated on control server", nil
	}

	return remote, reasonTailnetSynced, "Tailnet configuration already up to date", nil
}

func (r *TailnetReconciler) fetchMachineCount(ctx context.Context, client *controlclient.ControlServerClient, tailnetID uint64) (int, error) {
	machines, err := client.ListMachines(ctx, tailnetID)
	if err != nil {
		return 0, fmt.Errorf("failed to list machines for tailnet %d: %w", tailnetID, err)
	}
	return len(machines), nil
}

func (r *TailnetReconciler) setReadyStatus(ctx context.Context, resource *kodiakv1alpha1.Tailnet, tailnetID uint64, machineCount int, reason, message string) error {
	current := resource.DeepCopy()

	now := metav1.Now()
	upsertCondition(&resource.Status.Conditions, metav1.Condition{
		Type:               tailnetConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: resource.Generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	})
	resource.Status.Ready = true
	resource.Status.Phase = "Ready"
	resource.Status.TailnetID = tailnetID
	resource.Status.MachineCount = machineCount
	syncTime := metav1.NewTime(time.Now().UTC())
	resource.Status.LastSyncTime = &syncTime

	if equality.Semantic.DeepEqual(current.Status, resource.Status) {
		return nil
	}
	return r.Status().Update(ctx, resource)
}

func (r *TailnetReconciler) setPendingStatus(ctx context.Context, resource *kodiakv1alpha1.Tailnet, reason, message string) error {
	current := resource.DeepCopy()

	now := metav1.Now()
	upsertCondition(&resource.Status.Conditions, metav1.Condition{
		Type:               tailnetConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: resource.Generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	})
	resource.Status.Ready = false
	resource.Status.Phase = "Pending"

	if equality.Semantic.DeepEqual(current.Status, resource.Status) {
		return nil
	}
	return r.Status().Update(ctx, resource)
}

func (r *TailnetReconciler) setErrorStatus(ctx context.Context, resource *kodiakv1alpha1.Tailnet, reason string, reconcileErr error) error {
	current := resource.DeepCopy()

	now := metav1.Now()
	message := reasonTailnetError
	if reconcileErr != nil {
		message = reconcileErr.Error()
	}
	upsertCondition(&resource.Status.Conditions, metav1.Condition{
		Type:               tailnetConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: resource.Generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	})
	resource.Status.Ready = false
	resource.Status.Phase = "Error"

	if equality.Semantic.DeepEqual(current.Status, resource.Status) {
		return nil
	}
	return r.Status().Update(ctx, resource)
}

func buildDNSConfig(spec *kodiakv1alpha1.TailnetDNSConfig) *pb.DNSConfig {
	if spec == nil {
		return nil
	}

	cfg := &pb.DNSConfig{
		MagicDns:      spec.MagicDNS,
		Nameservers:   append([]string(nil), spec.Nameservers...),
		SearchDomains: append([]string(nil), spec.SearchDomains...),
	}

	if len(spec.Domains) > 0 {
		cfg.Routes = make(map[string]*pb.Routes)
		for _, domain := range spec.Domains {
			cfg.Routes[domain] = &pb.Routes{}
		}
	}

	return cfg
}

func buildCreateTailnetRequest(spec kodiakv1alpha1.TailnetSpec, dnsConfig *pb.DNSConfig) *pb.CreateTailnetRequest {
	return &pb.CreateTailnetRequest{
		Name:                        spec.Name,
		IamPolicy:                   spec.IAMPolicy,
		AclPolicy:                   spec.ACLPolicy,
		DnsConfig:                   dnsConfig,
		ServiceCollectionEnabled:    spec.ServiceCollectionEnabled,
		FileSharingEnabled:          spec.FileSharingEnabled,
		SshEnabled:                  spec.SSHEnabled,
		MachineAuthorizationEnabled: spec.MachineAuthorizationEnabled,
	}
}

func buildUpdateTailnetRequest(tailnetID uint64, spec kodiakv1alpha1.TailnetSpec, dnsConfig *pb.DNSConfig) *pb.UpdateTailnetRequest {
	return &pb.UpdateTailnetRequest{
		TailnetId:                   tailnetID,
		IamPolicy:                   spec.IAMPolicy,
		AclPolicy:                   spec.ACLPolicy,
		DnsConfig:                   dnsConfig,
		ServiceCollectionEnabled:    spec.ServiceCollectionEnabled,
		FileSharingEnabled:          spec.FileSharingEnabled,
		SshEnabled:                  spec.SSHEnabled,
		MachineAuthorizationEnabled: spec.MachineAuthorizationEnabled,
	}
}

func tailnetNeedsUpdate(remote *pb.Tailnet, spec kodiakv1alpha1.TailnetSpec, dnsConfig *pb.DNSConfig) bool {
	if remote.GetIamPolicy() != spec.IAMPolicy {
		return true
	}
	if remote.GetAclPolicy() != spec.ACLPolicy {
		return true
	}
	if remote.GetServiceCollectionEnabled() != spec.ServiceCollectionEnabled {
		return true
	}
	if remote.GetFileSharingEnabled() != spec.FileSharingEnabled {
		return true
	}
	if remote.GetSshEnabled() != spec.SSHEnabled {
		return true
	}
	if remote.GetMachineAuthorizationEnabled() != spec.MachineAuthorizationEnabled {
		return true
	}
	if !dnsConfigsEqual(remote.GetDnsConfig(), dnsConfig) {
		return true
	}
	return false
}

func dnsConfigsEqual(a, b *pb.DNSConfig) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.GetMagicDns() != b.GetMagicDns() {
		return false
	}
	if a.GetMagicDnsSuffix() != b.GetMagicDnsSuffix() {
		return false
	}
	if !stringSlicesEqual(a.GetNameservers(), b.GetNameservers()) {
		return false
	}
	if !stringSlicesEqual(a.GetSearchDomains(), b.GetSearchDomains()) {
		return false
	}
	return len(a.GetRoutes()) == len(b.GetRoutes())
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ca := append([]string(nil), a...)
	cb := append([]string(nil), b...)
	sort.Strings(ca)
	sort.Strings(cb)
	for i := range ca {
		if ca[i] != cb[i] {
			return false
		}
	}
	return true
}

func deriveControlServerEndpoint(controlServer *kodiakv1alpha1.ControlServer) (string, bool) {
	ports := extractPortConfiguration(controlServer)
	endpoint := controlServer.Status.Endpoint
	if endpoint == "" {
		endpoint = controlServerEndpoint(controlServer, ports)
	}

	disableTLS := controlServer.Spec.Config.TLS != nil && controlServer.Spec.Config.TLS.Disable
	skipVerify := false

	if !disableTLS && strings.HasPrefix(endpoint, "https://") {
		host := endpoint[len("https://"):]
		if idx := strings.Index(host, "/"); idx >= 0 {
			host = host[:idx]
		}
		if strings.Contains(host, ".svc.") || strings.Contains(host, ".cluster.local") {
			skipVerify = true
		}
		if controlServer.Spec.Config.TLS != nil && controlServer.Spec.Config.TLS.CertSecretName != "" {
			skipVerify = true
		}
	}

	if disableTLS {
		skipVerify = false
	}

	return endpoint, skipVerify
}

func isConnectNotFound(err error) bool {
	if err == nil {
		return false
	}
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connectErr.Code() == connect.CodeNotFound
	}
	return false
}
