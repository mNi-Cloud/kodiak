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

// Package workload renders the Kubernetes resources used by a Kodiak
// Connector. It deliberately contains no Ionscale or mNi VPC logic.
package workload

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

const (
	LabelManaged   = "kodiak.mnicloud.jp/managed"
	LabelConnector = "kodiak.mnicloud.jp/connector"
	LabelComponent = "app.kubernetes.io/component"

	HealthPort = 9002
)

// Names are deterministic child-resource names for a Connector.
type Names struct {
	StatefulSet    string
	Service        string
	ServiceAccount string
	Role           string
	RoleBinding    string
}

// ChildNames returns all non-replica child names.
func ChildNames(connectorName string) Names {
	base := connectorName + "-connector"
	const maxBaseLength = 52
	if len(base) > maxBaseLength {
		sum := sha256.Sum256([]byte(base))
		suffix := fmt.Sprintf("-%x", sum[:4])
		base = strings.TrimRight(base[:maxBaseLength-len(suffix)], "-") + suffix
	}
	return Names{
		StatefulSet:    base,
		Service:        base,
		ServiceAccount: base,
		Role:           base,
		RoleBinding:    base,
	}
}

// ConnectorLabelValue is a DNS-label-safe stable identity for child lookups.
func ConnectorLabelValue(connectorName string) string {
	return ChildNames(connectorName).StatefulSet
}

// StateSecretName returns the state Secret name for a StatefulSet ordinal.
func StateSecretName(connectorName string, ordinal int32) string {
	return fmt.Sprintf("%s-%d", ChildNames(connectorName).StatefulSet, ordinal)
}

// SelectorLabels are controller-owned and cannot be overridden by workload metadata.
func SelectorLabels(connector *kodiakv1alpha1.Connector) map[string]string {
	return map[string]string{
		LabelManaged:                   "true",
		LabelConnector:                 ConnectorLabelValue(connector.Name),
		LabelComponent:                 "subnet-router",
		"app.kubernetes.io/name":       "tailscale",
		"app.kubernetes.io/instance":   ConnectorLabelValue(connector.Name),
		"app.kubernetes.io/managed-by": "kodiak",
	}
}

// PodLabels merges user labels while preserving controller-owned selectors.
func PodLabels(connector *kodiakv1alpha1.Connector) map[string]string {
	labels := make(map[string]string, len(connector.Spec.Workload.Metadata.Labels)+6)
	for key, value := range connector.Spec.Workload.Metadata.Labels {
		labels[key] = value
	}
	for key, value := range SelectorLabels(connector) {
		labels[key] = value
	}
	return labels
}

// ServiceAccount returns the dedicated identity used by containerboot.
func ServiceAccount(connector *kodiakv1alpha1.Connector) *corev1.ServiceAccount {
	names := ChildNames(connector.Name)
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.ServiceAccount,
			Namespace: connector.Namespace,
			Labels:    SelectorLabels(connector),
		},
		AutomountServiceAccountToken: ptr.To(true),
	}
}

// Role permits containerboot to update only the pre-created per-replica state Secrets.
func Role(connector *kodiakv1alpha1.Connector, replicas int32) *rbacv1.Role {
	names := ChildNames(connector.Name)
	resourceNames := make([]string, 0, replicas)
	for ordinal := int32(0); ordinal < replicas; ordinal++ {
		resourceNames = append(resourceNames, StateSecretName(connector.Name, ordinal))
	}
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.Role,
			Namespace: connector.Namespace,
			Labels:    SelectorLabels(connector),
		},
		Rules: []rbacv1.PolicyRule{{
			APIGroups:     []string{""},
			Resources:     []string{"secrets"},
			ResourceNames: resourceNames,
			Verbs:         []string{"get", "patch", "update"},
		}},
	}
}

// RoleBinding binds the per-Connector state Secret Role.
func RoleBinding(connector *kodiakv1alpha1.Connector) *rbacv1.RoleBinding {
	names := ChildNames(connector.Name)
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.RoleBinding,
			Namespace: connector.Namespace,
			Labels:    SelectorLabels(connector),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     names.Role,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      names.ServiceAccount,
			Namespace: connector.Namespace,
		}},
	}
}

// HeadlessService provides the stable network identity required by StatefulSet.
func HeadlessService(connector *kodiakv1alpha1.Connector) *corev1.Service {
	names := ChildNames(connector.Name)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.Service,
			Namespace: connector.Namespace,
			Labels:    SelectorLabels(connector),
		},
		Spec: corev1.ServiceSpec{
			ClusterIP:                corev1.ClusterIPNone,
			PublishNotReadyAddresses: true,
			Selector:                 SelectorLabels(connector),
			Ports: []corev1.ServicePort{{
				Name: "health",
				Port: HealthPort,
			}},
		},
	}
}

// StatefulSet returns a kernel-networking subnet router with persistent
// Kubernetes Secret state. The auth key itself is injected into the state
// Secret and removed by containerboot after successful registration.
func StatefulSet(connector *kodiakv1alpha1.Connector, image, loginURL string, replicas int32) *appsv1.StatefulSet {
	names := ChildNames(connector.Name)
	extraArgs := []string{
		"--snat-subnet-routes=true",
		"--stateful-filtering=true",
	}
	if loginURL != "" {
		extraArgs = append(extraArgs, "--login-server="+loginURL)
	}
	if len(connector.Spec.Tags) != 0 {
		tags := append([]string(nil), connector.Spec.Tags...)
		sort.Strings(tags)
		extraArgs = append(extraArgs, "--advertise-tags="+strings.Join(tags, ","))
	}

	env := []corev1.EnvVar{
		{
			Name: "POD_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
			},
		},
		{
			Name: "POD_UID",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.uid"},
			},
		},
		{Name: "TS_KUBE_SECRET", Value: "$(POD_NAME)"},
		{Name: "TS_USERSPACE", Value: "false"},
		{Name: "TS_AUTH_ONCE", Value: "true"},
		{Name: "TS_ACCEPT_DNS", Value: "false"},
		{Name: "TS_HOSTNAME", Value: "$(POD_NAME)"},
		{Name: "TS_ROUTES", Value: strings.Join(connector.Spec.SubnetRouter.AdvertiseRoutes, ",")},
		{Name: "TS_EXTRA_ARGS", Value: strings.Join(extraArgs, " ")},
		{Name: "TS_ENABLE_HEALTH_CHECK", Value: "true"},
		{Name: "TS_LOCAL_ADDR_PORT", Value: fmt.Sprintf("[::]:%d", HealthPort)},
		{Name: "TS_EXPERIMENTAL_ENABLE_FORWARDING_OPTIMIZATIONS", Value: "true"},
	}

	privileged := ptr.To(true)
	podLabels := PodLabels(connector)
	annotations := make(map[string]string, len(connector.Spec.Workload.Metadata.Annotations))
	for key, value := range connector.Spec.Workload.Metadata.Annotations {
		annotations[key] = value
	}

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.StatefulSet,
			Namespace: connector.Namespace,
			Labels:    SelectorLabels(connector),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:            ptr.To(replicas),
			ServiceName:         names.Service,
			PodManagementPolicy: appsv1.ParallelPodManagement,
			Selector: &metav1.LabelSelector{
				MatchLabels: SelectorLabels(connector),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podLabels,
					Annotations: annotations,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: names.ServiceAccount,
					NodeSelector:       connector.Spec.Workload.NodeSelector,
					Tolerations:        connector.Spec.Workload.Tolerations,
					Affinity:           connector.Spec.Workload.Affinity,
					InitContainers: []corev1.Container{{
						Name:            "sysctler",
						Image:           image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Command:         []string{"/bin/sh", "-c"},
						Args: []string{
							"sysctl -w net.ipv4.ip_forward=1 && if sysctl net.ipv6.conf.all.forwarding; then sysctl -w net.ipv6.conf.all.forwarding=1; fi",
						},
						SecurityContext: &corev1.SecurityContext{Privileged: privileged},
					}},
					Containers: []corev1.Container{{
						Name:            "tailscale",
						Image:           image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Env:             env,
						Resources:       connector.Spec.Workload.Resources,
						SecurityContext: &corev1.SecurityContext{Privileged: privileged},
						Ports: []corev1.ContainerPort{{
							Name:          "health",
							ContainerPort: HealthPort,
						}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/healthz",
									Port: intstrFromInt(HealthPort),
								},
							},
							InitialDelaySeconds: 2,
							PeriodSeconds:       5,
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/healthz",
									Port: intstrFromInt(HealthPort),
								},
							},
							InitialDelaySeconds: 10,
							PeriodSeconds:       10,
						},
					}},
				},
			},
		},
	}
}

func intstrFromInt(value int) intstr.IntOrString {
	return intstr.FromInt(value)
}
