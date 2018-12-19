package util

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/config"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
)

func TestIsStatefulSetUpdating(t *testing.T) {
	ss := &appsv1.StatefulSet{
		Status: appsv1.StatefulSetStatus{
			CurrentReplicas: config.DefaultMongodSize,
			ReadyReplicas:   config.DefaultMongodSize,
			CurrentRevision: t.Name(),
			UpdateRevision:  t.Name(),
		},
	}

	// test success
	assert.False(t, IsStatefulSetUpdating(ss))

	// updateRevision != currentRevision
	ss.Status.UpdateRevision = ss.Status.UpdateRevision + "-true"
	assert.True(t, IsStatefulSetUpdating(ss))
	ss.Status.UpdateRevision = t.Name()

	// readyReplicas < currentReplicas
	assert.True(t, IsStatefulSetUpdating(&appsv1.StatefulSet{
		Status: appsv1.StatefulSetStatus{
			CurrentReplicas: config.DefaultMongodSize,
			ReadyReplicas:   config.DefaultMongodSize - 1,
			CurrentRevision: t.Name(),
			UpdateRevision:  t.Name(),
		},
	}))
}
