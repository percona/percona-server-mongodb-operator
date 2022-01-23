package perconaservermongodb

import (
	"context"
	"testing"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake" // nolint
)

// creates a fake client to mock API calls with the mock objects
func buildFakeClient(objs []runtime.Object) *ReconcilePerconaServerMongoDB {
	s := scheme.Scheme

	s.AddKnownTypes(api.SchemeGroupVersion, &api.PerconaServerMongoDB{})

	cl := fake.NewFakeClientWithScheme(s, objs...)

	return &ReconcilePerconaServerMongoDB{client: cl, scheme: s}
}

func TestUpdateStatus(t *testing.T) {
	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "psmdb-mock", Namespace: "psmdb"},
		Spec: api.PerconaServerMongoDBSpec{
			Replsets: []*api.ReplsetSpec{{Name: "rs0", Size: 3}, {Name: "rs1", Size: 3}},
			Sharding: api.Sharding{Enabled: true, Mongos: &api.MongosSpec{Size: 3}},
		},
	}

	rs0 := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "psmdb-mock-rs0", Namespace: "psmdb"}}
	rs1 := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "psmdb-mock-rs1", Namespace: "psmdb"}}

	r := buildFakeClient([]runtime.Object{cr, rs0, rs1})

	if err := r.updateStatus(context.TODO(), cr, nil, api.AppStateInit); err != nil {
		t.Error(err)
	}

	if cr.Status.State != api.AppStateInit {
		t.Errorf("cr.Status.State got %#v, want %#v", cr.Status.State, api.AppStateInit)
	}
}
