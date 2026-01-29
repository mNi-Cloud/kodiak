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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
)

var _ = Describe("Connector Webhook", func() {
	var (
		ctx       context.Context
		validator ConnectorCustomValidator
		defaulter ConnectorCustomDefaulter
	)

	BeforeEach(func() {
		ctx = context.Background()
		validator = ConnectorCustomValidator{}
		defaulter = ConnectorCustomDefaulter{}
	})

	Context("When creating Connector under Defaulting Webhook", func() {
		It("Should not modify the object as no defaults are needed", func() {
			obj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKeyRef: &corev1.LocalObjectReference{
								Name: "my-authkey",
							},
						},
					},
				},
			}

			err := defaulter.Default(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("When validating Connector", func() {
		It("Should deny creation if no auth key source is specified", func() {
			obj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							// No auth key source specified
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("authKey"))
		})

		It("Should admit creation with authKey", func() {
			obj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKey: "tskey-auth-xxxxx",
						},
					},
				},
			}

			warnings, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
			// Should have warning about inline auth key
			Expect(warnings).To(ContainElement(ContainSubstring("inline authKey")))
		})

		It("Should admit creation with authKeySecretRef", func() {
			obj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKeySecretRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "my-secret",
								},
								Key: "auth-key",
							},
						},
					},
				},
			}

			warnings, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
			// Should not have warning about inline auth key
			Expect(warnings).NotTo(ContainElement(ContainSubstring("inline authKey")))
		})

		It("Should admit creation with authKeyRef", func() {
			obj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKeyRef: &corev1.LocalObjectReference{
								Name: "my-authkey",
							},
						},
					},
				},
			}

			warnings, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
			// Should not have warning about inline auth key
			Expect(warnings).NotTo(ContainElement(ContainSubstring("inline authKey")))
		})

		It("Should deny creation with both authKey and authKeySecretRef", func() {
			obj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKey: "tskey-auth-xxxxx",
							AuthKeySecretRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "my-secret",
								},
								Key: "auth-key",
							},
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only one of"))
		})

		It("Should deny creation with both authKey and authKeyRef", func() {
			obj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKey: "tskey-auth-xxxxx",
							AuthKeyRef: &corev1.LocalObjectReference{
								Name: "my-authkey",
							},
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only one of"))
		})

		It("Should deny creation with all three auth key sources", func() {
			obj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKey: "tskey-auth-xxxxx",
							AuthKeySecretRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "my-secret",
								},
								Key: "auth-key",
							},
							AuthKeyRef: &corev1.LocalObjectReference{
								Name: "my-authkey",
							},
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only one of"))
		})

		It("Should admit creation with valid advertiseRoutes", func() {
			obj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKeyRef: &corev1.LocalObjectReference{
								Name: "my-authkey",
							},
							AdvertiseRoutes: []string{
								"10.0.0.0/8",
								"192.168.1.0/24",
								"172.16.0.0/12",
							},
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should deny creation with invalid CIDR in advertiseRoutes", func() {
			obj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKeyRef: &corev1.LocalObjectReference{
								Name: "my-authkey",
							},
							AdvertiseRoutes: []string{
								"not-a-cidr",
							},
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("CIDR"))
		})

		It("Should deny creation with invalid IP address in advertiseRoutes", func() {
			obj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKeyRef: &corev1.LocalObjectReference{
								Name: "my-authkey",
							},
							AdvertiseRoutes: []string{
								"999.999.999.999/24",
							},
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("CIDR"))
		})

		It("Should admit creation with controlServerUrl", func() {
			obj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKeyRef: &corev1.LocalObjectReference{
								Name: "my-authkey",
							},
							ControlServerUrl: "https://controlserver.example.com",
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should admit creation with controlServerRef", func() {
			obj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKeyRef: &corev1.LocalObjectReference{
								Name: "my-authkey",
							},
							ControlServerUrl: "https://controlserver.example.com",
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should validate updates correctly", func() {
			oldObj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKeyRef: &corev1.LocalObjectReference{
								Name: "my-authkey",
							},
							AdvertiseRoutes: []string{"10.0.0.0/8"},
						},
					},
				},
			}

			newObj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKeyRef: &corev1.LocalObjectReference{
								Name: "my-authkey",
							},
							AdvertiseRoutes: []string{"10.0.0.0/8", "192.168.0.0/16"},
						},
					},
				},
			}

			_, err := validator.ValidateUpdate(ctx, oldObj, newObj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should deny update with invalid CIDR", func() {
			oldObj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKeyRef: &corev1.LocalObjectReference{
								Name: "my-authkey",
							},
							AdvertiseRoutes: []string{"10.0.0.0/8"},
						},
					},
				},
			}

			newObj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKeyRef: &corev1.LocalObjectReference{
								Name: "my-authkey",
							},
							AdvertiseRoutes: []string{"invalid-cidr"},
						},
					},
				},
			}

			_, err := validator.ValidateUpdate(ctx, oldObj, newObj)
			Expect(err).To(HaveOccurred())
		})

		It("Should admit creation with full configuration", func() {
			obj := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-connector",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Metadata: &kodiakv1alpha1.ResourceMetadata{
						Labels: map[string]string{
							"app": "test",
						},
					},
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKeyRef: &corev1.LocalObjectReference{
								Name: "my-authkey",
							},
							Version:             "v1.60.0",
							AdvertiseRoutes:     []string{"10.0.0.0/8"},
							UserspaceNetworking: true,
							Hostname:            "my-connector",
							AcceptDNS:           true,
							ControlServerUrl:    "https://controlserver.example.com",
						},
						Resources: kodiakv1alpha1.ResourceRequirements{
							Limits: kodiakv1alpha1.ResourceList{
								CPU:    "500m",
								Memory: "256Mi",
							},
							Requests: kodiakv1alpha1.ResourceList{
								CPU:    "100m",
								Memory: "64Mi",
							},
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
