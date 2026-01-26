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
	"errors"
	"time"

	pb "github.com/jsiebens/ionscale/pkg/gen/ionscale/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
	controlclient "github.com/mNi-Cloud/kodiak/internal/client"
)

var _ = Describe("Tailnet Controller", func() {
	const (
		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When ControlServer reference is missing", func() {
		const tailnetName = "test-tailnet-no-ref"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      tailnetName,
			Namespace: "default",
		}

		AfterEach(func() {
			resource := &kodiakv1alpha1.Tailnet{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				By("Cleanup the Tailnet resource")
				resource.Finalizers = nil
				_ = k8sClient.Update(ctx, resource)
				_ = k8sClient.Delete(ctx, resource)
			}
		})

		It("should set Pending status", func() {
			By("Creating a Tailnet without ControlServer reference")
			resource := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tailnetName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "test-tailnet",
					// ControlServerRef is not set
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &TailnetReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// First reconcile adds finalizer
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			if result.Requeue {
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Checking that status is Pending")
			updatedResource := &kodiakv1alpha1.Tailnet{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updatedResource)).To(Succeed())
			Expect(updatedResource.Status.Phase).To(Equal("Pending"))
			Expect(updatedResource.Status.Ready).To(BeFalse())
		})
	})

	Context("When ControlServer is not found", func() {
		const tailnetName = "test-tailnet-missing-cs"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      tailnetName,
			Namespace: "default",
		}

		AfterEach(func() {
			resource := &kodiakv1alpha1.Tailnet{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				resource.Finalizers = nil
				_ = k8sClient.Update(ctx, resource)
				_ = k8sClient.Delete(ctx, resource)
			}
		})

		It("should set Pending status with ControlServerNotFound reason", func() {
			By("Creating a Tailnet with non-existent ControlServer reference")
			resource := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tailnetName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "test-tailnet",
					ControlServerRef: corev1.LocalObjectReference{
						Name: "non-existent-control-server",
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &TailnetReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// First reconcile adds finalizer
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			if result.Requeue {
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Checking that status is Pending with correct reason")
			updatedResource := &kodiakv1alpha1.Tailnet{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updatedResource)).To(Succeed())
			Expect(updatedResource.Status.Phase).To(Equal("Pending"))
			Expect(updatedResource.Status.Ready).To(BeFalse())

			var readyCondition *metav1.Condition
			for i := range updatedResource.Status.Conditions {
				if updatedResource.Status.Conditions[i].Type == "Ready" {
					readyCondition = &updatedResource.Status.Conditions[i]
					break
				}
			}
			Expect(readyCondition).NotTo(BeNil())
			Expect(readyCondition.Reason).To(Equal(reasonControlServerNotFound))
		})
	})

	Context("When ControlServer is not ready", func() {
		const (
			tailnetName       = "test-tailnet-cs-not-ready"
			controlServerName = "test-cs-not-ready"
		)

		ctx := context.Background()

		tailnetNamespacedName := types.NamespacedName{
			Name:      tailnetName,
			Namespace: "default",
		}

		controlServerNamespacedName := types.NamespacedName{
			Name:      controlServerName,
			Namespace: "default",
		}

		AfterEach(func() {
			tailnet := &kodiakv1alpha1.Tailnet{}
			err := k8sClient.Get(ctx, tailnetNamespacedName, tailnet)
			if err == nil {
				tailnet.Finalizers = nil
				_ = k8sClient.Update(ctx, tailnet)
				_ = k8sClient.Delete(ctx, tailnet)
			}

			cs := &kodiakv1alpha1.ControlServer{}
			err = k8sClient.Get(ctx, controlServerNamespacedName, cs)
			if err == nil {
				cs.Finalizers = nil
				_ = k8sClient.Update(ctx, cs)
				_ = k8sClient.Delete(ctx, cs)
			}
		})

		It("should set Pending status with ControlServerNotReady reason", func() {
			By("Creating a ControlServer that is not ready")
			controlServer := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      controlServerName,
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
			Expect(k8sClient.Create(ctx, controlServer)).To(Succeed())

			By("Creating a Tailnet referencing the ControlServer")
			tailnet := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tailnetName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "test-tailnet",
					ControlServerRef: corev1.LocalObjectReference{
						Name: controlServerName,
					},
				},
			}
			Expect(k8sClient.Create(ctx, tailnet)).To(Succeed())

			By("Reconciling the Tailnet")
			reconciler := &TailnetReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			// First reconcile adds finalizer
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: tailnetNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			if result.Requeue {
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: tailnetNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Checking that Tailnet status is Pending")
			updatedTailnet := &kodiakv1alpha1.Tailnet{}
			Expect(k8sClient.Get(ctx, tailnetNamespacedName, updatedTailnet)).To(Succeed())
			Expect(updatedTailnet.Status.Phase).To(Equal("Pending"))
			Expect(updatedTailnet.Status.Ready).To(BeFalse())

			var readyCondition *metav1.Condition
			for i := range updatedTailnet.Status.Conditions {
				if updatedTailnet.Status.Conditions[i].Type == "Ready" {
					readyCondition = &updatedTailnet.Status.Conditions[i]
					break
				}
			}
			Expect(readyCondition).NotTo(BeNil())
			Expect(readyCondition.Reason).To(Equal(reasonControlServerNotReady))
		})
	})

	Context("When successfully creating a Tailnet with mock client", func() {
		const (
			tailnetName       = "test-tailnet-success"
			controlServerName = "test-cs-success"
			adminSecretName   = "test-admin-secret"
		)

		ctx := context.Background()

		tailnetNamespacedName := types.NamespacedName{
			Name:      tailnetName,
			Namespace: "default",
		}

		controlServerNamespacedName := types.NamespacedName{
			Name:      controlServerName,
			Namespace: "default",
		}

		secretNamespacedName := types.NamespacedName{
			Name:      adminSecretName,
			Namespace: "default",
		}

		AfterEach(func() {
			tailnet := &kodiakv1alpha1.Tailnet{}
			err := k8sClient.Get(ctx, tailnetNamespacedName, tailnet)
			if err == nil {
				tailnet.Finalizers = nil
				_ = k8sClient.Update(ctx, tailnet)
				_ = k8sClient.Delete(ctx, tailnet)
			}

			cs := &kodiakv1alpha1.ControlServer{}
			err = k8sClient.Get(ctx, controlServerNamespacedName, cs)
			if err == nil {
				cs.Finalizers = nil
				_ = k8sClient.Update(ctx, cs)
				_ = k8sClient.Delete(ctx, cs)
			}

			secret := &corev1.Secret{}
			err = k8sClient.Get(ctx, secretNamespacedName, secret)
			if err == nil {
				_ = k8sClient.Delete(ctx, secret)
			}
		})

		It("should create Tailnet and set Ready status", func() {
			By("Creating the admin secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      adminSecretName,
					Namespace: "default",
				},
				Data: map[string][]byte{
					"systemAdminKey": []byte("test-admin-key"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			By("Creating a ready ControlServer")
			controlServer := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      controlServerName,
					Namespace: "default",
					Annotations: map[string]string{
						"kodiak.mnicloud.jp/system-admin-key-secret": adminSecretName,
					},
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
			Expect(k8sClient.Create(ctx, controlServer)).To(Succeed())

			controlServer.Status.Ready = true
			controlServer.Status.Endpoint = "http://test-cs-success-service.default.svc.cluster.local:8080"
			controlServer.Status.Phase = "Ready"
			Expect(k8sClient.Status().Update(ctx, controlServer)).To(Succeed())

			By("Creating a Tailnet")
			tailnet := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tailnetName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "test-tailnet",
					ControlServerRef: corev1.LocalObjectReference{
						Name: controlServerName,
					},
				},
			}
			Expect(k8sClient.Create(ctx, tailnet)).To(Succeed())

			By("Setting up mock client")
			mockClient := &controlclient.MockControlServerClient{
				ListTailnetsFunc: func(ctx context.Context) ([]*pb.Tailnet, error) {
					return []*pb.Tailnet{}, nil
				},
				CreateTailnetFunc: func(ctx context.Context, req *pb.CreateTailnetRequest) (*pb.Tailnet, error) {
					return &pb.Tailnet{
						Id:   123,
						Name: req.Name,
					}, nil
				},
				ListMachinesFunc: func(ctx context.Context, tailnetID uint64) ([]*pb.Machine, error) {
					return []*pb.Machine{}, nil
				},
			}

			By("Reconciling the Tailnet with mock client")
			reconciler := &TailnetReconciler{
				Client:        k8sClient,
				Scheme:        k8sClient.Scheme(),
				ClientFactory: controlclient.NewMockClientFactory(mockClient),
			}

			// First reconcile adds finalizer
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: tailnetNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			if result.Requeue {
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: tailnetNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Verifying mock was called")
			Expect(mockClient.ListTailnetsCalls).To(Equal(1))
			Expect(mockClient.CreateTailnetCalls).To(HaveLen(1))

			By("Checking that Tailnet status is Ready")
			updatedTailnet := &kodiakv1alpha1.Tailnet{}
			Expect(k8sClient.Get(ctx, tailnetNamespacedName, updatedTailnet)).To(Succeed())
			Expect(updatedTailnet.Status.Ready).To(BeTrue())
			Expect(updatedTailnet.Status.Phase).To(Equal("Ready"))
			Expect(updatedTailnet.Status.TailnetID).To(Equal(uint64(123)))
		})
	})

	Context("When ionscale API fails", func() {
		const (
			tailnetName       = "test-tailnet-api-fail"
			controlServerName = "test-cs-api-fail"
			adminSecretName   = "test-admin-secret-fail"
		)

		ctx := context.Background()

		tailnetNamespacedName := types.NamespacedName{
			Name:      tailnetName,
			Namespace: "default",
		}

		controlServerNamespacedName := types.NamespacedName{
			Name:      controlServerName,
			Namespace: "default",
		}

		secretNamespacedName := types.NamespacedName{
			Name:      adminSecretName,
			Namespace: "default",
		}

		AfterEach(func() {
			tailnet := &kodiakv1alpha1.Tailnet{}
			err := k8sClient.Get(ctx, tailnetNamespacedName, tailnet)
			if err == nil {
				tailnet.Finalizers = nil
				_ = k8sClient.Update(ctx, tailnet)
				_ = k8sClient.Delete(ctx, tailnet)
			}

			cs := &kodiakv1alpha1.ControlServer{}
			err = k8sClient.Get(ctx, controlServerNamespacedName, cs)
			if err == nil {
				cs.Finalizers = nil
				_ = k8sClient.Update(ctx, cs)
				_ = k8sClient.Delete(ctx, cs)
			}

			secret := &corev1.Secret{}
			err = k8sClient.Get(ctx, secretNamespacedName, secret)
			if err == nil {
				_ = k8sClient.Delete(ctx, secret)
			}
		})

		It("should set Error status when API call fails", func() {
			By("Creating the admin secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      adminSecretName,
					Namespace: "default",
				},
				Data: map[string][]byte{
					"systemAdminKey": []byte("test-admin-key"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			By("Creating a ready ControlServer")
			controlServer := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      controlServerName,
					Namespace: "default",
					Annotations: map[string]string{
						"kodiak.mnicloud.jp/system-admin-key-secret": adminSecretName,
					},
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
			Expect(k8sClient.Create(ctx, controlServer)).To(Succeed())

			controlServer.Status.Ready = true
			controlServer.Status.Endpoint = "http://test.example.com:8080"
			controlServer.Status.Phase = "Ready"
			Expect(k8sClient.Status().Update(ctx, controlServer)).To(Succeed())

			By("Creating a Tailnet")
			tailnet := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tailnetName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "test-tailnet",
					ControlServerRef: corev1.LocalObjectReference{
						Name: controlServerName,
					},
				},
			}
			Expect(k8sClient.Create(ctx, tailnet)).To(Succeed())

			By("Setting up mock client that returns error")
			mockClient := &controlclient.MockControlServerClient{
				ListTailnetsFunc: func(ctx context.Context) ([]*pb.Tailnet, error) {
					return nil, errors.New("connection refused")
				},
			}

			By("Reconciling the Tailnet")
			reconciler := &TailnetReconciler{
				Client:        k8sClient,
				Scheme:        k8sClient.Scheme(),
				ClientFactory: controlclient.NewMockClientFactory(mockClient),
			}

			// First reconcile adds finalizer
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: tailnetNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			if result.Requeue {
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: tailnetNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Checking that Tailnet status is Error")
			updatedTailnet := &kodiakv1alpha1.Tailnet{}
			Expect(k8sClient.Get(ctx, tailnetNamespacedName, updatedTailnet)).To(Succeed())
			Expect(updatedTailnet.Status.Ready).To(BeFalse())
			Expect(updatedTailnet.Status.Phase).To(Equal("Error"))
		})
	})

	Context("When deleting a Tailnet", func() {
		const (
			tailnetName       = "test-tailnet-delete"
			controlServerName = "test-cs-delete"
			adminSecretName   = "test-admin-secret-delete"
		)

		ctx := context.Background()

		tailnetNamespacedName := types.NamespacedName{
			Name:      tailnetName,
			Namespace: "default",
		}

		controlServerNamespacedName := types.NamespacedName{
			Name:      controlServerName,
			Namespace: "default",
		}

		secretNamespacedName := types.NamespacedName{
			Name:      adminSecretName,
			Namespace: "default",
		}

		AfterEach(func() {
			cs := &kodiakv1alpha1.ControlServer{}
			err := k8sClient.Get(ctx, controlServerNamespacedName, cs)
			if err == nil {
				cs.Finalizers = nil
				_ = k8sClient.Update(ctx, cs)
				_ = k8sClient.Delete(ctx, cs)
			}

			secret := &corev1.Secret{}
			err = k8sClient.Get(ctx, secretNamespacedName, secret)
			if err == nil {
				_ = k8sClient.Delete(ctx, secret)
			}
		})

		It("should delete remote tailnet and remove finalizer", func() {
			By("Creating the admin secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      adminSecretName,
					Namespace: "default",
				},
				Data: map[string][]byte{
					"systemAdminKey": []byte("test-admin-key"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			By("Creating a ready ControlServer")
			controlServer := &kodiakv1alpha1.ControlServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      controlServerName,
					Namespace: "default",
					Annotations: map[string]string{
						"kodiak.mnicloud.jp/system-admin-key-secret": adminSecretName,
					},
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
			Expect(k8sClient.Create(ctx, controlServer)).To(Succeed())

			controlServer.Status.Ready = true
			controlServer.Status.Endpoint = "http://test.example.com:8080"
			controlServer.Status.Phase = "Ready"
			Expect(k8sClient.Status().Update(ctx, controlServer)).To(Succeed())

			By("Creating a Tailnet")
			tailnet := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tailnetName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "test-tailnet",
					ControlServerRef: corev1.LocalObjectReference{
						Name: controlServerName,
					},
				},
			}
			Expect(k8sClient.Create(ctx, tailnet)).To(Succeed())

			By("Setting up mock client")
			mockClient := &controlclient.MockControlServerClient{
				ListTailnetsFunc: func(ctx context.Context) ([]*pb.Tailnet, error) {
					return []*pb.Tailnet{}, nil
				},
				CreateTailnetFunc: func(ctx context.Context, req *pb.CreateTailnetRequest) (*pb.Tailnet, error) {
					return &pb.Tailnet{Id: 456, Name: req.Name}, nil
				},
				ListMachinesFunc: func(ctx context.Context, tailnetID uint64) ([]*pb.Machine, error) {
					return []*pb.Machine{}, nil
				},
				DeleteTailnetFunc: func(ctx context.Context, tailnetID uint64, force bool) error {
					return nil
				},
			}

			By("Reconciling the Tailnet to create it")
			reconciler := &TailnetReconciler{
				Client:        k8sClient,
				Scheme:        k8sClient.Scheme(),
				ClientFactory: controlclient.NewMockClientFactory(mockClient),
			}

			// Reconcile to create
			for i := 0; i < 2; i++ {
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: tailnetNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Verifying Tailnet was created with TailnetID")
			updatedTailnet := &kodiakv1alpha1.Tailnet{}
			Expect(k8sClient.Get(ctx, tailnetNamespacedName, updatedTailnet)).To(Succeed())
			Expect(updatedTailnet.Status.TailnetID).To(Equal(uint64(456)))

			By("Deleting the Tailnet")
			Expect(k8sClient.Delete(ctx, updatedTailnet)).To(Succeed())

			By("Reconciling to handle deletion")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: tailnetNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying DeleteTailnet was called")
			Expect(mockClient.DeleteTailnetCalls).To(HaveLen(1))
			Expect(mockClient.DeleteTailnetCalls[0].TailnetID).To(Equal(uint64(456)))
			Expect(mockClient.DeleteTailnetCalls[0].Force).To(BeTrue())

			By("Verifying the resource was deleted")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, tailnetNamespacedName, updatedTailnet)
				return apierrors.IsNotFound(err)
			}, timeout, interval).Should(BeTrue())
		})
	})
})
