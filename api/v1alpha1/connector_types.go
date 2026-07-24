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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ConnectorConditionReady             = "Ready"
	ConnectorConditionAvailable         = "Available"
	ConnectorConditionWorkloadReady     = "WorkloadReady"
	ConnectorConditionControlPlaneReady = "ControlPlaneReady"
	ConnectorConditionRoutesReady       = "RoutesReady"
)

// ConnectorSpec describes a logical kernel-networking Tailscale subnet-router
// service. Individual Tailscale devices are replaceable implementation
// resources and are not stable identities.
//
// A managed Connector references a Kodiak Tailnet. Kodiak creates short-lived
// bootstrap credentials for each replica and observes the corresponding
// Ionscale machines. An externally managed Connector references an existing
// auth-key Secret and can optionally set a custom login URL.
//
// +kubebuilder:validation:XValidation:rule="has(self.tailnetRef) != has(self.authKeySecretRef)",message="exactly one of tailnetRef or authKeySecretRef must be specified"
// +kubebuilder:validation:XValidation:rule="!has(self.tailnetRef) || !has(self.loginURL)",message="loginURL is derived from Tailnet when tailnetRef is specified"
// +kubebuilder:validation:XValidation:rule="!has(self.tailnetRef) || size(self.tags) > 0",message="managed Connectors must specify at least one tag"
type ConnectorSpec struct {
	// TailnetRef selects a Kodiak-managed Tailnet.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="tailnetRef is immutable"
	TailnetRef *corev1.LocalObjectReference `json:"tailnetRef,omitempty"`

	// AuthKeySecretRef selects an externally managed bootstrap auth key.
	// The referenced key defaults to TS_AUTH_KEY.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="authKeySecretRef is immutable"
	AuthKeySecretRef *corev1.SecretKeySelector `json:"authKeySecretRef,omitempty"`

	// LoginURL is the control-plane URL for an externally managed Connector.
	// Empty uses the Tailscale client default.
	// +optional
	// +kubebuilder:validation:Pattern=`^https://[^[:space:]]+$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="loginURL is immutable"
	LoginURL string `json:"loginURL,omitempty"`

	// Tags are advertised by every Connector replica. Managed Connectors use
	// the same tags on their internally generated bootstrap credentials.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:Pattern=`^tag:[a-zA-Z0-9][a-zA-Z0-9-]*$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="tags are immutable"
	Tags []string `json:"tags,omitempty"`

	// Replicas is the number of independent subnet-router devices.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=5
	Replicas *int32 `json:"replicas,omitempty"`

	// SubnetRouter defines the routes advertised by the Connector.
	// +required
	SubnetRouter SubnetRouterSpec `json:"subnetRouter"`

	// Workload customizes the generated Pods without exposing
	// Tailscale implementation flags.
	// +optional
	Workload ConnectorWorkloadSpec `json:"workload,omitempty"`
}

// SubnetRouterSpec is the provider-neutral subnet routing contract.
type SubnetRouterSpec struct {
	// AdvertiseRoutes is the canonical CIDR set advertised by each replica.
	// Route approval remains a control-plane policy concern.
	// +required
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=49
	// +kubebuilder:validation:XValidation:rule="self.all(r, isCIDR(r) && r == string(cidr(r).masked()))",message="advertiseRoutes must contain canonical CIDRs"
	AdvertiseRoutes []string `json:"advertiseRoutes"`
}

// ConnectorWorkloadSpec contains standard Kubernetes workload overrides.
type ConnectorWorkloadSpec struct {
	// Metadata is applied to the generated Pods. Kodiak does not interpret
	// provider-specific annotations.
	// +optional
	Metadata ConnectorPodMetadata `json:"metadata,omitempty"`

	// Resources configures the tailscale container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// NodeSelector constrains Connector placement.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations are applied to Connector Pods.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Affinity is applied to Connector Pods.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`
}

// ConnectorPodMetadata is metadata propagated to Connector Pods.
type ConnectorPodMetadata struct {
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ConnectorStatus reports aggregate workload and managed control-plane state.
type ConnectorStatus struct {
	// ObservedGeneration is the most recent generation reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ManagedTailnetID records the Ionscale Tailnet used for exact cleanup.
	// +optional
	ManagedTailnetID string `json:"managedTailnetID,omitempty"`

	// Devices reports the active external device for each desired replica slot.
	// Device IDs, addresses, and hostnames can change when a Pod is replaced.
	// +optional
	// +listType=map
	// +listMapKey=ordinal
	Devices []ConnectorDeviceStatus `json:"devices,omitempty"`

	// Conditions contains the canonical readiness state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ConnectorDeviceStatus is the observed state for one replaceable replica.
type ConnectorDeviceStatus struct {
	// Ordinal identifies the logical replica slot.
	Ordinal int32 `json:"ordinal"`

	// DeviceID is the provider's current Tailscale node ID when available.
	// +optional
	DeviceID string `json:"deviceID,omitempty"`

	// MachineID is the Ionscale machine ID for a managed Connector.
	// +optional
	MachineID string `json:"machineID,omitempty"`

	// Hostname is the control-plane device hostname.
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// TailnetIPs are the assigned Tailscale addresses.
	// +optional
	TailnetIPs []string `json:"tailnetIPs,omitempty"`

	// Connected is reported only when the managed control plane provides it.
	// +optional
	Connected *bool `json:"connected,omitempty"`

	// AdvertisedRoutes are observed from the managed control plane.
	// +optional
	AdvertisedRoutes []string `json:"advertisedRoutes,omitempty"`

	// EnabledRoutes are routes approved by control-plane policy.
	// +optional
	EnabledRoutes []string `json:"enabledRoutes,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName={"kconnector"}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="REPLICAS",type="integer",JSONPath=".spec.replicas"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// Connector is the Schema for subnet-router Connectors.
type Connector struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConnectorSpec   `json:"spec"`
	Status ConnectorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ConnectorList contains a list of Connector.
type ConnectorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Connector `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Connector{}, &ConnectorList{})
}
