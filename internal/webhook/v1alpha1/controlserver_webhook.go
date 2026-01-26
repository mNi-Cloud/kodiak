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
var controlserverlog = logf.Log.WithName("controlserver-resource")

// SetupControlServerWebhookWithManager registers the webhook for ControlServer in the manager.
func SetupControlServerWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&kodiakv1alpha1.ControlServer{}).
		WithValidator(&ControlServerCustomValidator{}).
		WithDefaulter(&ControlServerCustomDefaulter{}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-kodiak-mnicloud-jp-v1alpha1-controlserver,mutating=true,failurePolicy=fail,sideEffects=None,groups=kodiak.mnicloud.jp,resources=controlservers,verbs=create;update,versions=v1alpha1,name=mcontrolserver-v1alpha1.kb.io,admissionReviewVersions=v1

// ControlServerCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind ControlServer when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type ControlServerCustomDefaulter struct{}

var _ webhook.CustomDefaulter = &ControlServerCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind ControlServer.
func (d *ControlServerCustomDefaulter) Default(_ context.Context, obj runtime.Object) error {
	controlserver, ok := obj.(*kodiakv1alpha1.ControlServer)

	if !ok {
		return fmt.Errorf("expected an ControlServer object but got %T", obj)
	}
	controlserverlog.Info("Defaulting for ControlServer", "name", controlserver.GetName())

	// Set default listen addresses if not specified
	if controlserver.Spec.Config.ListenAddr == "" {
		controlserver.Spec.Config.ListenAddr = ":8080"
	}
	if controlserver.Spec.Config.MetricsListenAddr == "" {
		controlserver.Spec.Config.MetricsListenAddr = ":9091"
	}
	if controlserver.Spec.Config.StunListenAddr == "" {
		controlserver.Spec.Config.StunListenAddr = ":3478"
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-kodiak-mnicloud-jp-v1alpha1-controlserver,mutating=false,failurePolicy=fail,sideEffects=None,groups=kodiak.mnicloud.jp,resources=controlservers,verbs=create;update,versions=v1alpha1,name=vcontrolserver-v1alpha1.kb.io,admissionReviewVersions=v1

// ControlServerCustomValidator struct is responsible for validating the ControlServer resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type ControlServerCustomValidator struct{}

var _ webhook.CustomValidator = &ControlServerCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type ControlServer.
func (v *ControlServerCustomValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	controlserver, ok := obj.(*kodiakv1alpha1.ControlServer)
	if !ok {
		return nil, fmt.Errorf("expected a ControlServer object but got %T", obj)
	}
	controlserverlog.Info("Validation for ControlServer upon creation", "name", controlserver.GetName())

	return nil, validateControlServer(controlserver)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type ControlServer.
func (v *ControlServerCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	controlserver, ok := newObj.(*kodiakv1alpha1.ControlServer)
	if !ok {
		return nil, fmt.Errorf("expected a ControlServer object for the newObj but got %T", newObj)
	}
	controlserverlog.Info("Validation for ControlServer upon update", "name", controlserver.GetName())

	return nil, validateControlServer(controlserver)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type ControlServer.
func (v *ControlServerCustomValidator) ValidateDelete(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	controlserver, ok := obj.(*kodiakv1alpha1.ControlServer)
	if !ok {
		return nil, fmt.Errorf("expected a ControlServer object but got %T", obj)
	}
	controlserverlog.Info("Validation for ControlServer upon deletion", "name", controlserver.GetName())

	return nil, nil
}

func validateControlServer(cs *kodiakv1alpha1.ControlServer) error {
	var allErrs field.ErrorList

	// Validate image is required
	if cs.Spec.Image == "" {
		allErrs = append(allErrs, field.Required(field.NewPath("spec", "image"), "image must be specified"))
	}

	// Validate database configuration
	hasURL := cs.Spec.Config.Database.URL != ""
	hasURLSecretRef := cs.Spec.Config.Database.URLSecretRef != nil && cs.Spec.Config.Database.URLSecretRef.Name != ""

	if cs.Spec.Config.Database.Type == "postgres" && !hasURL && !hasURLSecretRef {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "config", "database"),
			"either url or urlSecretRef must be specified when type is postgres",
		))
	}

	if hasURL && hasURLSecretRef {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "config", "database"),
			cs.Spec.Config.Database,
			"cannot specify both url and urlSecretRef",
		))
	}

	// Validate TLS configuration
	if cs.Spec.Config.TLS != nil && !cs.Spec.Config.TLS.Disable {
		// If TLS is not disabled, we need either ACME, cert secret, or cert files
		hasAcme := cs.Spec.Config.TLS.AcmeEnabled
		hasCertSecret := cs.Spec.Config.TLS.CertSecretName != ""
		hasCertFiles := cs.Spec.Config.TLS.CertFile != "" && cs.Spec.Config.TLS.KeyFile != ""

		if !hasAcme && !hasCertSecret && !hasCertFiles {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "config", "tls"),
				cs.Spec.Config.TLS,
				"when TLS is enabled, must specify either acme, certSecretName, or both certFile and keyFile",
			))
		}
	}

	if len(allErrs) == 0 {
		return nil
	}
	return allErrs.ToAggregate()
}
