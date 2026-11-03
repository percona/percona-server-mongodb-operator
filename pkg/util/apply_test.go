package util

import (
	"context"
	"errors"
	"testing"

	cm "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestGetObjectHashClusterIssuer(t *testing.T) {
	t.Parallel()

	spec := cm.IssuerSpec{
		CA: &cm.CAIssuer{SecretName: "psmdb-ca-cert"},
	}

	desired := &cm.ClusterIssuer{
		Name: "psmdb-issuer",
		Spec: spec,
	}
	existing := &cm.ClusterIssuer{
		Name:            "psmdb-issuer",
		ResourceVersion: "42",
		Annotations:     map[string]string{"some-random-annotation": "true"},
		Spec:            spec,
		Status: cm.IssuerStatus{
			Conditions: []cm.IssuerCondition{{Type: cm.IssuerConditionReady, Status: cmmeta.ConditionTrue}},
		},
	}

	desiredHash, err := getObjectHash(desired)
	require.NoError(t, err)
	existingHash, err := getObjectHash(existing)
	require.NoError(t, err)

	assert.Equal(t, desiredHash, existingHash)
}

func TestApplyIfSkipsUnownedObject(t *testing.T) {
	t.Parallel()

	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	s.AddKnownTypes(cm.SchemeGroupVersion, new(cm.ClusterIssuer))

	userIssuer := &cm.ClusterIssuer{
		Name: "psmdb-issuer", Labels: map[string]string{"owner": "user"},
		Spec: cm.IssuerSpec{
			IssuerConfig: cm.IssuerConfig{CA: &cm.CAIssuer{SecretName: "user-ca"}},
		},
	}

	canWrite := func(obj client.Object) bool { return obj.GetLabels()["owner"] == "operator" }

	tests := map[string]struct {
		staleRead bool
	}{
		"object already exists":       {staleRead: false},
		"object created concurrently": {staleRead: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(s).WithObjects(userIssuer.DeepCopy())
			if tc.staleRead {
				firstGet := true
				builder = builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if firstGet {
							firstGet = false
							return k8serrors.NewNotFound(cm.Resource("clusterissuers"), key.Name)
						}
						return cl.Get(ctx, key, obj, opts...)
					},
				})
			}
			cl := builder.Build()

			desired := &cm.ClusterIssuer{
				Name: userIssuer.Name, Labels: map[string]string{"owner": "operator"},
				Spec: cm.IssuerSpec{
					IssuerConfig: cm.IssuerConfig{CA: &cm.CAIssuer{SecretName: "operator-ca"}},
				},
			}

			status, err := ApplyIf(t.Context(), cl, desired, canWrite)
			require.NoError(t, err)
			assert.Equal(t, ApplyStatusUnchanged, status)

			stored := new(cm.ClusterIssuer)
			require.NoError(t, cl.Get(t.Context(), types.NamespacedName{Name: userIssuer.Name}, stored))
			assert.Equal(t, userIssuer.Spec, stored.Spec)
			assert.Equal(t, userIssuer.Labels, stored.Labels)
		})
	}
}

func TestApplyIfObjectDeletedBetweenReadAndUpdate(t *testing.T) {
	t.Parallel()

	nn := types.NamespacedName{Name: "some-name-rs0", Namespace: "psmdb"}

	cached := &appsv1.StatefulSet{
		Name:            nn.Name,
		Namespace:       nn.Namespace,
		ResourceVersion: "42",
		Annotations:     map[string]string{"percona.com/last-config-hash": "stale"},
	}

	gr := schema.GroupResource{Group: "apps", Resource: "statefulsets"}
	notFound := k8serrors.NewNotFound(gr, nn.Name)
	conflict := k8serrors.NewConflict(gr, nn.Name, errors.New("modified"))
	forbidden := k8serrors.NewForbidden(gr, nn.Name, errors.New("quota"))

	tests := map[string]struct {
		stored      []client.Object
		canWrite    func(client.Object) bool
		updateErr   error
		createErr   error
		want        ApplyStatus
		wantErr     func(error) bool
		wantUpdates int
		wantCreates int
	}{
		"recreated when the object is gone": {
			updateErr:   notFound,
			want:        ApplyStatusCreated,
			wantUpdates: 1,
			wantCreates: 1,
		},
		"object reappeared before the create": {
			stored:      []client.Object{cached.DeepCopy()},
			updateErr:   notFound,
			wantErr:     k8serrors.IsAlreadyExists,
			wantUpdates: 1,
			wantCreates: 1,
		},
		"create fails": {
			updateErr:   notFound,
			createErr:   forbidden,
			wantErr:     k8serrors.IsForbidden,
			wantUpdates: 1,
			wantCreates: 1,
		},
		"update fails with another error": {
			updateErr:   conflict,
			want:        ApplyStatusUpdated,
			wantErr:     k8serrors.IsConflict,
			wantUpdates: 1,
		},
		"rejected object is not written": {
			canWrite: func(client.Object) bool { return false },
			want:     ApplyStatusUnchanged,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var updates, creates int
			var created *appsv1.StatefulSet

			cl := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(tt.stored...).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
						cached.DeepCopyInto(obj.(*appsv1.StatefulSet))
						return nil
					},
					Update: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.UpdateOption) error {
						updates++
						return tt.updateErr
					},
					Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						creates++
						created = obj.(*appsv1.StatefulSet).DeepCopy()
						if tt.createErr != nil {
							return tt.createErr
						}
						return c.Create(ctx, obj, opts...)
					},
				}).
				Build()

			desired := &appsv1.StatefulSet{
				Name: nn.Name, Namespace: nn.Namespace,
				Spec: appsv1.StatefulSetSpec{ServiceName: nn.Name},
			}

			canWrite := tt.canWrite
			if canWrite == nil {
				canWrite = func(client.Object) bool { return true }
			}

			status, err := ApplyIf(t.Context(), cl, desired, canWrite)
			assert.Equal(t, tt.want, status)
			assert.Equal(t, tt.wantUpdates, updates)
			assert.Equal(t, tt.wantCreates, creates)

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.True(t, tt.wantErr(err), "unexpected error: %v", err)
				return
			}
			require.NoError(t, err)

			if tt.wantCreates == 0 {
				return
			}
			assert.Empty(t, created.ResourceVersion)
			assert.Equal(t, desired.Spec, created.Spec)
			assert.NotEqual(t, "stale", created.Annotations["percona.com/last-config-hash"])
			assert.Equal(t, desired.Annotations["percona.com/last-config-hash"], created.Annotations["percona.com/last-config-hash"])
		})
	}
}
