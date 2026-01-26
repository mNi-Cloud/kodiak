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

var _ = Describe("ControlServer Webhook", func() {
	var (
		ctx       context.Context
		validator ControlServerCustomValidator
		defaulter ControlServerCustomDefaulter
	)

	BeforeEach(func() {
		ctx = context.Background()
		validator = ControlServerCustomValidator{}
		defaulter = ControlServerCustomDefaulter{}
	})

	Context("When creating ControlServer under Defaulting Webhook", func() {
		It("Should apply default listen addresses", func() {
			obj := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-controlserver",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:latest",
				},
			}

			err := defaulter.Default(ctx, obj)
			Expect(err).NotTo(HaveOccurred())

			Expect(obj.Spec.Config.ListenAddr).To(Equal(":8080"))
			Expect(obj.Spec.Config.MetricsListenAddr).To(Equal(":9091"))
			Expect(obj.Spec.Config.StunListenAddr).To(Equal(":3478"))
		})

		It("Should not override existing listen addresses", func() {
			obj := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-controlserver",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:latest",
					Config: kodiakv1alpha1.ControlServerConfig{
						ListenAddr:        ":9000",
						MetricsListenAddr: ":9092",
						StunListenAddr:    ":3479",
					},
				},
			}

			err := defaulter.Default(ctx, obj)
			Expect(err).NotTo(HaveOccurred())

			Expect(obj.Spec.Config.ListenAddr).To(Equal(":9000"))
			Expect(obj.Spec.Config.MetricsListenAddr).To(Equal(":9092"))
			Expect(obj.Spec.Config.StunListenAddr).To(Equal(":3479"))
		})
	})

	Context("When validating ControlServer", func() {
		It("Should deny creation if image is missing", func() {
			obj := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-controlserver",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					// Image is missing
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("image must be specified"))
		})

		It("Should admit creation with valid configuration", func() {
			obj := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-controlserver",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:latest",
					Config: kodiakv1alpha1.ControlServerConfig{
						TLS: &kodiakv1alpha1.TLSConfig{
							Disable: true,
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should deny creation if postgres type without URL or URLSecretRef", func() {
			obj := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-controlserver",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:latest",
					Config: kodiakv1alpha1.ControlServerConfig{
						Database: kodiakv1alpha1.DatabaseConfig{
							Type: "postgres",
							// URL and URLSecretRef are missing
						},
						TLS: &kodiakv1alpha1.TLSConfig{
							Disable: true,
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("either url or urlSecretRef must be specified when type is postgres"))
		})

		It("Should admit creation with postgres type and URL", func() {
			obj := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-controlserver",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:latest",
					Config: kodiakv1alpha1.ControlServerConfig{
						Database: kodiakv1alpha1.DatabaseConfig{
							Type: "postgres",
							URL:  "postgres://user:pass@localhost:5432/ionscale",
						},
						TLS: &kodiakv1alpha1.TLSConfig{
							Disable: true,
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should admit creation with postgres type and URLSecretRef", func() {
			obj := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-controlserver",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:latest",
					Config: kodiakv1alpha1.ControlServerConfig{
						Database: kodiakv1alpha1.DatabaseConfig{
							Type: "postgres",
							URLSecretRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "postgres-secret",
								},
								Key: "url",
							},
						},
						TLS: &kodiakv1alpha1.TLSConfig{
							Disable: true,
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should deny creation with both URL and URLSecretRef", func() {
			obj := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-controlserver",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:latest",
					Config: kodiakv1alpha1.ControlServerConfig{
						Database: kodiakv1alpha1.DatabaseConfig{
							Type: "postgres",
							URL:  "postgres://user:pass@localhost:5432/ionscale",
							URLSecretRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "postgres-secret",
								},
								Key: "url",
							},
						},
						TLS: &kodiakv1alpha1.TLSConfig{
							Disable: true,
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cannot specify both url and urlSecretRef"))
		})

		It("Should deny creation if TLS enabled without cert configuration", func() {
			obj := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-controlserver",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:latest",
					Config: kodiakv1alpha1.ControlServerConfig{
						TLS: &kodiakv1alpha1.TLSConfig{
							Disable: false,
							// No ACME, cert secret, or cert files
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("must specify either acme, certSecretName, or both certFile and keyFile"))
		})

		It("Should admit creation with TLS and ACME enabled", func() {
			obj := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-controlserver",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:latest",
					Config: kodiakv1alpha1.ControlServerConfig{
						TLS: &kodiakv1alpha1.TLSConfig{
							Disable:     false,
							AcmeEnabled: true,
							AcmeEmail:   "admin@example.com",
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should admit creation with TLS and cert secret", func() {
			obj := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-controlserver",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:latest",
					Config: kodiakv1alpha1.ControlServerConfig{
						TLS: &kodiakv1alpha1.TLSConfig{
							Disable:        false,
							CertSecretName: "tls-secret",
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should admit creation with TLS and cert files", func() {
			obj := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-controlserver",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:latest",
					Config: kodiakv1alpha1.ControlServerConfig{
						TLS: &kodiakv1alpha1.TLSConfig{
							Disable:  false,
							CertFile: "/etc/ionscale/tls/tls.crt",
							KeyFile:  "/etc/ionscale/tls/tls.key",
						},
					},
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should return warnings when TLS is disabled", func() {
			obj := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-controlserver",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:latest",
					Config: kodiakv1alpha1.ControlServerConfig{
						TLS: &kodiakv1alpha1.TLSConfig{
							Disable: true,
						},
					},
				},
			}

			warnings, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(ContainElement(ContainSubstring("TLS is disabled")))
		})

		It("Should return warnings when using plain-text database URL", func() {
			obj := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-controlserver",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:latest",
					Config: kodiakv1alpha1.ControlServerConfig{
						Database: kodiakv1alpha1.DatabaseConfig{
							Type: "postgres",
							URL:  "postgres://user:pass@localhost:5432/ionscale",
						},
						TLS: &kodiakv1alpha1.TLSConfig{
							Disable: true,
						},
					},
				},
			}

			warnings, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(ContainElement(ContainSubstring("plain-text database URL")))
		})

		It("Should validate updates correctly", func() {
			oldObj := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-controlserver",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:v1.0.0",
					Config: kodiakv1alpha1.ControlServerConfig{
						TLS: &kodiakv1alpha1.TLSConfig{
							Disable: true,
						},
					},
				},
			}

			newObj := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-controlserver",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:v1.1.0",
					Config: kodiakv1alpha1.ControlServerConfig{
						TLS: &kodiakv1alpha1.TLSConfig{
							Disable: true,
						},
					},
				},
			}

			_, err := validator.ValidateUpdate(ctx, oldObj, newObj)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
