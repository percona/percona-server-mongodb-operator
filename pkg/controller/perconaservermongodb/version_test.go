package perconaservermongodb

import (
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	k8sversion "k8s.io/apimachinery/pkg/version"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/percona/percona-server-mongodb-operator/pkg/apis"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
	"github.com/percona/percona-server-mongodb-operator/pkg/versionservice"
)

type fakeVersionService struct {
	dep       versionservice.Dep
	err       error
	calls     int
	endpoints []string
	meta      versionservice.Meta
}

func (f *fakeVersionService) GetExactVersion(_ *api.PerconaServerMongoDB, endpoint string, vm versionservice.Meta, _ versionservice.Options) (versionservice.Dep, error) {
	f.calls++
	f.endpoints = append(f.endpoints, endpoint)
	f.meta = vm
	return f.dep, f.err
}

func fakeReconciler(t *testing.T, objs ...client.Object) *ReconcilePerconaServerMongoDB {
	t.Helper()

	s := k8sruntime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s), "add client-go scheme")
	require.NoError(t, apis.AddToScheme(s), "add apis scheme")

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).WithStatusSubresource(objs...).Build()

	crons := NewCronRegistry()
	t.Cleanup(func() { crons.crons.Stop() })

	return &ReconcilePerconaServerMongoDB{
		client:  cl,
		scheme:  s,
		crons:   crons,
		lockers: newLockStore(),
		serverVersion: &version.ServerVersion{
			Platform: version.PlatformKubernetes,
			Info:     k8sversion.Info{GitVersion: "v1.30.0"},
		},
	}
}

func fakeCR(t *testing.T, name, namespace string) *api.PerconaServerMongoDB {
	t.Helper()

	return &api.PerconaServerMongoDB{
		Name:      name,
		Namespace: namespace,
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: version.Version(),
			Image:     "percona/percona-server-mongodb:8.0.4-1",
			Replsets: []*api.ReplsetSpec{
				{
					Name:       "rs0",
					Size:       3,
					VolumeSpec: fakeVolumeSpec(t),
				},
			},
		},
	}
}

func setTelemetry(t *testing.T, enabled bool) {
	t.Helper()

	t.Setenv("DISABLE_TELEMETRY", strconv.FormatBool(!enabled))
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()

	old, ok := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	if ok {
		t.Cleanup(func() { require.NoError(t, os.Setenv(key, old)) })
	}
}

func fakeOperatorDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		Name:      "percona-server-mongodb-operator",
		Namespace: "some-namespace",
		Labels:    map[string]string{},
	}
}

func TestJobName(t *testing.T) {
	cr := fakeCR(t, "some-name", "some-namespace")

	assert.Equal(t, "ensure-version/some-namespace/some-name", jobName(ensureVersionPrefix, cr))
	assert.Equal(t, "telemetry/some-namespace/some-name", jobName(telemetryPrefix, cr))
}

func TestDeleteCronJob(t *testing.T) {
	tests := map[string]struct {
		stored any
	}{
		"removes a stored schedule": {stored: jobSchedule{CronSchedule: "0 0 * * *"}},
		"ignores an unknown key":    {},
		"ignores a wrong type":      {stored: "not-a-schedule"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := fakeReconciler(t)
			if tc.stored != nil {
				r.crons.ensureVersionJobs.Store("job", tc.stored)
			}

			r.deleteCronJob("job")

			_, ok := r.crons.ensureVersionJobs.Load("job")
			assert.False(t, ok, "job must not remain in the registry")
		})
	}
}

func TestScheduleEnsureVersion(t *testing.T) {
	ctx := t.Context()

	tests := map[string]struct {
		schedule         string
		apply            api.UpgradeStrategy
		disableTelemetry bool
		stored           *jobSchedule
		wantScheduled    string
		wantErrMsg       string
	}{
		"empty schedule stores no job": {
			apply: api.UpgradeStrategyRecommended,
		},
		"empty schedule removes an existing job": {
			apply:  api.UpgradeStrategyRecommended,
			stored: &jobSchedule{CronSchedule: "0 0 * * *"},
		},
		"upgrades and telemetry both disabled removes an existing job": {
			schedule:         "0 0 1 1 *",
			apply:            api.UpgradeStrategyDisabled,
			disableTelemetry: true,
			stored:           &jobSchedule{CronSchedule: "0 0 1 1 *"},
		},
		"new job is scheduled": {
			schedule:      "0 0 1 1 *",
			apply:         api.UpgradeStrategyRecommended,
			wantScheduled: "0 0 1 1 *",
		},
		"telemetry alone is enough to schedule": {
			schedule:      "0 0 1 1 *",
			apply:         api.UpgradeStrategyDisabled,
			wantScheduled: "0 0 1 1 *",
		},
		"unchanged schedule is a no-op": {
			schedule:      "0 0 1 1 *",
			apply:         api.UpgradeStrategyRecommended,
			stored:        &jobSchedule{CronSchedule: "0 0 1 1 *"},
			wantScheduled: "0 0 1 1 *",
		},
		"changed schedule replaces the job": {
			schedule:      "0 0 2 1 *",
			apply:         api.UpgradeStrategyRecommended,
			stored:        &jobSchedule{CronSchedule: "0 0 1 1 *"},
			wantScheduled: "0 0 2 1 *",
		},
		"invalid cron expression": {
			schedule:   "not-a-cron",
			apply:      api.UpgradeStrategyRecommended,
			wantErrMsg: "failed to parse cron schedule",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			setTelemetry(t, !tc.disableTelemetry)

			cr := fakeCR(t, "some-name", "some-namespace")
			cr.Spec.UpgradeOptions.Schedule = tc.schedule
			cr.Spec.UpgradeOptions.Apply = tc.apply

			r := fakeReconciler(t, cr)
			jn := jobName(ensureVersionPrefix, cr)
			if tc.stored != nil {
				r.crons.ensureVersionJobs.Store(jn, *tc.stored)
			}

			err := r.scheduleEnsureVersion(ctx, cr, &fakeVersionService{})
			if tc.wantErrMsg != "" {
				require.ErrorContains(t, err, tc.wantErrMsg)
				return
			}
			require.NoError(t, err)

			stored, ok := r.crons.ensureVersionJobs.Load(jn)
			if tc.wantScheduled == "" {
				assert.False(t, ok, "no job should be registered")
				return
			}
			require.True(t, ok, "job should be registered")
			assert.Equal(t, tc.wantScheduled, stored.(jobSchedule).CronSchedule)
		})
	}
}

func TestScheduleTelemetryRequestsDisabled(t *testing.T) {
	ctx := t.Context()
	setTelemetry(t, false)

	cr := fakeCR(t, "some-name", "some-namespace")
	r := fakeReconciler(t, cr)

	jn := jobName(telemetryPrefix, cr)
	r.crons.ensureVersionJobs.Store(jn, jobSchedule{CronSchedule: "0 0 1 1 *"})

	require.NoError(t, r.scheduleTelemetryRequests(ctx, cr, &fakeVersionService{}))

	_, ok := r.crons.ensureVersionJobs.Load(jn)
	assert.False(t, ok, "telemetry job must be removed when telemetry is disabled")
}

func TestScheduleTelemetryRequestsKeepsRandomSchedule(t *testing.T) {
	ctx := t.Context()
	setTelemetry(t, true)
	unsetEnv(t, "TELEMETRY_SCHEDULE")

	cr := fakeCR(t, "some-name", "some-namespace")
	r := fakeReconciler(t, cr)

	jn := jobName(telemetryPrefix, cr)
	existing := jobSchedule{CronSchedule: "7 * * * *"}
	r.crons.ensureVersionJobs.Store(jn, existing)

	vs := &fakeVersionService{}
	require.NoError(t, r.scheduleTelemetryRequests(ctx, cr, vs))

	stored, ok := r.crons.ensureVersionJobs.Load(jn)
	require.True(t, ok)
	assert.Equal(t, existing, stored, "an existing job must survive when TELEMETRY_SCHEDULE is unset")
	assert.Zero(t, vs.calls, "no telemetry request should be sent when the job is left alone")
}

func TestBuildVersionMeta(t *testing.T) {
	ctx := t.Context()
	t.Setenv(k8s.WatchNamespaceEnvVar, "some-namespace")

	cr := fakeCR(t, "some-name", "some-namespace")
	require.NoError(t, cr.CheckNSetDefaults(ctx, version.PlatformKubernetes))

	r := fakeReconciler(t, cr)

	vm, err := r.buildVersionMeta(ctx, cr, fakeOperatorDeployment())
	require.NoError(t, err)

	assert.Equal(t, "v1.30.0", vm.KubeVersion, "kube version comes from the reconciler's server version")
	assert.Equal(t, version.Version(), vm.Version)
}

func TestGetNewVersions(t *testing.T) {
	ctx := t.Context()

	dep := versionservice.Dep{
		MongoImage:   "mongo-image",
		MongoVersion: "8.0.4-1",
	}

	tests := map[string]struct {
		apply            api.UpgradeStrategy
		endpoint         string
		vsErr            error
		disableTelemetry bool
		want             versionservice.Dep
		wantEndpoint     string
		wantErrMsg       string
	}{
		"upgrades disabled sends telemetry to the default endpoint": {
			apply:        api.UpgradeStrategyDisabled,
			endpoint:     api.GetDefaultVersionServiceEndpoint(),
			want:         versionservice.Dep{},
			wantEndpoint: api.GetDefaultVersionServiceEndpoint(),
		},
		"telemetry failure is swallowed": {
			apply:        api.UpgradeStrategyDisabled,
			endpoint:     api.GetDefaultVersionServiceEndpoint(),
			vsErr:        assert.AnError,
			want:         versionservice.Dep{},
			wantEndpoint: api.GetDefaultVersionServiceEndpoint(),
		},
		"custom endpoint is reported to the default endpoint first": {
			apply:        api.UpgradeStrategyRecommended,
			endpoint:     "https://custom.example.com/versions",
			want:         versionservice.Dep{},
			wantEndpoint: api.GetDefaultVersionServiceEndpoint(),
		},
		"upgrades enabled returns the resolved versions": {
			apply:        api.UpgradeStrategyRecommended,
			endpoint:     api.GetDefaultVersionServiceEndpoint(),
			want:         dep,
			wantEndpoint: api.GetDefaultVersionServiceEndpoint(),
		},
		"version service failure on the upgrade path": {
			apply:      api.UpgradeStrategyRecommended,
			endpoint:   api.GetDefaultVersionServiceEndpoint(),
			vsErr:      assert.AnError,
			wantErrMsg: "check version",
		},
		"telemetry disabled still resolves versions": {
			apply:            api.UpgradeStrategyRecommended,
			endpoint:         api.GetDefaultVersionServiceEndpoint(),
			disableTelemetry: true,
			want:             dep,
			wantEndpoint:     api.GetDefaultVersionServiceEndpoint(),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv(k8s.WatchNamespaceEnvVar, "some-namespace")
			setTelemetry(t, !tc.disableTelemetry)

			cr := fakeCR(t, "some-name", "some-namespace")
			cr.Spec.UpgradeOptions.Apply = tc.apply
			cr.Spec.UpgradeOptions.VersionServiceEndpoint = tc.endpoint
			require.NoError(t, cr.CheckNSetDefaults(ctx, version.PlatformKubernetes))
			cr.Spec.UpgradeOptions.VersionServiceEndpoint = tc.endpoint

			r := fakeReconciler(t, cr)
			vs := &fakeVersionService{dep: dep, err: tc.vsErr}

			got, err := r.getNewVersions(ctx, cr, vs, fakeOperatorDeployment())
			if tc.wantErrMsg != "" {
				require.ErrorContains(t, err, tc.wantErrMsg)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			require.Equal(t, 1, vs.calls, "version service should be called exactly once")
			assert.Equal(t, tc.wantEndpoint, vs.endpoints[0])
		})
	}
}

func TestEnsureVersionEarlyReturns(t *testing.T) {
	ctx := t.Context()

	tests := map[string]struct {
		apply      api.UpgradeStrategy
		state      api.AppState
		mongoVer   string
		wantErrMsg string
	}{
		"upgrades and telemetry both disabled": {
			apply: api.UpgradeStrategyDisabled,
		},
		"cluster is not ready": {
			apply:      api.UpgradeStrategyRecommended,
			state:      api.AppStateInit,
			mongoVer:   "8.0.4-1",
			wantErrMsg: "cluster is not ready",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			setTelemetry(t, false)

			cr := fakeCR(t, "some-name", "some-namespace")
			cr.Spec.UpgradeOptions.Apply = tc.apply
			cr.Status.State = tc.state
			cr.Status.MongoVersion = tc.mongoVer

			r := fakeReconciler(t, cr)
			vs := &fakeVersionService{}

			err := r.ensureVersion(ctx, cr, vs)
			if tc.wantErrMsg != "" {
				require.ErrorContains(t, err, tc.wantErrMsg)
			} else {
				require.NoError(t, err)
			}
			assert.Zero(t, vs.calls, "version service must not be contacted")
		})
	}
}

func TestIsPMM3Configured(t *testing.T) {
	ctx := t.Context()

	tests := map[string]struct {
		secretData map[string][]byte
		hasSecret  bool
		want       bool
	}{
		"missing secret is not an error": {},
		"secret without a token": {
			hasSecret:  true,
			secretData: map[string][]byte{"MONGODB_BACKUP_USER": []byte("backup")},
		},
		"secret with an empty token": {
			hasSecret:  true,
			secretData: map[string][]byte{api.PMMServerToken: []byte("")},
		},
		"secret with a PMM server token": {
			hasSecret:  true,
			secretData: map[string][]byte{api.PMMServerToken: []byte("token")},
			want:       true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := fakeCR(t, "some-name", "some-namespace")
			require.NoError(t, cr.CheckNSetDefaults(ctx, version.PlatformKubernetes), "set CR defaults")

			objs := []client.Object{cr}
			if tc.hasSecret {
				objs = append(objs, &corev1.Secret{
					Name:      api.UserSecretName(cr),
					Namespace: cr.Namespace,
					Data:      tc.secretData,
				})
			}
			r := fakeReconciler(t, objs...)

			got, err := r.isPMM3Configured(ctx, cr)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFetchVersionFromMongo(t *testing.T) {
	ctx := t.Context()

	tests := map[string]struct {
		mutate       func(cr *api.PerconaServerMongoDB)
		wantVersion  string
		wantFetched  bool
		wantMongoImg string
	}{
		"generation not yet observed": {
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Generation = 2
				cr.Status.ObservedGeneration = 1
			},
		},
		"cluster is not ready": {
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Status.State = api.AppStateInit
			},
		},
		"image already matches the status": {
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Status.MongoImage = cr.Spec.Image
			},
		},
		"version is fetched from the database": {
			mutate:       func(cr *api.PerconaServerMongoDB) {},
			wantFetched:  true,
			wantVersion:  "4.2",
			wantMongoImg: "percona/percona-server-mongodb:8.0.4-1",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := fakeCR(t, "some-name", "some-namespace")
			cr.Generation = 1
			cr.Status.ObservedGeneration = 1
			cr.Status.State = api.AppStateReady
			cr.Status.MongoImage = "percona/percona-server-mongodb:7.0.0-1"
			tc.mutate(cr)

			r := fakeReconciler(t, cr)
			connections := 0
			r.mongoClientProvider = &fakeMongoClientProvider{cr: cr, connectionCount: &connections}

			require.NoError(t, r.fetchVersionFromMongo(ctx, cr, cr.Spec.Replsets[0]))

			if !tc.wantFetched {
				assert.Zero(t, connections, "no connection should be opened")
				assert.Empty(t, cr.Status.MongoVersion)
				return
			}
			assert.Zero(t, connections, "the mongo session must be closed")
			assert.Equal(t, tc.wantVersion, cr.Status.MongoVersion)
			assert.Equal(t, tc.wantMongoImg, cr.Status.MongoImage)
		})
	}
}

var _ versionservice.Service = new(fakeVersionService)
