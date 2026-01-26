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

var _ = Describe("AuthKey Webhook", func() {
	var (
		ctx       context.Context
		validator AuthKeyCustomValidator
		defaulter AuthKeyCustomDefaulter
	)

	BeforeEach(func() {
		ctx = context.Background()
		validator = AuthKeyCustomValidator{}
		defaulter = AuthKeyCustomDefaulter{}
	})

	Context("When creating AuthKey under Defaulting Webhook", func() {
		It("Should apply default expiry when not specified", func() {
			obj := &kodiakv1alpha1.AuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-authkey",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.AuthKeySpec{
					TailnetRef: corev1.LocalObjectReference{
						Name: "my-tailnet",
					},
					// Expiry not specified
				},
			}

			err := defaulter.Default(ctx, obj)
			Expect(err).NotTo(HaveOccurred())

			Expect(obj.Spec.Expiry).To(Equal("24h"))
		})

		It("Should not override existing expiry", func() {
			obj := &kodiakv1alpha1.AuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-authkey",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.AuthKeySpec{
					TailnetRef: corev1.LocalObjectReference{
						Name: "my-tailnet",
					},
					Expiry: "48h",
				},
			}

			err := defaulter.Default(ctx, obj)
			Expect(err).NotTo(HaveOccurred())

			Expect(obj.Spec.Expiry).To(Equal("48h"))
		})
	})

	Context("When validating AuthKey", func() {
		It("Should deny creation if tailnetRef.name is missing", func() {
			obj := &kodiakv1alpha1.AuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-authkey",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.AuthKeySpec{
					// TailnetRef.Name is missing
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tailnetRef"))
		})

		It("Should admit creation with valid configuration", func() {
			obj := &kodiakv1alpha1.AuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-authkey",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.AuthKeySpec{
					TailnetRef: corev1.LocalObjectReference{
						Name: "my-tailnet",
					},
					Expiry: "24h",
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should admit creation with ephemeral and preAuthorized flags", func() {
			obj := &kodiakv1alpha1.AuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-authkey",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.AuthKeySpec{
					TailnetRef: corev1.LocalObjectReference{
						Name: "my-tailnet",
					},
					Ephemeral:     true,
					PreAuthorized: true,
					Tags:          []string{"tag:server", "tag:prod"},
					Expiry:        "168h",
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should deny creation with invalid expiry format", func() {
			obj := &kodiakv1alpha1.AuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-authkey",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.AuthKeySpec{
					TailnetRef: corev1.LocalObjectReference{
						Name: "my-tailnet",
					},
					Expiry: "not-a-duration",
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid duration"))
		})

		It("Should deny creation with negative expiry", func() {
			obj := &kodiakv1alpha1.AuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-authkey",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.AuthKeySpec{
					TailnetRef: corev1.LocalObjectReference{
						Name: "my-tailnet",
					},
					Expiry: "-24h",
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("positive duration"))
		})

		It("Should deny creation with zero expiry", func() {
			obj := &kodiakv1alpha1.AuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-authkey",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.AuthKeySpec{
					TailnetRef: corev1.LocalObjectReference{
						Name: "my-tailnet",
					},
					Expiry: "0s",
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("positive duration"))
		})

		It("Should admit creation with various valid duration formats", func() {
			durations := []string{"1h", "30m", "7d", "168h", "1h30m", "90s"}

			for _, duration := range durations {
				obj := &kodiakv1alpha1.AuthKey{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-authkey",
						Namespace: "default",
					},
					Spec: kodiakv1alpha1.AuthKeySpec{
						TailnetRef: corev1.LocalObjectReference{
							Name: "my-tailnet",
						},
						Expiry: duration,
					},
				}

				_, err := validator.ValidateCreate(ctx, obj)
				// Note: "7d" is not a valid Go duration format, it should fail
				if duration == "7d" {
					Expect(err).To(HaveOccurred(), "expected 7d to fail as Go doesn't support day units")
				} else {
					Expect(err).NotTo(HaveOccurred(), "expected %s to be valid", duration)
				}
			}
		})

		It("Should validate updates correctly", func() {
			oldObj := &kodiakv1alpha1.AuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-authkey",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.AuthKeySpec{
					TailnetRef: corev1.LocalObjectReference{
						Name: "my-tailnet",
					},
					Expiry: "24h",
				},
			}

			newObj := &kodiakv1alpha1.AuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-authkey",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.AuthKeySpec{
					TailnetRef: corev1.LocalObjectReference{
						Name: "my-tailnet",
					},
					Expiry: "48h",
				},
			}

			_, err := validator.ValidateUpdate(ctx, oldObj, newObj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should deny update with invalid expiry", func() {
			oldObj := &kodiakv1alpha1.AuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-authkey",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.AuthKeySpec{
					TailnetRef: corev1.LocalObjectReference{
						Name: "my-tailnet",
					},
					Expiry: "24h",
				},
			}

			newObj := &kodiakv1alpha1.AuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-authkey",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.AuthKeySpec{
					TailnetRef: corev1.LocalObjectReference{
						Name: "my-tailnet",
					},
					Expiry: "invalid",
				},
			}

			_, err := validator.ValidateUpdate(ctx, oldObj, newObj)
			Expect(err).To(HaveOccurred())
		})
	})
})
