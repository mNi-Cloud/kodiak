package controller

import (
	"errors"
	"strings"

	"github.com/bufbuild/connect-go"
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

// isConnectUnavailable checks if the error indicates the control server is unreachable.
// This includes DNS resolution errors, connection refused, timeouts, etc.
// During deletion, we treat these errors as non-fatal to avoid blocking resource cleanup
// when the control server is temporarily or permanently unavailable.
func isConnectUnavailable(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()

	// DNS resolution failure
	if strings.Contains(errStr, "no such host") {
		return true
	}

	// Connection refused
	if strings.Contains(errStr, "connection refused") {
		return true
	}

	// Connection timeout or reset
	if strings.Contains(errStr, "connection timed out") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "connection reset") {
		return true
	}

	// gRPC/Connect unavailable or deadline exceeded codes
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		code := connectErr.Code()
		return code == connect.CodeUnavailable || code == connect.CodeDeadlineExceeded
	}

	return false
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
