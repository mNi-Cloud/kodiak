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
	tsworkload "github.com/mNi-Cloud/kodiak/internal/tailscale/workload"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("Connector controllers", func() {
	ctx := context.Background()

	cleanup := func(key types.NamespacedName) {
		var instances kodiakv1alpha1.ConnectorInstanceList
		_ = k8sClient.List(ctx, &instances, client.InNamespace(key.Namespace),
			client.MatchingLabels{tsworkload.LabelConnector: tsworkload.ConnectorLabelValue(key.Name)})
		for i := range instances.Items {
			instances.Items[i].Finalizers = nil
			_ = k8sClient.Update(ctx, &instances.Items[i])
			_ = k8sClient.Delete(ctx, &instances.Items[i])
		}
		resource := &kodiakv1alpha1.Connector{}
		if k8sClient.Get(ctx, key, resource) == nil {
			resource.Finalizers = nil
			_ = k8sClient.Update(ctx, resource)
			_ = k8sClient.Delete(ctx, resource)
		}
	}

	It("creates replaceable instances without copying an external auth key", func() {
		key := types.NamespacedName{Namespace: "default", Name: "external-connector"}
		defer cleanup(key)
		externalKey := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "external-auth", Namespace: key.Namespace},
			Data:       map[string][]byte{"TS_AUTH_KEY": []byte("tskey-auth-external")},
		}
		Expect(k8sClient.Create(ctx, externalKey)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, externalKey) }()
		connector := &kodiakv1alpha1.Connector{
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
		Expect(k8sClient.Create(ctx, connector)).To(Succeed())
		reconciler := &ConnectorReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(),
			TailscaleImage: "tailscale/tailscale:v1.98.9",
		}
		for range 4 {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
		}

		var instances kodiakv1alpha1.ConnectorInstanceList
		Expect(k8sClient.List(ctx, &instances, client.InNamespace(key.Namespace),
			client.MatchingLabels{tsworkload.LabelConnector: tsworkload.ConnectorLabelValue(key.Name)})).To(Succeed())
		Expect(instances.Items).To(HaveLen(2))
		for _, instance := range instances.Items {
			Expect(instance.Spec.AuthKeySecretRef).NotTo(BeNil())
			Expect(instance.Spec.AuthKeySecretRef.Name).To(Equal(externalKey.Name))
		}
		var secrets corev1.SecretList
		Expect(k8sClient.List(ctx, &secrets, client.InNamespace(key.Namespace),
			client.MatchingLabels{tsworkload.LabelConnector: tsworkload.ConnectorLabelValue(key.Name)})).To(Succeed())
		Expect(secrets.Items).To(BeEmpty())
	})

	It("uses an ephemeral managed bootstrap key and removes it after registration", func() {
		key := types.NamespacedName{Namespace: "default", Name: "managed-connector"}
		defer cleanup(key)
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
			Type: kodiakv1alpha1.TailnetConditionReady, Status: metav1.ConditionTrue,
			Reason: "Ready", ObservedGeneration: tailnet.Generation, LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, tailnet)).To(Succeed())

		mock := &controlclient.MockControlServerClient{}
		mock.CreateAuthKeyFunc = func(context.Context, uint64, bool, time.Duration, []string, bool) (*pb.AuthKey, string, error) {
			return &pb.AuthKey{Id: 71}, "tskey-auth-bootstrap", nil
		}
		connector := &kodiakv1alpha1.Connector{
			ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
			Spec: kodiakv1alpha1.ConnectorSpec{
				TailnetRef: &corev1.LocalObjectReference{Name: tailnet.Name},
				Tags:       []string{"tag:mni-vpn"},
				SubnetRouter: kodiakv1alpha1.SubnetRouterSpec{
					AdvertiseRoutes: []string{"10.1.0.0/24"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, connector)).To(Succeed())
		parent := &ConnectorReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(),
			IonscaleEndpoint: "https://api.example.test", IonscaleAdminKey: "admin",
		}
		for range 3 {
			_, err := parent.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
		}
		var instances kodiakv1alpha1.ConnectorInstanceList
		Expect(k8sClient.List(ctx, &instances, client.InNamespace(key.Namespace),
			client.MatchingLabels{tsworkload.LabelConnector: tsworkload.ConnectorLabelValue(key.Name)})).To(Succeed())
		Expect(instances.Items).To(HaveLen(1))
		instanceKey := client.ObjectKeyFromObject(&instances.Items[0])
		instanceController := &ConnectorInstanceReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(),
			ClientFactory:    controlclient.NewMockClientFactory(mock),
			IonscaleEndpoint: "https://api.example.test", IonscaleAdminKey: "admin",
		}
		for range 4 {
			_, err := instanceController.Reconcile(ctx, reconcile.Request{NamespacedName: instanceKey})
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(mock.CreateAuthKeyCalls).To(HaveLen(1))
		Expect(mock.CreateAuthKeyCalls[0].Ephemeral).To(BeTrue())
		Expect(mock.CreateAuthKeyCalls[0].Expiry).To(Equal(bootstrapKeyExpiry))

		var pod corev1.Pod
		Expect(k8sClient.Get(ctx, instanceKey, &pod)).To(Succeed())
		Expect(pod.Spec.AutomountServiceAccountToken).To(HaveValue(BeFalse()))
		pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
		Expect(k8sClient.Status().Update(ctx, &pod)).To(Succeed())
		var instance kodiakv1alpha1.ConnectorInstance
		Expect(k8sClient.Get(ctx, instanceKey, &instance)).To(Succeed())
		mock.ListMachinesFunc = func(context.Context, uint64) ([]*pb.Machine, error) {
			return []*pb.Machine{{
				Id: 91, Name: instance.Status.RequestedHostname, Ipv4: "100.64.0.10",
				Authorized: true, Connected: true,
				AdvertisedRoutes: []string{"10.1.0.0/24"}, EnabledRoutes: []string{"10.1.0.0/24"},
			}}, nil
		}
		for range 3 {
			_, err := instanceController.Reconcile(ctx, reconcile.Request{NamespacedName: instanceKey})
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(mock.DeleteAuthKeyCalls).To(ContainElement(uint64(71)))
		Expect(k8sClient.Get(ctx, instanceKey, &instance)).To(Succeed())
		Expect(meta.IsStatusConditionTrue(instance.Status.Conditions, kodiakv1alpha1.ConnectorInstanceConditionReady)).To(BeTrue())
		Expect(instance.Status.Device.MachineID).To(Equal("91"))
	})
})
