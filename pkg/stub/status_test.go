package stub

import (
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
)

func TestIsStatefulSetUpdating(t *testing.T) {
	ss := &appsv1.StatefulSet{
		Status: appsv1.StatefulSetStatus{
			CurrentReplicas: defaultMongodSize,
			ReadyReplicas:   defaultMongodSize,
			CurrentRevision: t.Name(),
			UpdateRevision:  t.Name(),
		},
	}
	assert.False(t, isStatefulSetUpdating(ss))

	ss.Status.UpdateRevision = ss.Status.UpdateRevision + "-true"
	assert.True(t, isStatefulSetUpdating(ss))
	ss.Status.UpdateRevision = t.Name()

	ss.Status.ReadyReplicas -= 1
	assert.True(t, isStatefulSetUpdating(ss))
}

//func TestGetReplsetStatus(t *testing.T) {}
//func TestGetReplsetMemberStatuses(t *testing.T) {}
//func TestHandlerUpdateStatus(t *testing.T) {}
