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
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
)

// nolint:unused
// log is for logging in this package.
var tailnetlog = logf.Log.WithName("tailnet-resource")

// SetupTailnetWebhookWithManager registers the webhook for Tailnet in the manager.
func SetupTailnetWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&kodiakv1alpha1.Tailnet{}).
		WithValidator(&TailnetCustomValidator{}).
		WithDefaulter(&TailnetCustomDefaulter{}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-kodiak-mnicloud-jp-v1alpha1-tailnet,mutating=true,failurePolicy=fail,sideEffects=None,groups=kodiak.mnicloud.jp,resources=tailnets,verbs=create;update,versions=v1alpha1,name=mtailnet-v1alpha1.kb.io,admissionReviewVersions=v1

// TailnetCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind Tailnet when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type TailnetCustomDefaulter struct{}

var _ webhook.CustomDefaulter = &TailnetCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind Tailnet.
func (d *TailnetCustomDefaulter) Default(_ context.Context, obj runtime.Object) error {
	tailnet, ok := obj.(*kodiakv1alpha1.Tailnet)

	if !ok {
		return fmt.Errorf("expected an Tailnet object but got %T", obj)
	}
	tailnetlog.Info("Defaulting for Tailnet", "name", tailnet.GetName())

	// No defaults needed currently

	return nil
}

// +kubebuilder:webhook:path=/validate-kodiak-mnicloud-jp-v1alpha1-tailnet,mutating=false,failurePolicy=fail,sideEffects=None,groups=kodiak.mnicloud.jp,resources=tailnets,verbs=create;update,versions=v1alpha1,name=vtailnet-v1alpha1.kb.io,admissionReviewVersions=v1

// TailnetCustomValidator struct is responsible for validating the Tailnet resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type TailnetCustomValidator struct{}

var _ webhook.CustomValidator = &TailnetCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type Tailnet.
func (v *TailnetCustomValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	tailnet, ok := obj.(*kodiakv1alpha1.Tailnet)
	if !ok {
		return nil, fmt.Errorf("expected a Tailnet object but got %T", obj)
	}
	tailnetlog.Info("Validation for Tailnet upon creation", "name", tailnet.GetName())

	return nil, validateTailnet(tailnet)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Tailnet.
func (v *TailnetCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	tailnet, ok := newObj.(*kodiakv1alpha1.Tailnet)
	if !ok {
		return nil, fmt.Errorf("expected a Tailnet object for the newObj but got %T", newObj)
	}
	tailnetlog.Info("Validation for Tailnet upon update", "name", tailnet.GetName())

	return nil, validateTailnet(tailnet)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type Tailnet.
func (v *TailnetCustomValidator) ValidateDelete(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	tailnet, ok := obj.(*kodiakv1alpha1.Tailnet)
	if !ok {
		return nil, fmt.Errorf("expected a Tailnet object but got %T", obj)
	}
	tailnetlog.Info("Validation for Tailnet upon deletion", "name", tailnet.GetName())

	return nil, nil
}

func validateTailnet(t *kodiakv1alpha1.Tailnet) error {
	var allErrs field.ErrorList

	// Validate name is required
	if t.Spec.Name == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "name"),
			"tailnet name must be specified",
		))
	}

	if len(allErrs) == 0 {
		return nil
	}
	return allErrs.ToAggregate()
}
