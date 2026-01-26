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

var _ = Describe("ControlServer Controller", func() {
	const (
		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When creating a ControlServer resource", func() {
		It("should create ConfigMap, Service, and Deployment", func() {
			const resourceName = "test-cs-basic"
			ctx := context.Background()

			typeNamespacedName := types.NamespacedName{
				Name:      resourceName,
				Namespace: "default",
			}

			By("Creating the ControlServer resource")
			resource := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:latest",
					Config: kodiakv1alpha1.ControlServerConfig{
						ListenAddr:        ":8080",
						MetricsListenAddr: ":9091",
						StunListenAddr:    ":3478",
						TLS: &kodiakv1alpha1.TLSConfig{
							Disable: true,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			DeferCleanup(func() {
				resource := &kodiakv1alpha1.ControlServer{}
				err := k8sClient.Get(ctx, typeNamespacedName, resource)
				if err == nil {
					resource.Finalizers = nil
					_ = k8sClient.Update(ctx, resource)
					_ = k8sClient.Delete(ctx, resource)
				}
			})

			By("Reconciling the resource")
			reconciler := &ControlServerReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// Reconcile multiple times to add finalizer and create resources
			for i := 0; i < 3; i++ {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Checking that ConfigMap was created")
			configMap := &corev1.ConfigMap{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName + "-config",
					Namespace: "default",
				}, configMap)
			}, timeout, interval).Should(Succeed())

			Expect(configMap.Data).To(HaveKey("config.yaml"))
			Expect(configMap.Data["config.yaml"]).To(ContainSubstring("listen_addr"))

			By("Checking that Service was created")
			service := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName + "-service",
					Namespace: "default",
				}, service)
			}, timeout, interval).Should(Succeed())

			Expect(service.Spec.Ports).To(HaveLen(3))

			By("Checking that Deployment was created")
			deployment := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName + "-controller",
					Namespace: "default",
				}, deployment)
			}, timeout, interval).Should(Succeed())

			Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal("ghcr.io/jsiebens/ionscale:latest"))
		})

		It("should create PVC when storage is specified", func() {
			const resourceName = "test-cs-storage"
			ctx := context.Background()

			typeNamespacedName := types.NamespacedName{
				Name:      resourceName,
				Namespace: "default",
			}

			By("Creating the ControlServer resource with storage")
			resource := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:latest",
					Config: kodiakv1alpha1.ControlServerConfig{
						TLS: &kodiakv1alpha1.TLSConfig{
							Disable: true,
						},
					},
					Storage: &kodiakv1alpha1.StorageConfig{
						Size: "5Gi",
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			DeferCleanup(func() {
				resource := &kodiakv1alpha1.ControlServer{}
				err := k8sClient.Get(ctx, typeNamespacedName, resource)
				if err == nil {
					resource.Finalizers = nil
					_ = k8sClient.Update(ctx, resource)
					_ = k8sClient.Delete(ctx, resource)
				}
			})

			By("Reconciling the resource")
			reconciler := &ControlServerReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// Reconcile multiple times to add finalizer and create resources
			for i := 0; i < 3; i++ {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Checking that PVC was created")
			pvc := &corev1.PersistentVolumeClaim{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName + "-data",
					Namespace: "default",
				}, pvc)
			}, timeout, interval).Should(Succeed())

			Expect(pvc.Spec.Resources.Requests.Storage().String()).To(Equal("5Gi"))
		})

		It("should create default PVC (10Gi) when storage is not specified", func() {
			const resourceName = "test-cs-default-pvc"
			ctx := context.Background()

			typeNamespacedName := types.NamespacedName{
				Name:      resourceName,
				Namespace: "default",
			}

			By("Creating the ControlServer resource without storage")
			resource := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
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
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			DeferCleanup(func() {
				resource := &kodiakv1alpha1.ControlServer{}
				err := k8sClient.Get(ctx, typeNamespacedName, resource)
				if err == nil {
					resource.Finalizers = nil
					_ = k8sClient.Update(ctx, resource)
					_ = k8sClient.Delete(ctx, resource)
				}
			})

			By("Reconciling the resource")
			reconciler := &ControlServerReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// Reconcile multiple times to add finalizer and create resources
			for i := 0; i < 3; i++ {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Checking that default PVC was created with 10Gi")
			pvc := &corev1.PersistentVolumeClaim{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName + "-data",
					Namespace: "default",
				}, pvc)
			}, timeout, interval).Should(Succeed())

			Expect(pvc.Spec.Resources.Requests.Storage().String()).To(Equal("10Gi"))

			By("Checking that Deployment uses PVC volume")
			deployment := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName + "-controller",
					Namespace: "default",
				}, deployment)
			}, timeout, interval).Should(Succeed())

			var dataVolume *corev1.Volume
			for i := range deployment.Spec.Template.Spec.Volumes {
				if deployment.Spec.Template.Spec.Volumes[i].Name == "data" {
					dataVolume = &deployment.Spec.Template.Spec.Volumes[i]
					break
				}
			}

			Expect(dataVolume).NotTo(BeNil())
			Expect(dataVolume.PersistentVolumeClaim).NotTo(BeNil())
			Expect(dataVolume.PersistentVolumeClaim.ClaimName).To(Equal(resourceName + "-data"))
		})

		It("should add finalizer to the resource", func() {
			const resourceName = "test-cs-finalizer"
			ctx := context.Background()

			typeNamespacedName := types.NamespacedName{
				Name:      resourceName,
				Namespace: "default",
			}

			By("Creating the ControlServer resource")
			resource := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
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
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			DeferCleanup(func() {
				resource := &kodiakv1alpha1.ControlServer{}
				err := k8sClient.Get(ctx, typeNamespacedName, resource)
				if err == nil {
					resource.Finalizers = nil
					_ = k8sClient.Update(ctx, resource)
					_ = k8sClient.Delete(ctx, resource)
				}
			})

			By("Reconciling the resource multiple times to add finalizer")
			reconciler := &ControlServerReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// First reconcile adds finalizer
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			if result.Requeue {
				// Second reconcile continues after finalizer was added
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Checking that finalizer was added")
			updatedResource := &kodiakv1alpha1.ControlServer{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updatedResource)).To(Succeed())
			Expect(updatedResource.Finalizers).To(ContainElement(kodiakFinalizer))
		})

		It("should update status correctly", func() {
			const resourceName = "test-cs-status"
			ctx := context.Background()

			typeNamespacedName := types.NamespacedName{
				Name:      resourceName,
				Namespace: "default",
			}

			By("Creating the ControlServer resource")
			resource := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
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
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			DeferCleanup(func() {
				resource := &kodiakv1alpha1.ControlServer{}
				err := k8sClient.Get(ctx, typeNamespacedName, resource)
				if err == nil {
					resource.Finalizers = nil
					_ = k8sClient.Update(ctx, resource)
					_ = k8sClient.Delete(ctx, resource)
				}
			})

			By("Reconciling the resource")
			reconciler := &ControlServerReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// Reconcile multiple times to ensure all resources are created
			for i := 0; i < 3; i++ {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Checking that status was updated")
			updatedResource := &kodiakv1alpha1.ControlServer{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updatedResource)).To(Succeed())

			Expect(updatedResource.Status.Endpoint).NotTo(BeEmpty())
			Expect(updatedResource.Status.Phase).To(Or(Equal("Ready"), Equal("Progressing")))
			Expect(updatedResource.Status.Conditions).NotTo(BeEmpty())
		})
	})

	Context("When deleting a ControlServer resource", func() {
		It("should clean up managed resources", func() {
			const resourceName = "test-cs-delete"
			ctx := context.Background()

			typeNamespacedName := types.NamespacedName{
				Name:      resourceName,
				Namespace: "default",
			}

			By("Creating the ControlServer resource")
			resource := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.ControlServerSpec{
					Image: "ghcr.io/jsiebens/ionscale:latest",
					Config: kodiakv1alpha1.ControlServerConfig{
						TLS: &kodiakv1alpha1.TLSConfig{
							Disable: true,
						},
					},
					Storage: &kodiakv1alpha1.StorageConfig{
						Size: "5Gi",
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			By("Reconciling to create all resources")
			reconciler := &ControlServerReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// Reconcile multiple times
			for i := 0; i < 3; i++ {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Verifying resources were created")
			configMap := &corev1.ConfigMap{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName + "-config",
					Namespace: "default",
				}, configMap)
			}, timeout, interval).Should(Succeed())

			By("Deleting the ControlServer resource")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			By("Reconciling to handle deletion")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the resource was deleted")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, resource)
				return errors.IsNotFound(err)
			}, timeout, interval).Should(BeTrue())
		})
	})
})
