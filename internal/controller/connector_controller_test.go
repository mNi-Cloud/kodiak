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
	"encoding/json"
	"time"

	pb "github.com/jsiebens/ionscale/pkg/gen/ionscale/v1"
	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
	controlclient "github.com/mNi-Cloud/kodiak/internal/client"
	tsworkload "github.com/mNi-Cloud/kodiak/internal/tailscale/workload"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"tailscale.com/kube/kubetypes"
)

var _ = Describe("Connector controller", func() {
	ctx := context.Background()

	reconcileTwice := func(reconciler *ConnectorReconciler, key types.NamespacedName) {
		for range 2 {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
		}
	}

	cleanupConnector := func(key types.NamespacedName) {
		resource := &kodiakv1alpha1.Connector{}
		if k8sClient.Get(ctx, key, resource) == nil {
			resource.Finalizers = nil
			Expect(k8sClient.Update(ctx, resource)).To(Succeed())
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		}
	}

	It("creates a kernel-networking StatefulSet with per-replica state", func() {
		key := types.NamespacedName{Namespace: "default", Name: "external-connector"}
		defer cleanupConnector(key)

		externalKey := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "external-auth", Namespace: key.Namespace},
			Data:       map[string][]byte{"TS_AUTH_KEY": []byte("tskey-auth-external")},
		}
		Expect(k8sClient.Create(ctx, externalKey)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, externalKey) }()

		resource := &kodiakv1alpha1.Connector{
			ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
			Spec: kodiakv1alpha1.ConnectorSpec{
				AuthKeySecretRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: externalKey.Name},
					Key:                  "TS_AUTH_KEY",
				},
				LoginURL: "https://control.example.test",
				Replicas: ptr.To[int32](2),
				SubnetRouter: kodiakv1alpha1.SubnetRouterSpec{
					AdvertiseRoutes: []string{"10.0.0.0/24"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, resource)).To(Succeed())

		reconciler := &ConnectorReconciler{
			Client:         k8sClient,
			Scheme:         k8sClient.Scheme(),
			TailscaleImage: "tailscale/tailscale:v1.98.9",
		}
		reconcileTwice(reconciler, key)

		var statefulSet appsv1.StatefulSet
		names := tsworkload.ChildNames(resource.Name)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: key.Namespace, Name: names.StatefulSet}, &statefulSet)).To(Succeed())
		Expect(statefulSet.Spec.Replicas).To(HaveValue(Equal(int32(2))))
		Expect(statefulSet.Spec.Template.Spec.Containers).To(HaveLen(1))
		Expect(statefulSet.Spec.Template.Spec.Containers[0].ImagePullPolicy).To(Equal(corev1.PullIfNotPresent))
		Expect(statefulSet.Spec.Template.Spec.Containers[0].SecurityContext.Privileged).To(HaveValue(BeTrue()))
		Expect(envValue(statefulSet.Spec.Template.Spec.Containers[0].Env, "TS_USERSPACE")).To(Equal("false"))
		Expect(envValue(statefulSet.Spec.Template.Spec.Containers[0].Env, "TS_ROUTES")).To(Equal("10.0.0.0/24"))

		for ordinal := int32(0); ordinal < 2; ordinal++ {
			var state corev1.Secret
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: key.Namespace,
				Name:      tsworkload.StateSecretName(resource.Name, ordinal),
			}, &state)).To(Succeed())
			Expect(string(state.Data[stateSecretAuthKey])).To(Equal("tskey-auth-external"))
		}

		var current kodiakv1alpha1.Connector
		Expect(k8sClient.Get(ctx, key, &current)).To(Succeed())
		current.Spec.Replicas = ptr.To[int32](1)
		Expect(k8sClient.Update(ctx, &current)).To(Succeed())
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		var removed corev1.Secret
		err = k8sClient.Get(ctx, types.NamespacedName{
			Namespace: key.Namespace,
			Name:      tsworkload.StateSecretName(resource.Name, 1),
		}, &removed)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("uses internal bootstrap credentials and only observes managed route approval", func() {
		key := types.NamespacedName{Namespace: "default", Name: "managed-connector"}
		defer cleanupConnector(key)

		tailnet := &kodiakv1alpha1.Tailnet{
			ObjectMeta: metav1.ObjectMeta{Name: "managed-tailnet", Namespace: key.Namespace},
			Spec:       kodiakv1alpha1.TailnetSpec{Name: "managed-tailnet"},
		}
		Expect(k8sClient.Create(ctx, tailnet)).To(Succeed())
		defer func() {
			tailnet.Finalizers = nil
			_ = k8sClient.Update(ctx, tailnet)
			_ = k8sClient.Delete(ctx, tailnet)
		}()
		tailnet.Status.TailnetID = "17"
		tailnet.Status.LoginURL = "https://vpn.example.test"
		tailnet.Status.Conditions = []metav1.Condition{{
			Type:               kodiakv1alpha1.TailnetConditionReady,
			Status:             metav1.ConditionTrue,
			Reason:             "Ready",
			ObservedGeneration: tailnet.Generation,
			LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, tailnet)).To(Succeed())

		mock := &controlclient.MockControlServerClient{}
		mock.CreateAuthKeyFunc = func(context.Context, uint64, bool, time.Duration, []string, bool) (*pb.AuthKey, string, error) {
			return &pb.AuthKey{Id: 71}, "tskey-auth-bootstrap", nil
		}
		mock.ListMachinesFunc = func(context.Context, uint64) ([]*pb.Machine, error) {
			return []*pb.Machine{{
				Id:               91,
				Name:             tsworkload.StateSecretName(key.Name, 0),
				Ipv4:             "100.64.0.10",
				Authorized:       true,
				Connected:        true,
				AdvertisedRoutes: []string{"10.1.0.0/24"},
				EnabledRoutes:    []string{"10.1.0.0/24"},
			}}, nil
		}

		resource := &kodiakv1alpha1.Connector{
			ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
			Spec: kodiakv1alpha1.ConnectorSpec{
				TailnetRef: &corev1.LocalObjectReference{Name: tailnet.Name},
				Tags:       []string{"tag:mni-vpn"},
				SubnetRouter: kodiakv1alpha1.SubnetRouterSpec{
					AdvertiseRoutes: []string{"10.1.0.0/24"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, resource)).To(Succeed())
		reconciler := &ConnectorReconciler{
			Client:           k8sClient,
			Scheme:           k8sClient.Scheme(),
			ClientFactory:    controlclient.NewMockClientFactory(mock),
			IonscaleEndpoint: "https://api.example.test",
			IonscaleAdminKey: "admin",
		}
		reconcileTwice(reconciler, key)

		Expect(mock.CreateAuthKeyCalls).To(HaveLen(1))
		Expect(mock.CreateAuthKeyCalls[0].TailnetID).To(Equal(uint64(17)))
		Expect(mock.CreateAuthKeyCalls[0].Expiry).To(Equal(bootstrapKeyExpiry))
		Expect(mock.CreateAuthKeyCalls[0].PreAuthorized).To(BeTrue())

		stateName := tsworkload.StateSecretName(key.Name, 0)
		var state corev1.Secret
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: key.Namespace, Name: stateName}, &state)).To(Succeed())
		ips, err := json.Marshal([]string{"100.64.0.10"})
		Expect(err).NotTo(HaveOccurred())
		delete(state.Data, stateSecretAuthKey)
		state.Data[kubetypes.KeyDeviceID] = []byte("node-key")
		state.Data[kubetypes.KeyDeviceIPs] = ips
		Expect(k8sClient.Update(ctx, &state)).To(Succeed())

		var statefulSet appsv1.StatefulSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: key.Namespace, Name: tsworkload.ChildNames(key.Name).StatefulSet}, &statefulSet)).To(Succeed())
		statefulSet.Status.Replicas = 1
		statefulSet.Status.CurrentReplicas = 1
		statefulSet.Status.ReadyReplicas = 1
		Expect(k8sClient.Status().Update(ctx, &statefulSet)).To(Succeed())

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(mock.DeleteAuthKeyCalls).To(ContainElement(uint64(71)))

		var updated kodiakv1alpha1.Connector
		Expect(k8sClient.Get(ctx, key, &updated)).To(Succeed())
		Expect(meta.IsStatusConditionTrue(updated.Status.Conditions, kodiakv1alpha1.ConnectorConditionReady)).To(BeTrue())
		Expect(updated.Status.Devices).To(HaveLen(1))
		Expect(updated.Status.Devices[0].MachineID).To(Equal("91"))
		Expect(updated.Status.Devices[0].EnabledRoutes).To(Equal([]string{"10.1.0.0/24"}))
	})
})

func envValue(values []corev1.EnvVar, name string) string {
	for _, value := range values {
		if value.Name == name {
			return value.Value
		}
	}
	return ""
}
