package v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConditions(t *testing.T) {
	status := &PerconaServerMongoDBStatus{}

	cond := status.FindCondition(AppStateReady)
	assert.Nil(t, cond)

	status.AddCondition(ClusterCondition{
		Type:    AppStateReady,
		Status:  ConditionTrue,
		Reason:  "ClusterReady",
		Message: "Cluster is ready",
	})
	cond = status.FindCondition(AppStateReady)
	assert.NotNil(t, cond)
	assert.Equal(t, ConditionTrue, cond.Status)
	assert.Equal(t, "ClusterReady", cond.Reason)
	assert.Equal(t, "Cluster is ready", cond.Message)
	lastTransitionTime := cond.LastTransitionTime
	assert.NotNil(t, lastTransitionTime)
	assert.True(t, status.IsStatusConditionTrue(AppStateReady))

	status.AddCondition(ClusterCondition{
		Type:    AppStateReady,
		Status:  ConditionFalse,
		Reason:  "ClusterNotReady",
		Message: "Cluster is not ready",
	})
	cond = status.FindCondition(AppStateReady)
	assert.NotNil(t, cond)
	assert.Equal(t, ConditionFalse, cond.Status)
	assert.Equal(t, "ClusterNotReady", cond.Reason)
	assert.Equal(t, "Cluster is not ready", cond.Message)
	assert.NotNil(t, cond.LastTransitionTime)
	assert.True(t, cond.LastTransitionTime.After(lastTransitionTime.Time))
	assert.False(t, status.IsStatusConditionTrue(AppStateReady))

	status.RemoveCondition(AppStateReady)
	assert.False(t, status.IsStatusConditionTrue(AppStateReady))
	assert.Nil(t, status.FindCondition(AppStateReady))
}
