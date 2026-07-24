/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"

	pb "github.com/jsiebens/ionscale/pkg/gen/ionscale/v1"
	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
	controlclient "github.com/mNi-Cloud/kodiak/internal/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("Tailnet controller", func() {
	ctx := context.Background()

	It("creates a managed Tailnet and publishes the protocol login URL", func() {
		key := types.NamespacedName{Namespace: "default", Name: "managed-tailnet-test"}
		resource := &kodiakv1alpha1.Tailnet{
			ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
			Spec:       kodiakv1alpha1.TailnetSpec{Name: "managed-tailnet-test"},
		}
		Expect(k8sClient.Create(ctx, resource)).To(Succeed())
		defer func() {
			var current kodiakv1alpha1.Tailnet
			if k8sClient.Get(ctx, key, &current) == nil {
				current.Finalizers = nil
				_ = k8sClient.Update(ctx, &current)
				_ = k8sClient.Delete(ctx, &current)
			}
		}()

		mock := &controlclient.MockControlServerClient{
			ListTailnetsFunc: func(context.Context) ([]*pb.Tailnet, error) {
				return nil, nil
			},
			CreateTailnetFunc: func(_ context.Context, request *pb.CreateTailnetRequest) (*pb.Tailnet, error) {
				return &pb.Tailnet{Id: 123, Name: request.Name}, nil
			},
			ListMachinesFunc: func(context.Context, uint64) ([]*pb.Machine, error) {
				return []*pb.Machine{{Id: 1}}, nil
			},
		}
		reconciler := &TailnetReconciler{
			Client:           k8sClient,
			Scheme:           k8sClient.Scheme(),
			ClientFactory:    controlclient.NewMockClientFactory(mock),
			IonscaleEndpoint: "https://api.example.test",
			IonscaleLoginURL: "https://vpn.example.test",
			IonscaleAdminKey: "admin",
		}
		for range 2 {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
		}

		var updated kodiakv1alpha1.Tailnet
		Expect(k8sClient.Get(ctx, key, &updated)).To(Succeed())
		Expect(updated.Status.TailnetID).To(Equal("123"))
		Expect(updated.Status.LoginURL).To(Equal("https://vpn.example.test"))
		Expect(updated.Status.MachineCount).To(Equal(int32(1)))
		Expect(meta.IsStatusConditionTrue(updated.Status.Conditions, kodiakv1alpha1.TailnetConditionReady)).To(BeTrue())
	})

	It("reports a missing managed-control-plane configuration", func() {
		key := types.NamespacedName{Namespace: "default", Name: "unconfigured-tailnet-test"}
		resource := &kodiakv1alpha1.Tailnet{
			ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
			Spec:       kodiakv1alpha1.TailnetSpec{Name: "unconfigured-tailnet-test"},
		}
		Expect(k8sClient.Create(ctx, resource)).To(Succeed())
		defer func() {
			var current kodiakv1alpha1.Tailnet
			if k8sClient.Get(ctx, key, &current) == nil {
				current.Finalizers = nil
				_ = k8sClient.Update(ctx, &current)
				_ = k8sClient.Delete(ctx, &current)
			}
		}()

		reconciler := &TailnetReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		for range 2 {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
		}

		var updated kodiakv1alpha1.Tailnet
		Expect(k8sClient.Get(ctx, key, &updated)).To(Succeed())
		condition := meta.FindStatusCondition(updated.Status.Conditions, kodiakv1alpha1.TailnetConditionReady)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal(reasonConfigurationMissing))
	})
})
