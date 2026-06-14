package perconaservermongodbrestore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func restoreScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, coordv1.AddToScheme(s))
	require.NoError(t, psmdbv1.SchemeBuilder.AddToScheme(s))
	return s
}

// TestCheckRestoreLocks_ClusterSyncBlocks isolates the new ClusterSync
// branch. The backup-lease branch and the PBM branch are unchanged and
// already covered by the existing suite; we only need to assert that
// the clustersync lease (a) gates the restore before PBM is consulted
// and (b) surfaces a ClusterSyncActive event with the cluster name.
func TestCheckRestoreLocks_ClusterSyncBlocks(t *testing.T) {
	const (
		ns          = "ns"
		clusterName = "tgt"
	)
	holder := "sync-uid"
	csLease := &coordv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.ClusterSyncLeaseName(clusterName),
			Namespace: ns,
		},
		Spec: coordv1.LeaseSpec{
			AcquireTime:    &metav1.MicroTime{Time: time.Now()},
			HolderIdentity: &holder,
		},
	}
	cr := &psmdbv1.PerconaServerMongoDBRestore{
		ObjectMeta: metav1.ObjectMeta{Name: "rst", Namespace: ns},
	}
	cluster := &psmdbv1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: ns},
	}

	cl := fake.NewClientBuilder().WithScheme(restoreScheme(t)).WithRuntimeObjects(csLease).Build()
	recorder := record.NewFakeRecorder(4)
	r := &ReconcilePerconaServerMongoDBRestore{client: cl, recorder: recorder}

	locked, err := r.checkRestoreLocks(context.Background(), cr, cluster)
	require.NoError(t, err)
	assert.True(t, locked, "clustersync lease must lock restores")

	select {
	case ev := <-recorder.Events:
		assert.Contains(t, ev, "ClusterSyncActive")
		assert.Contains(t, ev, clusterName)
	default:
		t.Fatal("expected ClusterSyncActive event, none emitted")
	}
}
