package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUpsertConditionRefreshesObservedGeneration(t *testing.T) {
	oldTransitionTime := metav1.NewTime(time.Unix(1, 0))
	newTransitionTime := metav1.NewTime(time.Unix(2, 0))
	conditions := []metav1.Condition{
		{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "ResourceUpdated",
			Message:            "resource updated",
			ObservedGeneration: 1,
			LastTransitionTime: oldTransitionTime,
		},
	}

	upsertCondition(&conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "ResourceUpdated",
		Message:            "resource updated",
		ObservedGeneration: 2,
		LastTransitionTime: newTransitionTime,
	})

	if got := conditions[0].ObservedGeneration; got != 2 {
		t.Fatalf("ObservedGeneration = %d, want 2", got)
	}
	if got := conditions[0].LastTransitionTime; !got.Equal(&newTransitionTime) {
		t.Fatalf("LastTransitionTime = %s, want %s", got, newTransitionTime)
	}
}
