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
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
)

var _ = Describe("Connector Controller", func() {
	const (
		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When reconciling a Connector resource", func() {
		const connectorName = "test-connector"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      connectorName,
			Namespace: "default",
		}

		AfterEach(func() {
			// Clean up Connector
			connector := &kodiakv1alpha1.Connector{}
			err := k8sClient.Get(ctx, typeNamespacedName, connector)
			if err == nil {
				// Remove finalizer for cleanup
				connector.Finalizers = nil
				_ = k8sClient.Update(ctx, connector)
				Expect(k8sClient.Delete(ctx, connector)).To(Succeed())
				Eventually(func() bool {
					err := k8sClient.Get(ctx, typeNamespacedName, connector)
					return errors.IsNotFound(err)
				}, timeout, interval).Should(BeTrue())
			}

			// Clean up Deployment
			deploy := &appsv1.Deployment{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: connectorName + "-ts-connector", Namespace: "default"}, deploy)
			if err == nil {
				_ = k8sClient.Delete(ctx, deploy)
			}

			// Clean up Secrets
			for _, secretName := range []string{"test-auth-secret"} {
				secret := &corev1.Secret{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: "default"}, secret)
				if err == nil {
					_ = k8sClient.Delete(ctx, secret)
				}
			}
		})

		It("should add finalizer to the resource", func() {
			By("Creating a Connector resource")
			connector := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      connectorName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKey:          "test-key",
							ControlServerUrl: "https://controlplane.example.com",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, connector)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &ConnectorReconciler{
				Client:           k8sClient,
				Scheme:           k8sClient.Scheme(),
				IonscaleEndpoint: "http://ionscale.test.svc:8080",
				IonscaleAdminKey: "test-admin-key",
			}

			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeTrue())

			By("Checking that finalizer was added")
			updated := &kodiakv1alpha1.Connector{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Finalizers).To(ContainElement("kodiak.mnicloud.jp/finalizer"))
		})

		It("should create Deployment when auth key is provided via authKeySecretRef", func() {
			By("Creating auth key secret")
			authSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-auth-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"TS_AUTH_KEY": []byte("tskey-auth-abc123"),
				},
			}
			Expect(k8sClient.Create(ctx, authSecret)).To(Succeed())

			By("Creating a Connector with authKeySecretRef")
			connector := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      connectorName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKeySecretRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "test-auth-secret",
								},
								Key: "TS_AUTH_KEY",
							},
							ControlServerUrl: "https://controlplane.example.com",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, connector)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &ConnectorReconciler{
				Client:           k8sClient,
				Scheme:           k8sClient.Scheme(),
				IonscaleEndpoint: "http://ionscale.test.svc:8080",
				IonscaleAdminKey: "test-admin-key",
			}

			// Multiple reconciles to add finalizer and create deployment
			for i := 0; i < 3; i++ {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Checking that Deployment was created")
			deploy := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      connectorName + "-ts-connector",
					Namespace: "default",
				}, deploy)
			}, timeout, interval).Should(Succeed())

			Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(deploy.Spec.Template.Spec.Containers[0].Image).To(ContainSubstring("tailscale/tailscale"))

			// Check that env vars include auth key from secret
			var authKeyEnv *corev1.EnvVar
			for i := range deploy.Spec.Template.Spec.Containers[0].Env {
				if deploy.Spec.Template.Spec.Containers[0].Env[i].Name == "TS_AUTH_KEY" {
					authKeyEnv = &deploy.Spec.Template.Spec.Containers[0].Env[i]
					break
				}
			}
			Expect(authKeyEnv).NotTo(BeNil())
			Expect(authKeyEnv.ValueFrom).NotTo(BeNil())
			Expect(authKeyEnv.ValueFrom.SecretKeyRef.Name).To(Equal("test-auth-secret"))
		})

		It("should create Deployment with inline auth key", func() {
			By("Creating a Connector with inline authKey")
			connector := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      connectorName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKey:          "tskey-auth-inline123",
							ControlServerUrl: "https://controlplane.example.com",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, connector)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &ConnectorReconciler{
				Client:           k8sClient,
				Scheme:           k8sClient.Scheme(),
				IonscaleEndpoint: "http://ionscale.test.svc:8080",
				IonscaleAdminKey: "test-admin-key",
			}

			// Multiple reconciles
			for i := 0; i < 3; i++ {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Checking that Deployment was created with inline auth key")
			deploy := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      connectorName + "-ts-connector",
					Namespace: "default",
				}, deploy)
			}, timeout, interval).Should(Succeed())

			// Check that env vars include inline auth key
			var authKeyEnv *corev1.EnvVar
			for i := range deploy.Spec.Template.Spec.Containers[0].Env {
				if deploy.Spec.Template.Spec.Containers[0].Env[i].Name == "TS_AUTH_KEY" {
					authKeyEnv = &deploy.Spec.Template.Spec.Containers[0].Env[i]
					break
				}
			}
			Expect(authKeyEnv).NotTo(BeNil())
			Expect(authKeyEnv.Value).To(Equal("tskey-auth-inline123"))
		})

		It("should configure advertised routes in Deployment", func() {
			By("Creating a Connector with advertised routes")
			connector := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      connectorName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKey:          "test-key",
							ControlServerUrl: "https://controlplane.example.com",
							AdvertiseRoutes:  []string{"10.0.0.0/8", "192.168.0.0/16"},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, connector)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &ConnectorReconciler{
				Client:           k8sClient,
				Scheme:           k8sClient.Scheme(),
				IonscaleEndpoint: "http://ionscale.test.svc:8080",
				IonscaleAdminKey: "test-admin-key",
			}

			// Multiple reconciles
			for i := 0; i < 3; i++ {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Checking that Deployment has routes configured")
			deploy := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      connectorName + "-ts-connector",
					Namespace: "default",
				}, deploy)
			}, timeout, interval).Should(Succeed())

			// Check that env vars include TS_ROUTES
			var routesEnv *corev1.EnvVar
			for i := range deploy.Spec.Template.Spec.Containers[0].Env {
				if deploy.Spec.Template.Spec.Containers[0].Env[i].Name == "TS_ROUTES" {
					routesEnv = &deploy.Spec.Template.Spec.Containers[0].Env[i]
					break
				}
			}
			Expect(routesEnv).NotTo(BeNil())
			Expect(routesEnv.Value).To(Equal("10.0.0.0/8,192.168.0.0/16"))
		})

		It("should use controller's ionscale endpoint when controlServerUrl is not specified", func() {
			By("Creating a Connector without controlServerUrl")
			connector := &kodiakv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      connectorName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ConnectorSpec{
					Spec: kodiakv1alpha1.ConnectorSpecSpec{
						Tailscale: kodiakv1alpha1.TailscaleConfig{
							AuthKey: "test-key",
							// No ControlServerUrl specified - should use controller's default
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, connector)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &ConnectorReconciler{
				Client:           k8sClient,
				Scheme:           k8sClient.Scheme(),
				IonscaleEndpoint: "http://ionscale.kodiak-system.svc:8080",
				IonscaleAdminKey: "test-admin-key",
			}

			// Multiple reconciles
			for i := 0; i < 3; i++ {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Checking that Deployment was created with login server from controller's ionscale endpoint")
			deploy := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      connectorName + "-ts-connector",
					Namespace: "default",
				}, deploy)
			}, timeout, interval).Should(Succeed())

			// Check that env vars include TS_EXTRA_ARGS with login server
			var extraArgsEnv *corev1.EnvVar
			for i := range deploy.Spec.Template.Spec.Containers[0].Env {
				if deploy.Spec.Template.Spec.Containers[0].Env[i].Name == "TS_EXTRA_ARGS" {
					extraArgsEnv = &deploy.Spec.Template.Spec.Containers[0].Env[i]
					break
				}
			}
			Expect(extraArgsEnv).NotTo(BeNil())
			Expect(extraArgsEnv.Value).To(ContainSubstring("--login-server=http://ionscale.kodiak-system.svc:8080"))
		})
	})
})
