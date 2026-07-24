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

package controller

import (
	"testing"

	pb "github.com/jsiebens/ionscale/pkg/gen/ionscale/v1"
	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFindConnectorMachinePrefersCurrentIP(t *testing.T) {
	oldMachine := &pb.Machine{
		Id:   1,
		Name: "security-labo-node",
		Ipv4: "100.87.249.1",
	}
	currentMachine := &pb.Machine{
		Id:   2,
		Name: "security-labo-node-1",
		Ipv4: "100.109.209.84",
	}
	connector := &kodiakv1alpha1.Connector{
		ObjectMeta: metav1.ObjectMeta{Name: "security-labo-connector"},
		Spec: kodiakv1alpha1.ConnectorSpec{
			Spec: kodiakv1alpha1.ConnectorSpecSpec{
				Tailscale: kodiakv1alpha1.TailscaleConfig{
					Hostname: "security-labo-node",
				},
			},
		},
		Status: kodiakv1alpha1.ConnectorStatus{
			TailscaleIP: "100.109.209.84",
		},
	}

	got := findConnectorMachine([]*pb.Machine{oldMachine, currentMachine}, connector)
	if got != currentMachine {
		t.Fatalf("findConnectorMachine() = %v, want current machine %v", got, currentMachine)
	}
}

func TestFindConnectorMachineFallsBackToHostname(t *testing.T) {
	expected := &pb.Machine{
		Id:   1,
		Name: "security-labo-node",
		Ipv4: "100.87.249.1",
	}
	connector := &kodiakv1alpha1.Connector{
		Spec: kodiakv1alpha1.ConnectorSpec{
			Spec: kodiakv1alpha1.ConnectorSpecSpec{
				Tailscale: kodiakv1alpha1.TailscaleConfig{
					Hostname: "security-labo-node",
				},
			},
		},
	}

	got := findConnectorMachine([]*pb.Machine{expected}, connector)
	if got != expected {
		t.Fatalf("findConnectorMachine() = %v, want hostname match %v", got, expected)
	}
}
