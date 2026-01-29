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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// TailnetSpec defines the desired state of Tailnet
type TailnetSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:Required
	// Name of the tailnet
	Name string `json:"name"`

	// +optional
	// IAMPolicy defines identity and access management policy (HuJSON format)
	IAMPolicy string `json:"iamPolicy,omitempty"`

	// +optional
	// ACLPolicy defines access control list policy (HuJSON format)
	ACLPolicy string `json:"aclPolicy,omitempty"`

	// +optional
	// DNSConfig for the tailnet
	DNSConfig *TailnetDNSConfig `json:"dnsConfig,omitempty"`

	// +optional
	// +kubebuilder:default=false
	// ServiceCollectionEnabled enables service collection
	ServiceCollectionEnabled bool `json:"serviceCollectionEnabled,omitempty"`

	// +optional
	// +kubebuilder:default=false
	// FileSharingEnabled enables file sharing between nodes
	FileSharingEnabled bool `json:"fileSharingEnabled,omitempty"`

	// +optional
	// +kubebuilder:default=false
	// SSHEnabled enables SSH access
	SSHEnabled bool `json:"sshEnabled,omitempty"`

	// +optional
	// +kubebuilder:default=false
	// MachineAuthorizationEnabled requires machine authorization
	MachineAuthorizationEnabled bool `json:"machineAuthorizationEnabled,omitempty"`
}

// TailnetDNSConfig defines DNS configuration for a tailnet
type TailnetDNSConfig struct {
	// +optional
	// Nameservers for the tailnet
	Nameservers []string `json:"nameservers,omitempty"`

	// +optional
	// MagicDNS enables magic DNS
	MagicDNS bool `json:"magicDns,omitempty"`

	// +optional
	// Domains for split DNS
	Domains []string `json:"domains,omitempty"`

	// +optional
	// SearchDomains for DNS resolution
	SearchDomains []string `json:"searchDomains,omitempty"`
}

// TailnetStatus defines the observed state of Tailnet.
type TailnetStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +optional
	// TailnetID is the ID assigned by the control server
	TailnetID uint64 `json:"tailnetId,omitempty"`

	// +optional
	// Ready indicates if the tailnet is ready
	Ready bool `json:"ready,omitempty"`

	// +optional
	// Phase represents the current phase of the tailnet
	Phase string `json:"phase,omitempty"`

	// +optional
	// MachineCount is the number of machines in the tailnet
	MachineCount int `json:"machineCount,omitempty"`

	// +optional
	// ControlServerUrl is the public URL of the ionscale control server
	ControlServerUrl string `json:"controlServerUrl,omitempty"`

	// +optional
	// LastSyncTime is the last time the tailnet was synced
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// conditions represent the current state of the Tailnet resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="READY",type="boolean",JSONPath=".status.ready",description="Tailnet ready status"
// +kubebuilder:printcolumn:name="ID",type="integer",JSONPath=".status.tailnetId",description="Tailnet ID"
// +kubebuilder:printcolumn:name="MACHINES",type="integer",JSONPath=".status.machineCount",description="Number of machines"
// +kubebuilder:printcolumn:name="PHASE",type="string",JSONPath=".status.phase",description="Current phase"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// Tailnet is the Schema for the tailnets API
type Tailnet struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Tailnet
	// +required
	Spec TailnetSpec `json:"spec"`

	// status defines the observed state of Tailnet
	// +optional
	Status TailnetStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// TailnetList contains a list of Tailnet
type TailnetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Tailnet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Tailnet{}, &TailnetList{})
}
