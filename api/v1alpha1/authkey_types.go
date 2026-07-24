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

const AuthKeyConditionReady = "Ready"

// AuthKeySpec requests an Ionscale enrollment credential.
// AuthKey is intended for user or manually managed device enrollment. Kodiak
// Connectors use controller-owned bootstrap credentials instead.
type AuthKeySpec struct {
	// TailnetRef references the managed Tailnet.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="tailnetRef is immutable"
	TailnetRef corev1.LocalObjectReference `json:"tailnetRef"`

	// Ephemeral makes machines registered with this key ephemeral.
	// +optional
	// +kubebuilder:default=false
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="ephemeral is immutable; change rotationNonce to issue a new key"
	Ephemeral bool `json:"ephemeral,omitempty"`

	// Expiry is the lifetime of newly issued keys. Zero uses the control-plane default.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="expiry is immutable; change rotationNonce to issue a new key"
	Expiry *metav1.Duration `json:"expiry,omitempty"`

	// Tags are fixed at issuance.
	// +required
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:Pattern=`^tag:[a-zA-Z0-9][a-zA-Z0-9-]*$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="tags are immutable; change rotationNonce to issue a new key"
	Tags []string `json:"tags"`

	// PreAuthorized authorizes machines immediately after registration.
	// +optional
	// +kubebuilder:default=false
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="preAuthorized is immutable; change rotationNonce to issue a new key"
	PreAuthorized bool `json:"preAuthorized,omitempty"`

	// SecretName is the Secret that receives TS_AUTH_KEY.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="secretName is immutable"
	SecretName string `json:"secretName,omitempty"`

	// RotationNonce requests replacement when its value changes.
	// +optional
	// +kubebuilder:validation:MaxLength=128
	RotationNonce string `json:"rotationNonce,omitempty"`
}

// AuthKeyStatus is the observed state of an issued credential.
type AuthKeyStatus struct {
	// ObservedGeneration is the most recent generation reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// KeyID is represented as a string to preserve uint64 values in JSON clients.
	// +optional
	KeyID string `json:"keyID,omitempty"`

	// SecretRef references the Secret containing TS_AUTH_KEY.
	// +optional
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`

	// CreatedAt is the remote creation time.
	// +optional
	CreatedAt *metav1.Time `json:"createdAt,omitempty"`

	// ExpiresAt is the remote expiration time.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// IssuedRotationNonce is the nonce associated with the current key.
	// +optional
	IssuedRotationNonce string `json:"issuedRotationNonce,omitempty"`

	// RetiringKeyID is an old remote key pending deletion after rotation.
	// +optional
	RetiringKeyID string `json:"retiringKeyID,omitempty"`

	// Conditions contains the canonical readiness state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName={"kauthkey"}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="TAILNET",type="string",JSONPath=".spec.tailnetRef.name"
// +kubebuilder:printcolumn:name="EXPIRES",type="date",JSONPath=".status.expiresAt"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// AuthKey is the Schema for enrollment credentials.
type AuthKey struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AuthKeySpec   `json:"spec"`
	Status AuthKeyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AuthKeyList contains a list of AuthKey.
type AuthKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AuthKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AuthKey{}, &AuthKeyList{})
}
