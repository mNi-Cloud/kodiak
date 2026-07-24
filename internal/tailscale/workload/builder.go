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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

const (
	LabelManaged   = "kodiak.mnicloud.jp/managed"
	LabelConnector = "kodiak.mnicloud.jp/connector"
	LabelInstance  = "kodiak.mnicloud.jp/connector-instance"
	LabelSlot      = "kodiak.mnicloud.jp/slot"
	LabelRevision  = "kodiak.mnicloud.jp/revision"
	LabelComponent = "app.kubernetes.io/component"

	HealthPort = 9002
)

// ConnectorLabelValue is a DNS-label-safe stable identity for child lookups.
func ConnectorLabelValue(connectorName string) string {
	return safeName(connectorName, 63)
}

// InstanceGenerateName returns the prefix for a replaceable replica instance.
func InstanceGenerateName(connectorName string, slot int32) string {
	return safeName(fmt.Sprintf("%s-%d", connectorName, slot), 57) + "-"
}

// RequestedHostname is a collision-resistant device hostname derived from an
// immutable ConnectorInstance UID.
func RequestedHostname(instance *kodiakv1alpha1.ConnectorInstance) string {
	raw := strings.ReplaceAll(string(instance.UID), "-", "")
	if len(raw) > 24 {
		raw = raw[:24]
	}
	return "kdk-" + raw
}

// BootstrapSecretName returns the transient auth-key Secret name.
func BootstrapSecretName(instanceName string) string {
	return safeName(instanceName+"-bootstrap", 63)
}

// InstanceLabels are controller-owned and cannot be overridden by workload metadata.
func InstanceLabels(instance *kodiakv1alpha1.ConnectorInstance) map[string]string {
	return map[string]string{
		LabelManaged:                   "true",
		LabelConnector:                 ConnectorLabelValue(instance.Spec.ConnectorRef.Name),
		LabelInstance:                  ConnectorLabelValue(instance.Name),
		LabelSlot:                      fmt.Sprintf("%d", instance.Spec.Slot),
		LabelRevision:                  instance.Spec.Revision,
		LabelComponent:                 "subnet-router",
		"app.kubernetes.io/name":       "tailscale",
		"app.kubernetes.io/instance":   ConnectorLabelValue(instance.Spec.ConnectorRef.Name),
		"app.kubernetes.io/managed-by": "kodiak",
	}
}

// Pod returns one replaceable, kernel-networking subnet-router. tailscaled
// state is intentionally scoped to the Pod incarnation and never stored in
// Kubernetes. The Pod has no service-account token and no Kubernetes API
// credentials.
func Pod(instance *kodiakv1alpha1.ConnectorInstance, authSecretName, authSecretKey string) *corev1.Pod {
	extraArgs := []string{
		"--snat-subnet-routes=true",
		"--stateful-filtering=true",
	}
	if instance.Spec.LoginURL != "" {
		extraArgs = append(extraArgs, "--login-server="+instance.Spec.LoginURL)
	}
	if len(instance.Spec.Tags) != 0 {
		tags := append([]string(nil), instance.Spec.Tags...)
		sort.Strings(tags)
		extraArgs = append(extraArgs, "--advertise-tags="+strings.Join(tags, ","))
	}

	labels := make(map[string]string, len(instance.Spec.Workload.Metadata.Labels)+8)
	for key, value := range instance.Spec.Workload.Metadata.Labels {
		labels[key] = value
	}
	for key, value := range InstanceLabels(instance) {
		labels[key] = value
	}
	annotations := make(map[string]string, len(instance.Spec.Workload.Metadata.Annotations))
	for key, value := range instance.Spec.Workload.Metadata.Annotations {
		annotations[key] = value
	}

	privileged := ptr.To(true)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        instance.Name,
			Namespace:   instance.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			AutomountServiceAccountToken: ptr.To(false),
			NodeSelector:                 instance.Spec.Workload.NodeSelector,
			Tolerations:                  instance.Spec.Workload.Tolerations,
			Affinity:                     instance.Spec.Workload.Affinity,
			InitContainers: []corev1.Container{{
				Name:            "sysctler",
				Image:           instance.Spec.Image,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Command:         []string{"/bin/sh", "-c"},
				Args: []string{
					"sysctl -w net.ipv4.ip_forward=1 && if sysctl net.ipv6.conf.all.forwarding; then sysctl -w net.ipv6.conf.all.forwarding=1; fi",
				},
				SecurityContext: &corev1.SecurityContext{Privileged: privileged},
			}},
			Containers: []corev1.Container{{
				Name:            "tailscale",
				Image:           instance.Spec.Image,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Env: []corev1.EnvVar{
					{
						Name: "TS_AUTHKEY",
						ValueFrom: &corev1.EnvVarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: authSecretName},
								Key:                  authSecretKey,
							},
						},
					},
					{Name: "TS_KUBE_SECRET", Value: ""},
					{Name: "TS_STATE_DIR", Value: "/var/lib/tailscale"},
					{Name: "TS_USERSPACE", Value: "false"},
					{Name: "TS_AUTH_ONCE", Value: "true"},
					{Name: "TS_ACCEPT_DNS", Value: "false"},
					{Name: "TS_HOSTNAME", Value: RequestedHostname(instance)},
					{Name: "TS_ROUTES", Value: strings.Join(instance.Spec.AdvertiseRoutes, ",")},
					{Name: "TS_EXTRA_ARGS", Value: strings.Join(extraArgs, " ")},
					{Name: "TS_ENABLE_HEALTH_CHECK", Value: "true"},
					{Name: "TS_LOCAL_ADDR_PORT", Value: fmt.Sprintf("[::]:%d", HealthPort)},
					{Name: "TS_EXPERIMENTAL_ENABLE_FORWARDING_OPTIMIZATIONS", Value: "true"},
				},
				Resources:       instance.Spec.Workload.Resources,
				SecurityContext: &corev1.SecurityContext{Privileged: privileged},
				Ports: []corev1.ContainerPort{{
					Name:          "health",
					ContainerPort: HealthPort,
				}},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "tailscale-state",
					MountPath: "/var/lib/tailscale",
				}},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{
							Path: "/healthz",
							Port: intstr.FromInt(HealthPort),
						},
					},
					InitialDelaySeconds: 2,
					PeriodSeconds:       5,
				},
				LivenessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{
							Path: "/healthz",
							Port: intstr.FromInt(HealthPort),
						},
					},
					InitialDelaySeconds: 10,
					PeriodSeconds:       10,
				},
			}},
			Volumes: []corev1.Volume{{
				Name: "tailscale-state",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			}},
		},
	}
}

func safeName(value string, max int) string {
	value = strings.ToLower(value)
	if len(value) <= max {
		return strings.Trim(value, "-")
	}
	sum := sha256.Sum256([]byte(value))
	suffix := fmt.Sprintf("-%x", sum[:4])
	return strings.TrimRight(value[:max-len(suffix)], "-") + suffix
}
