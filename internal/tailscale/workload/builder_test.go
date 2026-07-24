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

	"github.com/google/go-cmp/cmp"
	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestConnectorWorkloadContract(t *testing.T) {
	connector := &kodiakv1alpha1.Connector{
		ObjectMeta: metav1.ObjectMeta{Name: "vpn", Namespace: "tenant-a"},
		Spec: kodiakv1alpha1.ConnectorSpec{
			Tags: []string{"tag:vpn"},
			SubnetRouter: kodiakv1alpha1.SubnetRouterSpec{
				AdvertiseRoutes: []string{"10.0.1.0/24"},
			},
		},
	}

	role := Role(connector, 2)
	if diff := cmp.Diff(
		[]string{"vpn-connector-0", "vpn-connector-1"},
		role.Rules[0].ResourceNames,
	); diff != "" {
		t.Fatalf("state Secret RBAC mismatch (-want +got):\n%s", diff)
	}

	workload := StatefulSet(connector, "tailscale/tailscale:v1.98.9", "https://vpn.example.test", 2)
	if workload.Spec.ServiceName != "vpn-connector" {
		t.Fatalf("unexpected headless service name %q", workload.Spec.ServiceName)
	}
	if workload.Spec.Replicas == nil || *workload.Spec.Replicas != 2 {
		t.Fatalf("unexpected replicas: %v", workload.Spec.Replicas)
	}
	container := workload.Spec.Template.Spec.Containers[0]
	if got := workloadEnv(container.Env, "TS_USERSPACE"); got != "false" {
		t.Fatalf("TS_USERSPACE=%q, want false", got)
	}
	if got := workloadEnv(container.Env, "TS_KUBE_SECRET"); got != "$(POD_NAME)" {
		t.Fatalf("TS_KUBE_SECRET=%q", got)
	}
	if got := workloadEnv(container.Env, "TS_ROUTES"); got != "10.0.1.0/24" {
		t.Fatalf("TS_ROUTES=%q", got)
	}
	if got := workloadEnv(container.Env, "TS_EXTRA_ARGS"); got != "--snat-subnet-routes=true --stateful-filtering=true --login-server=https://vpn.example.test --advertise-tags=tag:vpn" {
		t.Fatalf("TS_EXTRA_ARGS=%q", got)
	}
}

func TestChildNamesAreStableDNSLabels(t *testing.T) {
	name := strings.Repeat("connector-", 20)
	first := ChildNames(name)
	second := ChildNames(name)
	if first != second {
		t.Fatalf("child names are not stable: %#v != %#v", first, second)
	}
	if len(first.StatefulSet) > 52 {
		t.Fatalf("StatefulSet base name length %d exceeds 52", len(first.StatefulSet))
	}
	if got := ConnectorLabelValue(name); got != first.StatefulSet {
		t.Fatalf("connector label %q != child identity %q", got, first.StatefulSet)
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
