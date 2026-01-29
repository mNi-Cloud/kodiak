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
	"net"

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
var connectorlog = logf.Log.WithName("connector-resource")

// SetupConnectorWebhookWithManager registers the webhook for Connector in the manager.
func SetupConnectorWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&kodiakv1alpha1.Connector{}).
		WithValidator(&ConnectorCustomValidator{}).
		WithDefaulter(&ConnectorCustomDefaulter{}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-kodiak-mnicloud-jp-v1alpha1-connector,mutating=true,failurePolicy=fail,sideEffects=None,groups=kodiak.mnicloud.jp,resources=connectors,verbs=create;update,versions=v1alpha1,name=mconnector-v1alpha1.kb.io,admissionReviewVersions=v1

// ConnectorCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind Connector when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type ConnectorCustomDefaulter struct{}

var _ webhook.CustomDefaulter = &ConnectorCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind Connector.
func (d *ConnectorCustomDefaulter) Default(_ context.Context, obj runtime.Object) error {
	connector, ok := obj.(*kodiakv1alpha1.Connector)

	if !ok {
		return fmt.Errorf("expected an Connector object but got %T", obj)
	}
	connectorlog.Info("Defaulting for Connector", "name", connector.GetName())

	// No defaults needed currently

	return nil
}

// +kubebuilder:webhook:path=/validate-kodiak-mnicloud-jp-v1alpha1-connector,mutating=false,failurePolicy=fail,sideEffects=None,groups=kodiak.mnicloud.jp,resources=connectors,verbs=create;update,versions=v1alpha1,name=vconnector-v1alpha1.kb.io,admissionReviewVersions=v1

// ConnectorCustomValidator struct is responsible for validating the Connector resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type ConnectorCustomValidator struct{}

var _ webhook.CustomValidator = &ConnectorCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type Connector.
func (v *ConnectorCustomValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	connector, ok := obj.(*kodiakv1alpha1.Connector)
	if !ok {
		return nil, fmt.Errorf("expected a Connector object but got %T", obj)
	}
	connectorlog.Info("Validation for Connector upon creation", "name", connector.GetName())

	warnings := collectConnectorWarnings(connector)
	return warnings, validateConnector(connector)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Connector.
func (v *ConnectorCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	connector, ok := newObj.(*kodiakv1alpha1.Connector)
	if !ok {
		return nil, fmt.Errorf("expected a Connector object for the newObj but got %T", newObj)
	}
	connectorlog.Info("Validation for Connector upon update", "name", connector.GetName())

	warnings := collectConnectorWarnings(connector)
	return warnings, validateConnector(connector)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type Connector.
func (v *ConnectorCustomValidator) ValidateDelete(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	connector, ok := obj.(*kodiakv1alpha1.Connector)
	if !ok {
		return nil, fmt.Errorf("expected a Connector object but got %T", obj)
	}
	connectorlog.Info("Validation for Connector upon deletion", "name", connector.GetName())

	return nil, nil
}

func validateConnector(c *kodiakv1alpha1.Connector) error {
	var allErrs field.ErrorList

	ts := c.Spec.Spec.Tailscale

	// Validate auth key configuration
	hasInlineKey := ts.AuthKey != ""
	hasSecretRef := ts.AuthKeySecretRef != nil && ts.AuthKeySecretRef.Name != ""
	hasAuthKeyRef := ts.AuthKeyRef != nil && ts.AuthKeyRef.Name != ""

	authKeyCount := 0
	if hasInlineKey {
		authKeyCount++
	}
	if hasSecretRef {
		authKeyCount++
	}
	if hasAuthKeyRef {
		authKeyCount++
	}

	if authKeyCount == 0 {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "spec", "tailscale"),
			"one of authKey, authKeySecretRef, or authKeyRef must be specified",
		))
	}

	if authKeyCount > 1 {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "spec", "tailscale"),
			ts,
			"only one of authKey, authKeySecretRef, or authKeyRef can be specified",
		))
	}

	// Validate routes are valid CIDR format
	for i, route := range ts.AdvertiseRoutes {
		_, _, err := net.ParseCIDR(route)
		if err != nil {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "spec", "tailscale", "advertiseRoutes").Index(i),
				route,
				fmt.Sprintf("invalid CIDR format: %v", err),
			))
		}
	}

	if len(allErrs) == 0 {
		return nil
	}
	return allErrs.ToAggregate()
}

func collectConnectorWarnings(c *kodiakv1alpha1.Connector) admission.Warnings {
	var warnings admission.Warnings

	ts := c.Spec.Spec.Tailscale

	// Warn about inline auth key (security concern)
	if ts.AuthKey != "" {
		warnings = append(warnings, "using inline authKey is not recommended for production; consider using authKeySecretRef or authKeyRef instead")
	}

	return warnings
}
