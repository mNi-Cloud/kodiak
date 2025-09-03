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

// AuthKeySpec defines the desired state of AuthKey
type AuthKeySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:Required
	// TailnetRef references the Tailnet this auth key belongs to
	TailnetRef corev1.LocalObjectReference `json:"tailnetRef"`

	// +optional
	// +kubebuilder:default=false
	// Ephemeral determines if machines authenticated with this key are ephemeral
	Ephemeral bool `json:"ephemeral,omitempty"`

	// +optional
	// Expiry duration for the auth key (e.g., "24h", "7d")
	Expiry string `json:"expiry,omitempty"`

	// +optional
	// Tags to apply to machines authenticated with this key
	Tags []string `json:"tags,omitempty"`

	// +optional
	// +kubebuilder:default=false
	// PreAuthorized determines if machines are automatically authorized
	PreAuthorized bool `json:"preAuthorized,omitempty"`

	// +optional
	// SecretName to store the generated auth key (defaults to authkey-<name>)
	SecretName string `json:"secretName,omitempty"`
}

// AuthKeyStatus defines the observed state of AuthKey.
type AuthKeyStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +optional
	// KeyID is the ID assigned by the control server
	KeyID uint64 `json:"keyId,omitempty"`

	// +optional
	// SecretRef references the secret containing the auth key
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`

	// +optional
	// CreatedAt is when the key was created
	CreatedAt *metav1.Time `json:"createdAt,omitempty"`

	// +optional
	// ExpiresAt is when the key expires
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// +optional
	// Ready indicates if the auth key is ready for use
	Ready bool `json:"ready,omitempty"`

	// +optional
	// Phase represents the current phase of the auth key
	Phase string `json:"phase,omitempty"`

	// conditions represent the current state of the AuthKey resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="READY",type="boolean",JSONPath=".status.ready",description="Auth key ready status"
// +kubebuilder:printcolumn:name="TAILNET",type="string",JSONPath=".spec.tailnetRef.name",description="Tailnet reference"
// +kubebuilder:printcolumn:name="EPHEMERAL",type="boolean",JSONPath=".spec.ephemeral",description="Ephemeral key"
// +kubebuilder:printcolumn:name="PREAUTH",type="boolean",JSONPath=".spec.preAuthorized",description="Pre-authorized"
// +kubebuilder:printcolumn:name="EXPIRES",type="date",JSONPath=".status.expiresAt",description="Expiration time"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// AuthKey is the Schema for the authkeys API
type AuthKey struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of AuthKey
	// +required
	Spec AuthKeySpec `json:"spec"`

	// status defines the observed state of AuthKey
	// +optional
	Status AuthKeyStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// AuthKeyList contains a list of AuthKey
type AuthKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AuthKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AuthKey{}, &AuthKeyList{})
}
