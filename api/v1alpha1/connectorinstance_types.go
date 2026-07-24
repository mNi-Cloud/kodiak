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

const ConnectorInstanceConditionReady = "Ready"

// ConnectorInstanceSpec is an immutable snapshot of one Connector replica
// incarnation. ConnectorInstance is an implementation resource owned by a
// Connector, not a user-facing identity resource.
//
// +kubebuilder:validation:XValidation:rule="has(self.managedTailnetID) != has(self.authKeySecretRef)",message="exactly one of managedTailnetID or authKeySecretRef must be specified"
type ConnectorInstanceSpec struct {
	// ConnectorRef identifies the owning logical Connector.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="connectorRef is immutable"
	ConnectorRef corev1.LocalObjectReference `json:"connectorRef"`

	// Slot is the logical replica position served by this incarnation.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="slot is immutable"
	Slot int32 `json:"slot"`

	// Revision changes whenever the resolved Connector configuration changes.
	// +required
	// +kubebuilder:validation:MinLength=8
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="revision is immutable"
	Revision string `json:"revision"`

	// ManagedTailnetID selects a Kodiak-managed Ionscale Tailnet. Empty means
	// the control plane is externally managed.
	// +optional
	// +kubebuilder:validation:Pattern=`^[0-9]+$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="managedTailnetID is immutable"
	ManagedTailnetID string `json:"managedTailnetID,omitempty"`

	// LoginURL is the Tailscale protocol endpoint.
	// +optional
	// +kubebuilder:validation:Pattern=`^https://[^[:space:]]+$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="loginURL is immutable"
	LoginURL string `json:"loginURL,omitempty"`

	// AuthKeySecretRef selects an externally managed bootstrap key.
	// It is mutually exclusive with managedTailnetID.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="authKeySecretRef is immutable"
	AuthKeySecretRef *corev1.SecretKeySelector `json:"authKeySecretRef,omitempty"`

	// Tags are the policy identity requested for the external device.
	// +optional
	// +listType=set
	Tags []string `json:"tags,omitempty"`

	// AdvertiseRoutes are the exact prefixes served by this instance.
	// +required
	// +listType=set
	AdvertiseRoutes []string `json:"advertiseRoutes"`

	// Image is the stock Tailscale container image.
	// +required
	Image string `json:"image"`

	// Workload is the resolved Pod configuration.
	// +optional
	Workload ConnectorWorkloadSpec `json:"workload,omitempty"`
}

// ConnectorInstanceStatus records the external child currently associated
// with one Pod incarnation. It never contains tailscaled private state.
type ConnectorInstanceStatus struct {
	// ObservedGeneration is the latest reconciled generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// PodName is the controller-owned Pod for this incarnation.
	// +optional
	PodName string `json:"podName,omitempty"`

	// PodUID disambiguates a recreated Pod with the same name.
	// +optional
	PodUID string `json:"podUID,omitempty"`

	// RequestedHostname is the unique correlation key presented to the
	// control plane during registration.
	// +optional
	RequestedHostname string `json:"requestedHostname,omitempty"`

	// Device is the last external control-plane observation.
	// +optional
	Device ConnectorDeviceStatus `json:"device,omitempty"`

	// Conditions reports provisioning and route readiness.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName={"kconnectorinstance"}
// +kubebuilder:printcolumn:name="CONNECTOR",type="string",JSONPath=".spec.connectorRef.name"
// +kubebuilder:printcolumn:name="SLOT",type="integer",JSONPath=".spec.slot"
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// ConnectorInstance is an internal lifecycle record for one replaceable
// Connector device and its Pod.
type ConnectorInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConnectorInstanceSpec   `json:"spec"`
	Status ConnectorInstanceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ConnectorInstanceList contains a list of ConnectorInstance.
type ConnectorInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConnectorInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ConnectorInstance{}, &ConnectorInstanceList{})
}
