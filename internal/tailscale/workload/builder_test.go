/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package workload

import (
	"strings"
	"testing"

	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestConnectorPodContract(t *testing.T) {
	instance := &kodiakv1alpha1.ConnectorInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vpn-0-abcd",
			Namespace: "tenant-a",
			UID:       types.UID("12345678-1234-1234-1234-123456789abc"),
		},
		Spec: kodiakv1alpha1.ConnectorInstanceSpec{
			ConnectorRef:    corev1.LocalObjectReference{Name: "vpn"},
			Slot:            0,
			Revision:        "0123456789abcdef",
			LoginURL:        "https://vpn.example.test",
			Tags:            []string{"tag:vpn"},
			AdvertiseRoutes: []string{"10.0.1.0/24"},
			Image:           "tailscale/tailscale:v1.98.9",
		},
	}

	pod := Pod(instance, "bootstrap", "TS_AUTH_KEY")
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Fatal("Connector Pod must not mount a service-account token")
	}
	if pod.Spec.ServiceAccountName != "" {
		t.Fatalf("unexpected serviceAccountName %q", pod.Spec.ServiceAccountName)
	}
	if pod.Spec.Containers[0].ImagePullPolicy != corev1.PullIfNotPresent {
		t.Fatalf("imagePullPolicy=%q, want IfNotPresent", pod.Spec.Containers[0].ImagePullPolicy)
	}
	container := pod.Spec.Containers[0]
	if got := workloadEnv(container.Env, "TS_KUBE_SECRET"); got != "" {
		t.Fatalf("TS_KUBE_SECRET=%q, want empty", got)
	}
	if got := workloadEnv(container.Env, "TS_STATE_DIR"); got != "/var/lib/tailscale" {
		t.Fatalf("TS_STATE_DIR=%q", got)
	}
	if got := workloadEnv(container.Env, "TS_ROUTES"); got != "10.0.1.0/24" {
		t.Fatalf("TS_ROUTES=%q", got)
	}
	if got := workloadEnv(container.Env, "TS_EXTRA_ARGS"); got != "--snat-subnet-routes=true --stateful-filtering=true --login-server=https://vpn.example.test --advertise-tags=tag:vpn" {
		t.Fatalf("TS_EXTRA_ARGS=%q", got)
	}
	if container.Env[0].ValueFrom == nil || container.Env[0].ValueFrom.SecretKeyRef == nil ||
		container.Env[0].ValueFrom.SecretKeyRef.Name != "bootstrap" {
		t.Fatal("TS_AUTHKEY does not reference the bootstrap Secret")
	}
	if len(pod.Spec.Volumes) != 1 || pod.Spec.Volumes[0].EmptyDir == nil {
		t.Fatal("tailscaled state must use an emptyDir scoped to the Pod")
	}
}

func TestNamesAreStableDNSLabels(t *testing.T) {
	name := strings.Repeat("connector-", 20)
	first := ConnectorLabelValue(name)
	second := ConnectorLabelValue(name)
	if first != second {
		t.Fatalf("connector label is not stable: %q != %q", first, second)
	}
	if len(first) > 63 {
		t.Fatalf("connector label length %d exceeds 63", len(first))
	}
	if got := InstanceGenerateName(name, 4); len(got) > 58 || got[len(got)-1] != '-' {
		t.Fatalf("invalid GenerateName %q", got)
	}
}

func workloadEnv(values []corev1.EnvVar, name string) string {
	for _, value := range values {
		if value.Name == name {
			return value.Value
		}
	}
	return ""
}
