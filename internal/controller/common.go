package controller

import (
	"errors"

	"github.com/bufbuild/connect-go"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// kodiakFinalizer is used across all controllers managed by this project to ensure
	// that we have a chance to perform external clean-up before Kubernetes removes the resource.
	kodiakFinalizer = "kodiak.mnicloud.jp/finalizer"
)

// isConnectNotFound checks if the error is a gRPC/Connect NotFound error.
func isConnectNotFound(err error) bool {
	if err == nil {
		return false
	}
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connectErr.Code() == connect.CodeNotFound
	}
	return false
}

func upsertCondition(conditions *[]metav1.Condition, condition metav1.Condition) {
	meta.SetStatusCondition(conditions, condition)
}
