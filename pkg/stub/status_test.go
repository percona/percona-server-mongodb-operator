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

	// test success
	assert.False(t, isStatefulSetUpdating(ss))

	// updateRevision != currentRevision
	ss.Status.UpdateRevision = ss.Status.UpdateRevision + "-true"
	assert.True(t, isStatefulSetUpdating(ss))
	ss.Status.UpdateRevision = t.Name()

	// readyReplicas < currentReplicas
	assert.True(t, isStatefulSetUpdating(&appsv1.StatefulSet{
		Status: appsv1.StatefulSetStatus{
			CurrentReplicas: defaultMongodSize,
			ReadyReplicas:   defaultMongodSize - 1,
			CurrentRevision: t.Name(),
			UpdateRevision:  t.Name(),
		},
	}))
}

//func TestGetReplsetStatus(t *testing.T) {}
//func TestGetReplsetMemberStatuses(t *testing.T) {}
//func TestHandlerUpdateStatus(t *testing.T) {}
