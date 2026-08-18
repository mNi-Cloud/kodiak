package controller

import (
	"errors"

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
			(*conditions)[i].Message == condition.Message &&
			(*conditions)[i].ObservedGeneration == condition.ObservedGeneration {
			return
		}

		(*conditions)[i] = condition
		return
	}

	*conditions = append(*conditions, condition)
}
