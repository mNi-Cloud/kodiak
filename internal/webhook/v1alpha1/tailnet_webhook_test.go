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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
)

var _ = Describe("Tailnet Webhook", func() {
	var (
		ctx       context.Context
		validator TailnetCustomValidator
		defaulter TailnetCustomDefaulter
	)

	BeforeEach(func() {
		ctx = context.Background()
		validator = TailnetCustomValidator{}
		defaulter = TailnetCustomDefaulter{}
	})

	Context("When creating Tailnet under Defaulting Webhook", func() {
		It("Should not modify the object as no defaults are needed", func() {
			obj := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-tailnet",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "my-tailnet",
				},
			}

			err := defaulter.Default(ctx, obj)
			Expect(err).NotTo(HaveOccurred())

			// Verify no modifications were made
			Expect(obj.Spec.Name).To(Equal("my-tailnet"))
		})
	})

	Context("When validating Tailnet", func() {
		It("Should deny creation if name is missing", func() {
			obj := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-tailnet",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					// Name is missing
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("name"))
		})

		It("Should admit creation with valid configuration", func() {
			obj := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-tailnet",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "my-tailnet",
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should admit creation with IAMPolicy and ACLPolicy", func() {
			obj := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-tailnet",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name:      "my-tailnet",
					IAMPolicy: `{"groups": {}}`,
					ACLPolicy: `{"acls": []}`,
				},
			}

			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should validate updates correctly", func() {
			oldObj := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-tailnet",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "my-tailnet",
				},
			}

			newObj := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-tailnet",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "my-tailnet-updated",
				},
			}

			_, err := validator.ValidateUpdate(ctx, oldObj, newObj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should deny update if name becomes empty", func() {
			oldObj := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-tailnet",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "my-tailnet",
				},
			}

			newObj := &kodiakv1alpha1.Tailnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-tailnet",
					Namespace: "default",
				},
				Spec: kodiakv1alpha1.TailnetSpec{
					Name: "", // Empty name
				},
			}

			_, err := validator.ValidateUpdate(ctx, oldObj, newObj)
			Expect(err).To(HaveOccurred())
		})
	})
})
