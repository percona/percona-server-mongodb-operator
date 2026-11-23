package perconaservermongodbclustersync

import (
	"context"
	"errors"
	"testing"

	pkgerrors "github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/clustersync"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

type recordingMongoClient struct {
	mongo.Client

	getUserInfoResp *mongo.User
	getUserInfoErr  error

	createUserErr     error
	updateUserPassErr error
	updateRolesErr    error

	createUserCalls     int
	updateUserPassCalls int
	updateRolesCalls    int
	disconnectCalls     int

	lastCreatePass  string
	lastUpdatePass  string
	lastUpdateRoles []mongo.Role
}

func (m *recordingMongoClient) Disconnect(context.Context) error {
	m.disconnectCalls++
	return nil
}

func (m *recordingMongoClient) GetUserInfo(_ context.Context, _, _ string) (*mongo.User, error) {
	return m.getUserInfoResp, m.getUserInfoErr
}

func (m *recordingMongoClient) CreateUser(_ context.Context, _, _, pwd string, _ ...mongo.Role) error {
	m.createUserCalls++
	m.lastCreatePass = pwd
	return m.createUserErr
}

func (m *recordingMongoClient) UpdateUserPass(_ context.Context, _, _, pwd string) error {
	m.updateUserPassCalls++
	m.lastUpdatePass = pwd
	return m.updateUserPassErr
}

func (m *recordingMongoClient) UpdateUserRoles(_ context.Context, _, _ string, roles []mongo.Role) error {
	m.updateRolesCalls++
	m.lastUpdateRoles = roles
	return m.updateRolesErr
}

func TestEnsureTargetMongoUser(t *testing.T) {
	t.Parallel()

	const (
		username = "clustersync-cr"
		password = "new-password"
	)
	passwordMAC := passwordAnnotationMAC([]byte(password))
	boom := errors.New("mongo down")

	secretWith := func(annotations map[string]string) *corev1.Secret {
		return &corev1.Secret{
			Annotations: annotations,
			Data: map[string][]byte{
				targetUserSecretUsernameKey: []byte(username),
				targetUserSecretPasswordKey: []byte(password),
			},
		}
	}
	existingWithDesiredRoles := &mongo.User{DB: "admin", Roles: append([]mongo.Role(nil), syncTargetUserRoles...)}

	tests := map[string]struct {
		mc          *recordingMongoClient
		sec         *corev1.Secret
		wantErrMsg  string
		wantMutated bool
		assert      func(t *testing.T, m *recordingMongoClient, s *corev1.Secret)
	}{
		"missing user is created and password hash annotation is set": {
			mc:          &recordingMongoClient{getUserInfoResp: nil},
			sec:         secretWith(nil),
			wantMutated: true,
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Equal(t, 1, m.createUserCalls)
				assert.Equal(t, password, m.lastCreatePass)
				assert.Zero(t, m.updateUserPassCalls)
				assert.Zero(t, m.updateRolesCalls)
				assert.Equal(t, passwordMAC, s.Annotations[targetUserPasswordMACAnnotation])
			},
		},
		"steady state converged does no writes": {
			mc:          &recordingMongoClient{getUserInfoResp: existingWithDesiredRoles},
			sec:         secretWith(map[string]string{targetUserPasswordMACAnnotation: passwordMAC}),
			wantMutated: false,
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Zero(t, m.createUserCalls)
				assert.Zero(t, m.updateUserPassCalls)
				assert.Zero(t, m.updateRolesCalls)
			},
		},
		"password hash mismatch triggers UpdateUserPass and annotation refresh": {
			mc:          &recordingMongoClient{getUserInfoResp: existingWithDesiredRoles},
			sec:         secretWith(map[string]string{targetUserPasswordMACAnnotation: "stale"}),
			wantMutated: true,
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Equal(t, 1, m.updateUserPassCalls)
				assert.Equal(t, password, m.lastUpdatePass)
				assert.Zero(t, m.updateRolesCalls)
				assert.Equal(t, passwordMAC, s.Annotations[targetUserPasswordMACAnnotation])
			},
		},
		"recreated secret with existing mongo user triggers UpdateUserPass and creates annotation map": {
			mc:          &recordingMongoClient{getUserInfoResp: existingWithDesiredRoles},
			sec:         secretWith(nil),
			wantMutated: true,
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Equal(t, 1, m.updateUserPassCalls)
				assert.Equal(t, password, m.lastUpdatePass)
				assert.Zero(t, m.updateRolesCalls)
				require.NotNil(t, s.Annotations)
				assert.Equal(t, passwordMAC, s.Annotations[targetUserPasswordMACAnnotation])
			},
		},
		"new cr reconcile against stale mongo user with drifted roles applies both password and roles": {
			mc: &recordingMongoClient{
				getUserInfoResp: &mongo.User{DB: "admin", Roles: []mongo.Role{{Role: "clusterMonitor", DB: "admin"}}},
			},
			sec:         secretWith(nil),
			wantMutated: true,
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Equal(t, 1, m.updateUserPassCalls)
				assert.Equal(t, password, m.lastUpdatePass)
				assert.Equal(t, 1, m.updateRolesCalls)
				assert.Equal(t, syncTargetUserRoles, m.lastUpdateRoles)
				assert.Equal(t, passwordMAC, s.Annotations[targetUserPasswordMACAnnotation])
			},
		},
		"role drift triggers UpdateUserRoles without touching password": {
			mc: &recordingMongoClient{
				getUserInfoResp: &mongo.User{DB: "admin", Roles: []mongo.Role{{Role: "clusterMonitor", DB: "admin"}}},
			},
			sec:         secretWith(map[string]string{targetUserPasswordMACAnnotation: passwordMAC}),
			wantMutated: false,
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Zero(t, m.updateUserPassCalls)
				assert.Equal(t, 1, m.updateRolesCalls)
				assert.Equal(t, syncTargetUserRoles, m.lastUpdateRoles)
			},
		},
		"roles in a different order are treated as equal": {
			mc: &recordingMongoClient{
				getUserInfoResp: &mongo.User{DB: "admin", Roles: []mongo.Role{
					{Role: "readWriteAnyDatabase", DB: "admin"},
					{Role: "clusterManager", DB: "admin"},
					{Role: "clusterMonitor", DB: "admin"},
					{Role: "restore", DB: "admin"},
				}},
			},
			sec:         secretWith(map[string]string{targetUserPasswordMACAnnotation: passwordMAC}),
			wantMutated: false,
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Zero(t, m.updateRolesCalls)
			},
		},
		"GetUserInfo error short-circuits with no writes": {
			mc:         &recordingMongoClient{getUserInfoErr: boom},
			sec:        secretWith(nil),
			wantErrMsg: "look up target user: mongo down",
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Zero(t, m.createUserCalls)
				assert.Zero(t, m.updateUserPassCalls)
				assert.Zero(t, m.updateRolesCalls)
			},
		},
		"CreateUser error is wrapped": {
			mc:         &recordingMongoClient{createUserErr: boom},
			sec:        secretWith(nil),
			wantErrMsg: `create target user "clustersync-cr": mongo down`,
		},
		"UpdateUserPass error stops before UpdateUserRoles": {
			mc: &recordingMongoClient{
				getUserInfoResp:   &mongo.User{DB: "admin"},
				updateUserPassErr: boom,
			},
			sec:        secretWith(nil),
			wantErrMsg: `sync target user password "clustersync-cr": mongo down`,
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Zero(t, m.updateRolesCalls)
			},
		},
		"UpdateUserRoles error is wrapped": {
			mc: &recordingMongoClient{
				getUserInfoResp: &mongo.User{DB: "admin"},
				updateRolesErr:  boom,
			},
			sec:        secretWith(map[string]string{targetUserPasswordMACAnnotation: passwordMAC}),
			wantErrMsg: `sync target user roles "clustersync-cr": mongo down`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			mutated, err := ensureTargetMongoUser(t.Context(), tc.mc, tc.sec)
			if tc.wantErrMsg != "" {
				require.EqualError(t, err, tc.wantErrMsg)
				require.ErrorIs(t, err, boom)
				assert.False(t, mutated)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantMutated, mutated)
			}
			if tc.assert != nil {
				tc.assert(t, tc.mc, tc.sec)
			}
		})
	}
}

func TestEnsureSyncTargetUser(t *testing.T) {
	const password = "stored-password"
	boom := errors.New("apiserver down")
	cr := &psmdbv1.PerconaServerMongoDBClusterSync{Name: "sync", Namespace: "ns", UID: "u"}
	target := &psmdbv1.PerconaServerMongoDB{Name: "tgt", Namespace: "ns"}
	secretName := clustersync.TargetUserSecretName(cr)
	userSecret := func(data map[string][]byte, annotations map[string]string) *corev1.Secret {
		return &corev1.Secret{Name: secretName, Namespace: "ns", Data: data, Annotations: annotations}
	}
	complete := map[string][]byte{
		targetUserSecretUsernameKey: []byte("clustersync-sync"),
		targetUserSecretPasswordKey: []byte(password),
	}
	converged := &mongo.User{DB: "admin", Roles: append([]mongo.Role(nil), syncTargetUserRoles...)}
	mac := passwordAnnotationMAC([]byte(password))

	tests := map[string]struct {
		existing       *corev1.Secret
		getErr         error
		createErr      error
		updateErr      error
		emptyScheme    bool
		mc             *recordingMongoClient
		dialErr        error
		wantErrMsg     string
		wantErrKind    func(error) bool
		wantDialed     bool
		wantSecretSave bool
		assert         func(t *testing.T, m *recordingMongoClient, stored *corev1.Secret)
	}{
		"missing secret is generated and the user is created": {
			mc:             &recordingMongoClient{},
			wantDialed:     true,
			wantSecretSave: true,
			assert: func(t *testing.T, m *recordingMongoClient, stored *corev1.Secret) {
				assert.Equal(t, "clustersync-sync", string(stored.Data[targetUserSecretUsernameKey]))
				assert.NotEmpty(t, stored.Data[targetUserSecretPasswordKey])
				assert.Equal(t, clustersync.Labels(cr), stored.Labels)
				require.Len(t, stored.OwnerReferences, 1, "secret must be garbage-collected with the CR")
				assert.Equal(t, cr.Name, stored.OwnerReferences[0].Name)
				assert.Equal(t, 1, m.createUserCalls)
				assert.Equal(t, string(stored.Data[targetUserSecretPasswordKey]), m.lastCreatePass)
				assert.Equal(t, passwordAnnotationMAC(stored.Data[targetUserSecretPasswordKey]),
					stored.Annotations[targetUserPasswordMACAnnotation])
			},
		},
		"converged secret and user cause no writes": {
			existing:   userSecret(complete, map[string]string{targetUserPasswordMACAnnotation: mac}),
			mc:         &recordingMongoClient{getUserInfoResp: converged},
			wantDialed: true,
			assert: func(t *testing.T, m *recordingMongoClient, _ *corev1.Secret) {
				assert.Zero(t, m.createUserCalls+m.updateUserPassCalls+m.updateRolesCalls)
			},
		},
		"stale password annotation syncs the password and persists the annotation": {
			existing:       userSecret(complete, map[string]string{targetUserPasswordMACAnnotation: "stale"}),
			mc:             &recordingMongoClient{getUserInfoResp: converged},
			wantDialed:     true,
			wantSecretSave: true,
			assert: func(t *testing.T, m *recordingMongoClient, stored *corev1.Secret) {
				assert.Equal(t, 1, m.updateUserPassCalls)
				assert.Equal(t, password, m.lastUpdatePass)
				assert.Equal(t, mac, stored.Annotations[targetUserPasswordMACAnnotation])
			},
		},
		"secret without a username is rejected before dialing": {
			existing:   userSecret(map[string][]byte{targetUserSecretPasswordKey: []byte(password)}, nil),
			wantErrMsg: "ensure target user secret: target user secret ns/" + secretName + " missing username key",
		},
		"secret without a password is rejected before dialing": {
			existing:   userSecret(map[string][]byte{targetUserSecretUsernameKey: []byte("clustersync-sync")}, nil),
			wantErrMsg: "ensure target user secret: target user secret ns/" + secretName + " missing password key",
		},
		"secret read failure is returned": {
			getErr:     boom,
			wantErrMsg: "ensure target user secret: get target user secret ns/" + secretName + ": apiserver down",
		},
		"owner reference failure is returned": {
			emptyScheme: true,
			wantErrKind: runtime.IsNotRegisteredError,
		},
		"secret create failure is returned": {
			createErr:  boom,
			wantErrMsg: "ensure target user secret: create target user secret ns/" + secretName + ": apiserver down",
		},
		"dial failure is returned": {
			existing:   userSecret(complete, nil),
			dialErr:    boom,
			wantDialed: true,
			wantErrMsg: "connect to target cluster: apiserver down",
		},
		"mongo user failure is returned and the client is still closed": {
			existing:   userSecret(complete, nil),
			mc:         &recordingMongoClient{getUserInfoErr: boom},
			wantDialed: true,
			wantErrMsg: "ensure target mongo user: look up target user: apiserver down",
		},
		"annotation update failure is returned": {
			existing:   userSecret(complete, nil),
			updateErr:  boom,
			mc:         &recordingMongoClient{getUserInfoResp: converged},
			wantDialed: true,
			wantErrMsg: "update target user secret annotations: apiserver down",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := clusterSyncScheme(t)
			require.NoError(t, corev1.AddToScheme(s))
			b := fake.NewClientBuilder().WithScheme(s).WithObjects(cr).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c k8sclient.WithWatch, key k8sclient.ObjectKey, obj k8sclient.Object, opts ...k8sclient.GetOption) error {
						if _, ok := obj.(*corev1.Secret); ok && tc.getErr != nil {
							return tc.getErr
						}
						return c.Get(ctx, key, obj, opts...)
					},
					Create: func(ctx context.Context, c k8sclient.WithWatch, obj k8sclient.Object, opts ...k8sclient.CreateOption) error {
						if tc.createErr != nil {
							return tc.createErr
						}
						return c.Create(ctx, obj, opts...)
					},
					Update: func(ctx context.Context, c k8sclient.WithWatch, obj k8sclient.Object, opts ...k8sclient.UpdateOption) error {
						if tc.updateErr != nil {
							return tc.updateErr
						}
						return c.Update(ctx, obj, opts...)
					},
				})
			if tc.existing != nil {
				b = b.WithObjects(tc.existing)
			}
			cl := b.Build()
			ownerScheme := s
			if tc.emptyScheme {
				ownerScheme = runtime.NewScheme()
			}
			dialed := 0
			r := &ReconcilePerconaServerMongoDBClusterSync{
				client: cl,
				scheme: ownerScheme,
				newTargetMongoClient: func(_ context.Context, _ k8sclient.Client, got *psmdbv1.PerconaServerMongoDB) (mongo.Client, error) {
					dialed++
					assert.Same(t, target, got)
					if tc.dialErr != nil {
						return nil, tc.dialErr
					}
					if tc.mc == nil {
						return &recordingMongoClient{}, nil
					}
					return tc.mc, nil
				},
			}

			nn := types.NamespacedName{Name: secretName, Namespace: "ns"}
			before := &corev1.Secret{}
			if tc.existing != nil {
				require.NoError(t, cl.Get(t.Context(), nn, before))
			}

			creds, err := r.ensureSyncTargetUser(t.Context(), cr, target)
			assert.Equal(t, tc.wantDialed, dialed == 1)
			if tc.mc != nil {
				assert.Equal(t, 1, tc.mc.disconnectCalls, "target client must be closed on every path")
			}
			if tc.wantErrKind != nil {
				require.True(t, tc.wantErrKind(pkgerrors.Cause(err)), "unexpected error: %v", err)
				assert.Zero(t, creds)
				return
			}
			if tc.wantErrMsg != "" {
				require.EqualError(t, err, tc.wantErrMsg)
				assert.Zero(t, creds)
				return
			}
			require.NoError(t, err)

			stored := &corev1.Secret{}
			require.NoError(t, cl.Get(t.Context(), nn, stored))
			assert.Equal(t, string(stored.Data[targetUserSecretUsernameKey]), creds.Username)
			assert.Equal(t, string(stored.Data[targetUserSecretPasswordKey]), creds.Password)
			assert.Equal(t, tc.wantSecretSave, stored.ResourceVersion != before.ResourceVersion)
			if tc.assert != nil {
				tc.assert(t, tc.mc, stored)
			}
		})
	}
}
