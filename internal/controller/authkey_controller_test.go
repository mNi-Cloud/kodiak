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
	"time"

	pb "github.com/jsiebens/ionscale/pkg/gen/ionscale/v1"
	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
	controlclient "github.com/mNi-Cloud/kodiak/internal/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("AuthKey controller", func() {
	ctx := context.Background()

	It("issues and transactionally rotates a user enrollment key", func() {
		tailnetKey := types.NamespacedName{Namespace: "default", Name: "authkey-tailnet"}
		tailnet := &kodiakv1alpha1.Tailnet{
			ObjectMeta: metav1.ObjectMeta{Name: tailnetKey.Name, Namespace: tailnetKey.Namespace},
			Spec:       kodiakv1alpha1.TailnetSpec{Name: tailnetKey.Name},
		}
		Expect(k8sClient.Create(ctx, tailnet)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, tailnet) }()
		tailnet.Status.TailnetID = "22"
		tailnet.Status.Conditions = []metav1.Condition{{
			Type:               kodiakv1alpha1.TailnetConditionReady,
			Status:             metav1.ConditionTrue,
			Reason:             "Ready",
			ObservedGeneration: tailnet.Generation,
			LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, tailnet)).To(Succeed())

		nextID := uint64(40)
		mock := &controlclient.MockControlServerClient{}
		mock.CreateAuthKeyFunc = func(context.Context, uint64, bool, time.Duration, []string, bool) (*pb.AuthKey, string, error) {
			nextID++
			return &pb.AuthKey{Id: nextID}, "tskey-auth-value", nil
		}
		mock.ListAuthKeysFunc = func(context.Context, uint64) ([]*pb.AuthKey, error) {
			return []*pb.AuthKey{{Id: nextID}}, nil
		}

		key := types.NamespacedName{Namespace: "default", Name: "user-enrollment"}
		resource := &kodiakv1alpha1.AuthKey{
			ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
			Spec: kodiakv1alpha1.AuthKeySpec{
				TailnetRef:    corev1.LocalObjectReference{Name: tailnet.Name},
				Tags:          []string{"tag:user"},
				PreAuthorized: true,
				Expiry:        &metav1.Duration{Duration: time.Hour},
			},
		}
		Expect(k8sClient.Create(ctx, resource)).To(Succeed())
		defer func() {
			var current kodiakv1alpha1.AuthKey
			if k8sClient.Get(ctx, key, &current) == nil {
				current.Finalizers = nil
				_ = k8sClient.Update(ctx, &current)
				_ = k8sClient.Delete(ctx, &current)
			}
		}()

		reconciler := &AuthKeyReconciler{
			Client:           k8sClient,
			Scheme:           k8sClient.Scheme(),
			ClientFactory:    controlclient.NewMockClientFactory(mock),
			IonscaleEndpoint: "https://api.example.test",
			IonscaleAdminKey: "admin",
		}
		for range 2 {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
		}

		var issued kodiakv1alpha1.AuthKey
		Expect(k8sClient.Get(ctx, key, &issued)).To(Succeed())
		Expect(issued.Status.KeyID).To(Equal("41"))
		Expect(meta.IsStatusConditionTrue(issued.Status.Conditions, kodiakv1alpha1.AuthKeyConditionReady)).To(BeTrue())
		Expect(mock.CreateAuthKeyCalls).To(HaveLen(1))
		Expect(mock.CreateAuthKeyCalls[0].TailnetID).To(Equal(uint64(22)))
		Expect(mock.CreateAuthKeyCalls[0].Expiry).To(Equal(time.Hour))

		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: key.Namespace, Name: "authkey-" + key.Name}, &secret)).To(Succeed())
		Expect(string(secret.Data[authKeySecretDataKey])).To(Equal("tskey-auth-value"))

		issued.Spec.RotationNonce = "rotate-1"
		Expect(k8sClient.Update(ctx, &issued)).To(Succeed())
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		var rotating kodiakv1alpha1.AuthKey
		Expect(k8sClient.Get(ctx, key, &rotating)).To(Succeed())
		Expect(rotating.Status.KeyID).To(Equal("42"))
		Expect(rotating.Status.RetiringKeyID).To(Equal("41"))

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(mock.DeleteAuthKeyCalls).To(ContainElement(uint64(41)))

		var rotated kodiakv1alpha1.AuthKey
		Expect(k8sClient.Get(ctx, key, &rotated)).To(Succeed())
		Expect(rotated.Status.RetiringKeyID).To(BeEmpty())
		Expect(rotated.Status.IssuedRotationNonce).To(Equal("rotate-1"))
	})
})
