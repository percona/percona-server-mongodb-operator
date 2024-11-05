package perconaservermongodb

import (
	"context"
	"os"
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/version"
)

func TestReconcileStatefulSet(t *testing.T) {
	ctx := context.Background()

	const (
		ns     = "reconcile-statefulset"
		crName = ns + "-cr"
	)

	defaultCR := readDefaultCR(t, crName, ns)
	defaultCR.Spec.Replsets[0].NonVoting.Enabled = true
	if err := defaultCR.CheckNSetDefaults(ctx, version.PlatformKubernetes); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		cr        *api.PerconaServerMongoDB
		rsName    string
		component string
		ls        map[string]string

		expectedSts *appsv1.StatefulSet
	}{
		{
			name:        "rs0-mongod",
			cr:          defaultCR.DeepCopy(),
			rsName:      "rs0",
			component:   "mongod",
			expectedSts: expectedSts(t, "reconcile-statefulset/rs0-mongod.yaml"),
		},
		{
			name:        "rs0-arbiter",
			cr:          defaultCR.DeepCopy(),
			rsName:      "rs0",
			component:   "arbiter",
			expectedSts: expectedSts(t, "reconcile-statefulset/rs0-arbiter.yaml"),
		},
		{
			name:        "rs0-non-voting",
			cr:          defaultCR.DeepCopy(),
			rsName:      "rs0",
			component:   "nonVoting",
			expectedSts: expectedSts(t, "reconcile-statefulset/rs0-nv.yaml"),
		},
		{
			name:        "cfg-mongod",
			cr:          defaultCR.DeepCopy(),
			rsName:      "cfg",
			component:   "mongod",
			expectedSts: expectedSts(t, "reconcile-statefulset/cfg-mongod.yaml"),
		},
		{
			name:        "cfg-arbiter",
			cr:          defaultCR.DeepCopy(),
			rsName:      "cfg",
			component:   "arbiter",
			expectedSts: expectedSts(t, "reconcile-statefulset/cfg-arbiter.yaml"),
		},
		{
			name:        "cfg-non-voting",
			cr:          defaultCR.DeepCopy(),
			rsName:      "cfg",
			component:   "nonVoting",
			expectedSts: expectedSts(t, "reconcile-statefulset/cfg-nv.yaml"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := buildFakeClient(tt.cr, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      crName + "-ssl",
					Namespace: tt.cr.Namespace,
				},
			}, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      crName + "-ssl-internal",
					Namespace: tt.cr.Namespace,
				},
			})

			rs := tt.cr.Spec.Replset(tt.rsName)

			var ls map[string]string
			switch tt.component {
			case "mongod":
				ls = naming.MongodLabels(tt.cr, rs)
			case "arbiter":
				ls = naming.ArbiterLabels(tt.cr, rs)
			case "nonVoting":
				ls = naming.NonVotingLabels(tt.cr, rs)
			default:
				t.Fatalf("unexpected component: %s", tt.component)
			}

			sts, err := r.reconcileStatefulSet(ctx, tt.cr, rs, ls)
			if err != nil {
				t.Fatalf("reconcileStatefulSet() error = %v", err)
			}

			compareSts(t, sts, tt.expectedSts)
		})
	}
}

func expectedSts(t *testing.T, filename string) *appsv1.StatefulSet {
	t.Helper()

	data, err := os.ReadFile("testdata/" + filename)
	if err != nil {
		t.Fatal(err)
	}
	sts := new(appsv1.StatefulSet)

	if err := yaml.Unmarshal(data, sts); err != nil {
		t.Fatal(err)
	}

	return sts
}

func compareSts(t *testing.T, got, want *appsv1.StatefulSet) {
	t.Helper()

	if !reflect.DeepEqual(got.TypeMeta, want.TypeMeta) {
		t.Fatalf("expected sts typemeta: %v, got: %v", want.TypeMeta, got.TypeMeta)
	}
	compareObjectMeta := func(got, want metav1.ObjectMeta) {
		delete(got.Annotations, "percona.com/last-config-hash")
		gotBytes, err := yaml.Marshal(got)
		if err != nil {
			t.Fatalf("error marshaling got: %v", err)
		}
		wantBytes, err := yaml.Marshal(want)
		if err != nil {
			t.Fatalf("error marshaling want: %v", err)
		}
		if string(gotBytes) != string(wantBytes) {
			t.Fatalf("expected sts object meta:\n%s\ngot:\n%s", string(wantBytes), string(gotBytes))
		}
	}
	compareObjectMeta(got.ObjectMeta, want.ObjectMeta)

	compareSpec := func(got, want appsv1.StatefulSetSpec) {
		delete(got.Template.Annotations, "percona.com/ssl-hash")
		delete(got.Template.Annotations, "percona.com/ssl-internal-hash")
		gotBytes, err := yaml.Marshal(got)
		if err != nil {
			t.Fatalf("error marshaling got: %v", err)
		}
		wantBytes, err := yaml.Marshal(want)
		if err != nil {
			t.Fatalf("error marshaling want: %v", err)
		}
		if string(gotBytes) != string(wantBytes) {
			t.Fatalf("expected sts spec:\n%s\ngot:\n%s", string(wantBytes), string(gotBytes))
		}
	}
	compareSpec(got.Spec, want.Spec)

	if !reflect.DeepEqual(got.Status, want.Status) {
		t.Fatalf("expected sts status: %v, got: %v", want.Status, got.Status)
	}
}
