package util

import (
	"context"
	"testing"

	cm "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestGetObjectHashClusterIssuer(t *testing.T) {
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
	s := scheme.Scheme
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
