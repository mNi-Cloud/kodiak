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
	"time"

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
var authkeylog = logf.Log.WithName("authkey-resource")

// SetupAuthKeyWebhookWithManager registers the webhook for AuthKey in the manager.
func SetupAuthKeyWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&kodiakv1alpha1.AuthKey{}).
		WithValidator(&AuthKeyCustomValidator{}).
		WithDefaulter(&AuthKeyCustomDefaulter{}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-kodiak-mnicloud-jp-v1alpha1-authkey,mutating=true,failurePolicy=fail,sideEffects=None,groups=kodiak.mnicloud.jp,resources=authkeys,verbs=create;update,versions=v1alpha1,name=mauthkey-v1alpha1.kb.io,admissionReviewVersions=v1

// AuthKeyCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind AuthKey when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type AuthKeyCustomDefaulter struct{}

var _ webhook.CustomDefaulter = &AuthKeyCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind AuthKey.
func (d *AuthKeyCustomDefaulter) Default(_ context.Context, obj runtime.Object) error {
	authkey, ok := obj.(*kodiakv1alpha1.AuthKey)

	if !ok {
		return fmt.Errorf("expected an AuthKey object but got %T", obj)
	}
	authkeylog.Info("Defaulting for AuthKey", "name", authkey.GetName())

	// Set default expiry if not specified
	if authkey.Spec.Expiry == "" {
		authkey.Spec.Expiry = "24h"
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-kodiak-mnicloud-jp-v1alpha1-authkey,mutating=false,failurePolicy=fail,sideEffects=None,groups=kodiak.mnicloud.jp,resources=authkeys,verbs=create;update,versions=v1alpha1,name=vauthkey-v1alpha1.kb.io,admissionReviewVersions=v1

// AuthKeyCustomValidator struct is responsible for validating the AuthKey resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type AuthKeyCustomValidator struct{}

var _ webhook.CustomValidator = &AuthKeyCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type AuthKey.
func (v *AuthKeyCustomValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	authkey, ok := obj.(*kodiakv1alpha1.AuthKey)
	if !ok {
		return nil, fmt.Errorf("expected a AuthKey object but got %T", obj)
	}
	authkeylog.Info("Validation for AuthKey upon creation", "name", authkey.GetName())

	return nil, validateAuthKey(authkey)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type AuthKey.
func (v *AuthKeyCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	authkey, ok := newObj.(*kodiakv1alpha1.AuthKey)
	if !ok {
		return nil, fmt.Errorf("expected a AuthKey object for the newObj but got %T", newObj)
	}
	authkeylog.Info("Validation for AuthKey upon update", "name", authkey.GetName())

	return nil, validateAuthKey(authkey)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type AuthKey.
func (v *AuthKeyCustomValidator) ValidateDelete(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	authkey, ok := obj.(*kodiakv1alpha1.AuthKey)
	if !ok {
		return nil, fmt.Errorf("expected a AuthKey object but got %T", obj)
	}
	authkeylog.Info("Validation for AuthKey upon deletion", "name", authkey.GetName())

	return nil, nil
}

func validateAuthKey(a *kodiakv1alpha1.AuthKey) error {
	var allErrs field.ErrorList

	// Validate tailnetRef.name is required
	if a.Spec.TailnetRef.Name == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "tailnetRef", "name"),
			"tailnet reference name must be specified",
		))
	}

	// Validate expiry is a valid duration
	if a.Spec.Expiry != "" {
		duration, err := time.ParseDuration(a.Spec.Expiry)
		if err != nil {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "expiry"),
				a.Spec.Expiry,
				fmt.Sprintf("invalid duration format: %v", err),
			))
		} else if duration <= 0 {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "expiry"),
				a.Spec.Expiry,
				"expiry must be a positive duration",
			))
		}
	}

	if len(allErrs) == 0 {
		return nil
	}
	return allErrs.ToAggregate()
}
