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

	Context("When ionscale configuration is missing", func() {
		const tailnetName = "test-tailnet-no-config"

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

		It("should set Pending status when ionscale config is missing", func() {
			By("Creating a Tailnet")
			resource := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tailnetName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "test-tailnet",
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			By("Reconciling the resource without ionscale configuration")
			reconciler := &TailnetReconciler{
				Client:           k8sClient,
				Scheme:           k8sClient.Scheme(),
				IonscaleEndpoint: "", // Missing configuration
				IonscaleAdminKey: "", // Missing configuration
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

	Context("When reconciling a Tailnet resource", func() {
		const tailnetName = "test-tailnet-basic"

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

		It("should add finalizer to the resource", func() {
			By("Creating a Tailnet resource")
			resource := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tailnetName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "test-tailnet",
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &TailnetReconciler{
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
			updated := &kodiakv1alpha1.Tailnet{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Finalizers).To(ContainElement(kodiakFinalizer))
		})
	})

	Context("When successfully creating a Tailnet with mock client", func() {
		const tailnetName = "test-tailnet-success"

		ctx := context.Background()

		tailnetNamespacedName := types.NamespacedName{
			Name:      tailnetName,
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
		})

		It("should create Tailnet and set Ready status", func() {
			By("Creating a Tailnet")
			tailnet := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tailnetName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "test-tailnet",
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
				Client:           k8sClient,
				Scheme:           k8sClient.Scheme(),
				ClientFactory:    controlclient.NewMockClientFactory(mockClient),
				IonscaleEndpoint: "http://ionscale.test.svc:8080",
				IonscaleAdminKey: "test-admin-key",
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
		const tailnetName = "test-tailnet-api-fail"

		ctx := context.Background()

		tailnetNamespacedName := types.NamespacedName{
			Name:      tailnetName,
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
		})

		It("should set Error status when API call fails", func() {
			By("Creating a Tailnet")
			tailnet := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tailnetName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "test-tailnet",
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
				Client:           k8sClient,
				Scheme:           k8sClient.Scheme(),
				ClientFactory:    controlclient.NewMockClientFactory(mockClient),
				IonscaleEndpoint: "http://ionscale.test.svc:8080",
				IonscaleAdminKey: "test-admin-key",
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
		const tailnetName = "test-tailnet-delete"

		ctx := context.Background()

		tailnetNamespacedName := types.NamespacedName{
			Name:      tailnetName,
			Namespace: "default",
		}

		AfterEach(func() {
			// Nothing to clean up - the test deletes the Tailnet
		})

		It("should delete remote tailnet and remove finalizer", func() {
			By("Creating a Tailnet")
			tailnet := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tailnetName,
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "test-tailnet",
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
				Client:           k8sClient,
				Scheme:           k8sClient.Scheme(),
				ClientFactory:    controlclient.NewMockClientFactory(mockClient),
				IonscaleEndpoint: "http://ionscale.test.svc:8080",
				IonscaleAdminKey: "test-admin-key",
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
