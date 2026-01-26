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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
)

var _ = Describe("AuthKey Controller", func() {
	const (
		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When reconciling an AuthKey resource", func() {
		const authKeyName = "test-authkey"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      authKeyName,
			Namespace: "default",
		}

		AfterEach(func() {
			// Clean up AuthKey
			authKey := &kodiakv1alpha1.AuthKey{}
			err := k8sClient.Get(ctx, typeNamespacedName, authKey)
			if err == nil {
				// Remove finalizer for cleanup
				authKey.Finalizers = nil
				_ = k8sClient.Update(ctx, authKey)
				Expect(k8sClient.Delete(ctx, authKey)).To(Succeed())
				Eventually(func() bool {
					err := k8sClient.Get(ctx, typeNamespacedName, authKey)
					return errors.IsNotFound(err)
				}, timeout, interval).Should(BeTrue())
			}

			// Clean up Tailnet
			tailnet := &kodiakv1alpha1.Tailnet{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: "test-tailnet", Namespace: "default"}, tailnet)
			if err == nil {
				tailnet.Finalizers = nil
				_ = k8sClient.Update(ctx, tailnet)
				_ = k8sClient.Delete(ctx, tailnet)
			}

			// Clean up ControlServer
			cs := &kodiakv1alpha1.ControlServer{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: "test-controlserver", Namespace: "default"}, cs)
			if err == nil {
				cs.Finalizers = nil
				_ = k8sClient.Update(ctx, cs)
				_ = k8sClient.Delete(ctx, cs)
			}

			// Clean up Secrets
			for _, secretName := range []string{"test-controlserver-admin-key", "authkey-" + authKeyName} {
				secret := &corev1.Secret{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: "default"}, secret)
				if err == nil {
					_ = k8sClient.Delete(ctx, secret)
				}
			}
		})

		It("should add finalizer to the resource", func() {
			By("Creating an AuthKey resource")
			authKey := &kodiakv1alpha1.AuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      authKeyName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.AuthKeySpec{
					TailnetRef: corev1.LocalObjectReference{
						Name: "test-tailnet",
					},
				},
			}
			Expect(k8sClient.Create(ctx, authKey)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &AuthKeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeTrue())

			By("Checking that finalizer was added")
			updated := &kodiakv1alpha1.AuthKey{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Finalizers).To(ContainElement(kodiakFinalizer))
		})

		It("should set Pending status when referenced tailnet does not exist", func() {
			By("Creating an AuthKey referencing a non-existent tailnet")
			authKey := &kodiakv1alpha1.AuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      authKeyName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.AuthKeySpec{
					TailnetRef: corev1.LocalObjectReference{
						Name: "non-existent-tailnet",
					},
				},
			}
			Expect(k8sClient.Create(ctx, authKey)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &AuthKeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// First reconcile adds finalizer
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			if result.Requeue {
				// Second reconcile processes the resource
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Checking the status is Pending")
			updated := &kodiakv1alpha1.AuthKey{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal("Pending"))
			Expect(updated.Status.Ready).To(BeFalse())
			Expect(updated.Status.Conditions).To(HaveLen(1))
			Expect(updated.Status.Conditions[0].Reason).To(Equal(reasonTailnetNotFound))
		})

		It("should set Pending status when referenced tailnet is not ready", func() {
			By("Creating a Tailnet that is not ready")
			tailnet := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-tailnet",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "test-tailnet",
					ControlServerRef: corev1.LocalObjectReference{
						Name: "test-controlserver",
					},
				},
			}
			Expect(k8sClient.Create(ctx, tailnet)).To(Succeed())
			// Tailnet status not set, so Ready=false and TailnetID=0

			By("Creating an AuthKey referencing the not-ready tailnet")
			authKey := &kodiakv1alpha1.AuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      authKeyName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.AuthKeySpec{
					TailnetRef: corev1.LocalObjectReference{
						Name: "test-tailnet",
					},
				},
			}
			Expect(k8sClient.Create(ctx, authKey)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &AuthKeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// First reconcile adds finalizer
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			if result.Requeue {
				// Second reconcile processes the resource
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Checking the status is Pending")
			updated := &kodiakv1alpha1.AuthKey{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal("Pending"))
			Expect(updated.Status.Ready).To(BeFalse())
			Expect(updated.Status.Conditions[0].Reason).To(Equal(reasonTailnetNotReady))
		})

		It("should set Pending status when admin key secret is missing", func() {
			By("Creating a ControlServer without admin key secret")
			cs := &kodiakv1alpha1.ControlServer{
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
			Expect(k8sClient.Create(ctx, cs)).To(Succeed())

			cs.Status.Phase = "Ready"
			cs.Status.Endpoint = "http://test-controlserver-service.default.svc:8080"
			cs.Status.Ready = true
			Expect(k8sClient.Status().Update(ctx, cs)).To(Succeed())

			By("Creating a Tailnet")
			tailnet := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-tailnet",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "test-tailnet",
					ControlServerRef: corev1.LocalObjectReference{
						Name: "test-controlserver",
					},
				},
			}
			Expect(k8sClient.Create(ctx, tailnet)).To(Succeed())

			tailnet.Status.Ready = true
			tailnet.Status.Phase = "Ready"
			tailnet.Status.TailnetID = 123
			Expect(k8sClient.Status().Update(ctx, tailnet)).To(Succeed())

			By("Creating an AuthKey")
			authKey := &kodiakv1alpha1.AuthKey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      authKeyName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.AuthKeySpec{
					TailnetRef: corev1.LocalObjectReference{
						Name: "test-tailnet",
					},
				},
			}
			Expect(k8sClient.Create(ctx, authKey)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &AuthKeyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// Multiple reconciles
			for i := 0; i < 3; i++ {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Checking the status is Pending due to missing admin key")
			updated := &kodiakv1alpha1.AuthKey{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal("Pending"))
			Expect(updated.Status.Ready).To(BeFalse())
			Expect(updated.Status.Conditions[0].Reason).To(Equal(reasonMissingAdminToken))
		})
	})
})
