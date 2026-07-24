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

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const TailnetConditionReady = "Ready"

// TailnetSpec is the desired state of an Ionscale Tailnet.
type TailnetSpec struct {
	// Name is the immutable remote Tailnet name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="name is immutable"
	Name string `json:"name"`

	// IAMPolicy is the complete Ionscale IAM policy in HuJSON format.
	// +optional
	IAMPolicy string `json:"iamPolicy,omitempty"`

	// ACLPolicy is the complete Ionscale ACL policy in HuJSON format.
	// +optional
	ACLPolicy string `json:"aclPolicy,omitempty"`

	// DNSConfig configures Tailnet DNS.
	// +optional
	DNSConfig *TailnetDNSConfig `json:"dnsConfig,omitempty"`

	// +optional
	// +kubebuilder:default=false
	ServiceCollectionEnabled bool `json:"serviceCollectionEnabled,omitempty"`

	// +optional
	// +kubebuilder:default=false
	FileSharingEnabled bool `json:"fileSharingEnabled,omitempty"`

	// +optional
	// +kubebuilder:default=false
	SSHEnabled bool `json:"sshEnabled,omitempty"`

	// +optional
	// +kubebuilder:default=false
	MachineAuthorizationEnabled bool `json:"machineAuthorizationEnabled,omitempty"`
}

// TailnetDNSConfig defines DNS configuration for a Tailnet.
type TailnetDNSConfig struct {
	// +optional
	Nameservers []string `json:"nameservers,omitempty"`

	// +optional
	MagicDNS bool `json:"magicDNS,omitempty"`

	// +optional
	Domains []string `json:"domains,omitempty"`

	// +optional
	SearchDomains []string `json:"searchDomains,omitempty"`
}

// TailnetStatus is the observed state of a remote Tailnet.
type TailnetStatus struct {
	// ObservedGeneration is the most recent generation reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// TailnetID is represented as a string to preserve uint64 values in JSON clients.
	// +optional
	TailnetID string `json:"tailnetID,omitempty"`

	// LoginURL is the public Tailscale protocol endpoint used by managed Connectors.
	// +optional
	LoginURL string `json:"loginURL,omitempty"`

	// MachineCount is the current number of machines.
	// +optional
	MachineCount int32 `json:"machineCount,omitempty"`

	// LastSyncTime is the last successful synchronization time.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions contains the canonical readiness state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName={"ktailnet"}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="ID",type="string",JSONPath=".status.tailnetID"
// +kubebuilder:printcolumn:name="MACHINES",type="integer",JSONPath=".status.machineCount"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// Tailnet is the Schema for managed Ionscale Tailnets.
type Tailnet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TailnetSpec   `json:"spec"`
	Status TailnetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TailnetList contains a list of Tailnet.
type TailnetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Tailnet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Tailnet{}, &TailnetList{})
}
