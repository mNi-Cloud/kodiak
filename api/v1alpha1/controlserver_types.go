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

// ControlServerSpec defines the desired state of ControlServer
type ControlServerSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +optional
	// +kubebuilder:default="ghcr.io/jsiebens/ionscale:latest"
	// Image for the control server
	Image string `json:"image,omitempty"`

	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// Replicas for the control server deployment
	Replicas *int32 `json:"replicas,omitempty"`

	// +optional
	// Resources for the control server container
	Resources ResourceRequirements `json:"resources,omitempty"`

	// +optional
	// Configuration for the control server
	Config ControlServerConfig `json:"config,omitempty"`

	// +optional
	// Storage configuration for persistent data
	Storage *StorageConfig `json:"storage,omitempty"`
}

// ControlServerConfig defines configuration for the control server
type ControlServerConfig struct {
	// +optional
	// +kubebuilder:default=":8080"
	// ListenAddr for the control server API
	ListenAddr string `json:"listenAddr,omitempty"`

	// +optional
	// +kubebuilder:default=":9091"
	// MetricsListenAddr for metrics endpoint
	MetricsListenAddr string `json:"metricsListenAddr,omitempty"`

	// +optional
	// +kubebuilder:default=":3478"
	// StunListenAddr for STUN server
	StunListenAddr string `json:"stunListenAddr,omitempty"`

	// +optional
	// PublicAddr is the public address for the control server
	PublicAddr string `json:"publicAddr,omitempty"`

	// +optional
	// StunPublicAddr is the publicly reachable STUN address
	StunPublicAddr string `json:"stunPublicAddr,omitempty"`

	// +optional
	// Database configuration
	Database DatabaseConfig `json:"database,omitempty"`

	// +optional
	// TLS configuration
	TLS *TLSConfig `json:"tls,omitempty"`

	// +optional
	// DERP server configuration
	DERP *DERPConfig `json:"derp,omitempty"`

	// +optional
	// DNS configuration
	DNS *DNSConfig `json:"dns,omitempty"`

	// +optional
	// Auth configuration for OIDC
	Auth *AuthConfig `json:"auth,omitempty"`

	// +optional
	// Keys configuration
	Keys *KeysConfig `json:"keys,omitempty"`

	// +optional
	// PollNet configuration
	PollNet *PollNetConfig `json:"pollNet,omitempty"`

	// +optional
	// Logging configuration
	Logging *LoggingConfig `json:"logging,omitempty"`
}

// DatabaseConfig defines database settings
type DatabaseConfig struct {
	// +optional
	// +kubebuilder:default="sqlite"
	// +kubebuilder:validation:Enum=sqlite;postgres
	// Type of database
	Type string `json:"type,omitempty"`

	// +optional
	// URL for database connection
	URL string `json:"url,omitempty"`
}

// TLSConfig defines TLS settings
type TLSConfig struct {
	// +optional
	// +kubebuilder:default=false
	// Disable TLS
	Disable bool `json:"disable,omitempty"`

	// +optional
	// CertSecretName references a secret containing TLS cert and key
	CertSecretName string `json:"certSecretName,omitempty"`

	// +optional
	// CertFile path when providing TLS cert via filesystem
	CertFile string `json:"certFile,omitempty"`

	// +optional
	// KeyFile path when providing TLS key via filesystem
	KeyFile string `json:"keyFile,omitempty"`

	// +optional
	// AcmeEnabled enables automatic certificate management
	AcmeEnabled bool `json:"acmeEnabled,omitempty"`

	// +optional
	// AcmeEmail for Let's Encrypt registration
	AcmeEmail string `json:"acmeEmail,omitempty"`

	// +optional
	// AcmeCA overrides the default ACME directory endpoint
	AcmeCA string `json:"acmeCa,omitempty"`

	// +optional
	// AcmePath for storing ACME generated artifacts
	AcmePath string `json:"acmePath,omitempty"`

	// +kubebuilder:default=true
	// +optional
	// ForceHTTPS redirects HTTP requests to HTTPS when TLS is enabled
	ForceHTTPS bool `json:"forceHttps,omitempty"`
}

// DERPConfig defines DERP server settings
type DERPConfig struct {
	// +optional
	// Sources to fetch DERP map updates from
	Sources []string `json:"sources,omitempty"`

	// +optional
	// +kubebuilder:default=false
	// Disabled flag for embedded DERP server
	Disabled bool `json:"disabled,omitempty"`

	// +optional
	// +kubebuilder:default=1000
	// RegionID for the DERP region
	RegionID int `json:"regionId,omitempty"`

	// +optional
	// +kubebuilder:default="kodiak"
	// RegionCode for the DERP region
	RegionCode string `json:"regionCode,omitempty"`

	// +optional
	// +kubebuilder:default="Kodiak Embedded DERP"
	// RegionName for the DERP region
	RegionName string `json:"regionName,omitempty"`
}

// DNSConfig defines DNS settings for control server
type DNSConfig struct {
	// +optional
	// +kubebuilder:default="tailnet.local"
	// MagicDNSSuffix for the tailnet
	MagicDNSSuffix string `json:"magicDnsSuffix,omitempty"`

	// +optional
	// Provider configuration for DNS integrations
	Provider *DNSProviderConfig `json:"provider,omitempty"`
}

// DNSProviderConfig defines external DNS provider configuration
type DNSProviderConfig struct {
	// +optional
	// Provider name
	Name string `json:"name,omitempty"`

	// +optional
	// Zone managed by the provider
	Zone string `json:"zone,omitempty"`

	// +optional
	// Arbitrary provider configuration key/value pairs
	Config map[string]string `json:"config,omitempty"`
}

// AuthConfig defines authentication settings
type AuthConfig struct {
	// +optional
	// OIDC provider configuration
	OIDC *OIDCConfig `json:"oidc,omitempty"`

	// +optional
	// System admin configuration
	SystemAdmins *SystemAdminsConfig `json:"systemAdmins,omitempty"`
}

// OIDCConfig defines OIDC provider settings
type OIDCConfig struct {
	// Issuer URL for the OIDC provider
	Issuer string `json:"issuer"`

	// ClientID for OIDC authentication
	ClientID string `json:"clientId"`

	// ClientSecretRef references a secret containing the client secret
	ClientSecretRef corev1.SecretKeySelector `json:"clientSecretRef"`

	// +optional
	// AdditionalScopes for OIDC authentication
	AdditionalScopes []string `json:"additionalScopes,omitempty"`
}

// SystemAdminsConfig defines configuration for granting system admin privileges
type SystemAdminsConfig struct {
	// +optional
	// Emails of users that should be system administrators
	Emails []string `json:"emails,omitempty"`

	// +optional
	// Subject claims of users that should be system administrators
	Subs []string `json:"subs,omitempty"`

	// +optional
	// Additional filters encoded as BEXPR expressions
	Filters []string `json:"filters,omitempty"`
}

// StorageConfig defines storage settings
type StorageConfig struct {
	// +optional
	// +kubebuilder:default="10Gi"
	// Size of the persistent volume
	Size string `json:"size,omitempty"`

	// +optional
	// StorageClassName for the PVC
	StorageClassName string `json:"storageClassName,omitempty"`
}

// KeysConfig defines configuration for static keys
type KeysConfig struct {
	// +optional
	// SystemAdminKey provides a static admin key
	SystemAdminKey string `json:"systemAdminKey,omitempty"`
}

// PollNetConfig captures polling network settings
type PollNetConfig struct {
	// +optional
	// KeepAliveInterval configures device keep alive frequency
	KeepAliveInterval string `json:"keepAliveInterval,omitempty"`
}

// LoggingConfig defines logging preferences
type LoggingConfig struct {
	// +optional
	// Format specifies log output formatting
	Format string `json:"format,omitempty"`

	// +optional
	// Level sets the log verbosity level
	Level string `json:"level,omitempty"`

	// +optional
	// File writes logs to the specified file path
	File string `json:"file,omitempty"`
}

// ControlServerStatus defines the observed state of ControlServer.
type ControlServerStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +optional
	// Ready indicates if the control server is ready
	Ready bool `json:"ready,omitempty"`

	// +optional
	// Endpoint is the service endpoint for the control server
	Endpoint string `json:"endpoint,omitempty"`

	// +optional
	// Phase represents the current phase of the control server
	Phase string `json:"phase,omitempty"`

	// conditions represent the current state of the ControlServer resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="READY",type="boolean",JSONPath=".status.ready",description="Control server ready status"
// +kubebuilder:printcolumn:name="ENDPOINT",type="string",JSONPath=".status.endpoint",description="Control server endpoint"
// +kubebuilder:printcolumn:name="PHASE",type="string",JSONPath=".status.phase",description="Current phase"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// ControlServer is the Schema for the controlservers API
type ControlServer struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of ControlServer
	// +required
	Spec ControlServerSpec `json:"spec"`

	// status defines the observed state of ControlServer
	// +optional
	Status ControlServerStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ControlServerList contains a list of ControlServer
type ControlServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ControlServer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ControlServer{}, &ControlServerList{})
}
