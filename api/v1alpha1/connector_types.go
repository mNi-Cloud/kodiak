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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ResourceList defines resource quantities
type ResourceList struct {
	// +optional
	CPU string `json:"cpu,omitempty"`

	// +optional
	Memory string `json:"memory,omitempty"`
}

// ResourceRequirements defines resource requirements for the container
type ResourceRequirements struct {
	// +optional
	Limits ResourceList `json:"limits,omitempty"`

	// +optional
	Requests ResourceList `json:"requests,omitempty"`
}

// ConnectorSpec defines the desired state of Connector.
type ConnectorSpec struct {
	// +optional
	// List of resources this object depends on
	DependsOn []metav1.GroupVersionKind `json:"dependsOn,omitempty" patchStrategy:"merge" patchMergeKey:"kind"`

	// +optional
	// Metadata for the resource
	Metadata *ResourceMetadata `json:"metadata,omitempty"`

	// +optional
	// Specification of the connector
	Spec ConnectorSpecSpec `json:"spec,omitempty"`
}

// ResourceMetadata contains metadata for the resource
type ResourceMetadata struct {
	// +optional
	// Labels to apply to deployments and pods
	Labels map[string]string `json:"labels,omitempty"`

	// +optional
	// Annotations to apply to pods
	Annotations map[string]string `json:"annotations,omitempty"`
}

type ConnectorSpecSpec struct {
	// +optional
	// Tailscale configuration
	Tailscale TailscaleConfig `json:"tailscale,omitempty"`

	// +optional
	// Resource requirements for the Tailscale container
	Resources ResourceRequirements `json:"resources,omitempty"`
}

type TailscaleConfig struct {
	// +optional
	// Auth key used for Tailscale authentication (mutually exclusive with AuthKeyRef)
	AuthKey string `json:"authKey,omitempty"`

	// +optional
	// AuthKeySecretRef references an existing secret containing TS_AUTH_KEY (mutually exclusive with AuthKey and AuthKeyRef)
	AuthKeySecretRef *corev1.SecretKeySelector `json:"authKeySecretRef,omitempty"`

	// +optional
	// AuthKeyRef references an AuthKey resource to use for authentication
	AuthKeyRef *corev1.LocalObjectReference `json:"authKeyRef,omitempty"`

	// +kubebuilder:default="stable"
	// +optional
	// Tailscale image tag/version to use
	Version string `json:"version,omitempty"`

	// +optional
	// List of networks to advertise
	AdvertiseRoutes []string `json:"advertiseRoutes,omitempty"`

	// +kubebuilder:default=true
	// +optional
	// Enable USERSPACE networking mode
	UserspaceNetworking bool `json:"userspaceNetworking,omitempty"`

	// +optional
	// Hostname to use for the tailscale node
	Hostname string `json:"hostname,omitempty"`

	// +kubebuilder:default=false
	// +optional
	// Accept DNS configuration from the Tailscale network
	AcceptDNS bool `json:"acceptDns,omitempty"`

	// +optional
	// URL for custom Tailscale control server (mutually exclusive with ControlServerRef)
	ControlServerUrl string `json:"controlServerUrl,omitempty"`

	// +optional
	// ControlServerRef references a ControlServer resource to use
	ControlServerRef *corev1.LocalObjectReference `json:"controlServerRef,omitempty"`
}

// ConnectorStatus defines the observed state of Connector.
type ConnectorStatus struct {
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	// The Tailscale node ID if connected
	NodeID string `json:"nodeId,omitempty"`

	// +optional
	// The Tailscale IP address assigned to the node
	TailscaleIP string `json:"tailscaleIp,omitempty"`

	// +optional
	// The advertised routes that are actually being advertised
	AdvertisedRoutes []string `json:"advertisedRoutes,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="STATUS",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status",description="The status of the connector"
// +kubebuilder:printcolumn:name="TAILSCALE-IP",type="string",JSONPath=".status.tailscaleIp",description="Assigned Tailscale IP"
// +kubebuilder:printcolumn:name="NODE-ID",type="string",JSONPath=".status.nodeId",description="Tailscale node ID"
// +kubebuilder:printcolumn:name="ROUTES",type="string",JSONPath=".status.advertisedRoutes",description="Advertised routes"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// Connector is the Schema for the connectors API.
type Connector struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConnectorSpec   `json:"spec,omitempty"`
	Status ConnectorStatus `json:"status,omitempty"`
}

const (
	ConnectorStatusAvailable string = "Available"
	ConnectorStatusDegraded  string = "Degraded"
)

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
