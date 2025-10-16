package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
)

const (
	// kodiakFinalizer is used across all controllers managed by this project to ensure
	// that we have a chance to perform external clean-up before Kubernetes removes the resource.
	kodiakFinalizer = "kodiak.mnicloud.jp/finalizer"

	// adminKeySecretAnnotation allows users to override the secret that contains
	// the ionscale system admin key. If left empty, a default name is derived
	// from the ControlServer resource name.
	adminKeySecretAnnotation = "kodiak.mnicloud.jp/system-admin-key-secret"

	// oidcClientSecretEnv is the environment variable used when wiring the OIDC
	// client secret into the ionscale configuration.
	oidcClientSecretEnv = "OIDC_CLIENT_SECRET"

	// ionscaleAdminKeyEnv is the environment variable expected by the ionscale
	// binaries for bearer token authentication.
	ionscaleAdminKeyEnv = "IONSCALE_SYSTEM_ADMIN_KEY"
)

func defaultAdminSecretName(resourceName string) string {
	return fmt.Sprintf("%s-admin", resourceName)
}

func adminSecretNameFromAnnotations(obj metav1.Object) string {
	if obj == nil {
		return ""
	}
	if override, ok := obj.GetAnnotations()[adminKeySecretAnnotation]; ok && override != "" {
		return override
	}
	return defaultAdminSecretName(obj.GetName())
}

func readAdminKey(ctx context.Context, kubeClient client.Client, controlServer *kodiakv1alpha1.ControlServer) (string, string, error) {
	secretName := adminSecretNameFromAnnotations(controlServer)
	secret := &corev1.Secret{}
	if err := kubeClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: controlServer.Namespace}, secret); err != nil {
		return "", secretName, err
	}

	key := secretKeyOrDefault(secret, "systemAdminKey")
	value, exists := secret.Data[key]
	if !exists {
		return "", secretName, fmt.Errorf("secret %q does not contain key %q", secretName, key)
	}

	return strings.TrimSpace(string(value)), secretName, nil
}

func upsertCondition(conditions *[]metav1.Condition, condition metav1.Condition) {
	if conditions == nil {
		return
	}

	for i := range *conditions {
		if (*conditions)[i].Type != condition.Type {
			continue
		}

		if (*conditions)[i].Status == condition.Status &&
			(*conditions)[i].Reason == condition.Reason &&
			(*conditions)[i].Message == condition.Message {
			return
		}

		(*conditions)[i] = condition
		return
	}

	*conditions = append(*conditions, condition)
}
