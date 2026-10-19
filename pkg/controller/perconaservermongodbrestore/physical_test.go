package perconaservermongodbrestore

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestUpdateStatefulSetForPhysicalRestore(t *testing.T) {
	nonRoot := true
	allowPrivEsc := false
	initSC := &corev1.SecurityContext{
		RunAsNonRoot:             &nonRoot,
		AllowPrivilegeEscalation: &allowPrivEsc,
	}

	tests := []struct {
		name                string
		crVersion           string
		backupVersion       string
		storages            map[string]psmdbv1.BackupStorageSpec
		clusterInitSC       *corev1.SecurityContext
		wantPbmInitSC       *corev1.SecurityContext
		wantAWSChecksumEnvs bool
	}{
		{
			name:                "latest_version_with_InitContainerSecurityContext",
			crVersion:           version.Version(),
			backupVersion:       "2.11.0",
			storages:            s3CompatibleOSSStorage(),
			clusterInitSC:       initSC,
			wantPbmInitSC:       initSC,
			wantAWSChecksumEnvs: true,
		},
		{
			name:                "latest_version_without_InitContainerSecurityContext",
			crVersion:           version.Version(),
			backupVersion:       "2.12.0",
			storages:            s3CompatibleOSSStorage(),
			clusterInitSC:       nil,
			wantPbmInitSC:       nil,
			wantAWSChecksumEnvs: false,
		},
		{
			name:                "latest_version_with_regular_s3",
			crVersion:           version.Version(),
			backupVersion:       "2.11.0",
			storages:            regularS3Storage(),
			clusterInitSC:       nil,
			wantPbmInitSC:       nil,
			wantAWSChecksumEnvs: false,
		},
		{
			name:                "1_22_with_InitContainerSecurityContext_ignored",
			crVersion:           "1.22.0",
			backupVersion:       "2.11.0",
			storages:            s3CompatibleOSSStorage(),
			clusterInitSC:       initSC,
			wantPbmInitSC:       nil,
			wantAWSChecksumEnvs: false,
		},
		{
			name:                "1_22_without_InitContainerSecurityContext",
			crVersion:           "1.22.0",
			backupVersion:       "2.11.0",
			storages:            s3CompatibleOSSStorage(),
			clusterInitSC:       nil,
			wantPbmInitSC:       nil,
			wantAWSChecksumEnvs: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()

			cluster := &psmdbv1.PerconaServerMongoDB{
				Name:      "my-cluster",
				Namespace: "default",
				Spec: psmdbv1.PerconaServerMongoDBSpec{
					CRVersion: tt.crVersion,
					Backup: psmdbv1.BackupSpec{
						Image:    "percona/percona-backup-mongodb:latest",
						Storages: tt.storages,
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "extra-volume",
								MountPath: "/extra",
							},
						},
					},
					ImagePullPolicy: corev1.PullIfNotPresent,
					Secrets: &psmdbv1.SecretsSpec{
						Users: "users-secret",
						SSL:   "ssl-secret",
					},
					InitContainerSecurityContext: tt.clusterInitSC,
				},
				Status: psmdbv1.PerconaServerMongoDBStatus{
					BackupVersion: tt.backupVersion,
				},
			}

			sts := &appsv1.StatefulSet{
				Name:      "my-cluster-rs0",
				Namespace: "default",
				Spec: appsv1.StatefulSetSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "my-cluster"},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "my-cluster"},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "mongod",
									Image: "percona/percona-server-mongodb:latest",
								},
								{
									Name:  naming.ContainerBackupAgent,
									Image: "percona/percona-backup-agent:latest",
								},
							},
						},
					},
				},
			}

			secretTLS := &corev1.Secret{
				Name:      cluster.Spec.Secrets.SSL,
				Namespace: cluster.Namespace,
				Data: map[string][]byte{
					"ca.crt":  {},
					"tls.crt": {},
					"tls.key": {},
				},
			}

			r := fakeReconciler(cluster, sts, secretTLS)
			namespacedName := types.NamespacedName{
				Name:      sts.Name,
				Namespace: sts.Namespace,
			}

			err := r.updateStatefulSetForPhysicalRestore(ctx, cluster, namespacedName, 27017)
			assert.NoError(t, err)

			updatedSTS := &appsv1.StatefulSet{}
			err = r.client.Get(ctx, namespacedName, updatedSTS)
			assert.NoError(t, err)

			assert.Equal(t, "true", updatedSTS.Annotations[psmdbv1.AnnotationRestoreInProgress])

			for _, c := range updatedSTS.Spec.Template.Spec.Containers {
				assert.NotEqual(t, naming.ContainerBackupAgent, c.Name)
			}

			var pbmInit *corev1.Container
			for i := range updatedSTS.Spec.Template.Spec.InitContainers {
				if updatedSTS.Spec.Template.Spec.InitContainers[i].Name == "pbm-init" {
					pbmInit = &updatedSTS.Spec.Template.Spec.InitContainers[i]
					break
				}
			}
			assert.NotNil(t, pbmInit)
			assert.Equal(t, tt.wantPbmInitSC, pbmInit.SecurityContext)

			assert.Equal(t, "/opt/percona/physical-restore-ps-entry.sh", updatedSTS.Spec.Template.Spec.Containers[0].Command[0])

			assert.True(t,
				slices.ContainsFunc(updatedSTS.Spec.Template.Spec.Containers[0].VolumeMounts, func(c corev1.VolumeMount) bool {
					return c.MountPath == "/etc/pbm/"
				}))

			assert.Equal(t, tt.wantAWSChecksumEnvs,
				slices.ContainsFunc(updatedSTS.Spec.Template.Spec.Containers[0].Env, func(e corev1.EnvVar) bool {
					return e.Name == "AWS_REQUEST_CHECKSUM_CALCULATION" && e.Value == "when_required"
				}))
			assert.Equal(t, tt.wantAWSChecksumEnvs,
				slices.ContainsFunc(updatedSTS.Spec.Template.Spec.Containers[0].Env, func(e corev1.EnvVar) bool {
					return e.Name == "AWS_RESPONSE_CHECKSUM_VALIDATION" && e.Value == "when_required"
				}))

			lastEnvVar := updatedSTS.Spec.Template.Spec.Containers[0].Env[len(updatedSTS.Spec.Template.Spec.Containers[0].Env)-1]
			expectedURI := "mongodb://$(PBM_AGENT_MONGODB_USERNAME):$(PBM_AGENT_MONGODB_PASSWORD)@localhost:$(PBM_MONGODB_PORT)/?tls=true&tlsCertificateKeyFile=/tmp/tls.pem&tlsCAFile=/etc/mongodb-ssl/ca.crt&tlsInsecure=true"

			assert.Equal(t, "PBM_MONGODB_URI", lastEnvVar.Name)
			assert.Equal(t, expectedURI, lastEnvVar.Value)
		})
	}
}

func s3CompatibleOSSStorage() map[string]psmdbv1.BackupStorageSpec {
	return map[string]psmdbv1.BackupStorageSpec{
		"oss-s3": {
			Type: psmdbv1.BackupStorageS3,
			S3: psmdbv1.BackupStorageS3Spec{
				EndpointURL: "https://s3.oss-eu-central-1.aliyuncs.com",
			},
		},
	}
}

func regularS3Storage() map[string]psmdbv1.BackupStorageSpec {
	return map[string]psmdbv1.BackupStorageSpec{
		"s3": {
			Type: psmdbv1.BackupStorageS3,
			S3: psmdbv1.BackupStorageS3Spec{
				EndpointURL: "https://s3.amazonaws.com",
			},
		},
	}
}

func TestUpdateKnownFields(t *testing.T) {
	tests := []struct {
		name string
		a    map[string]any
		b    map[string]any
		want map[string]any
	}{
		{
			name: "updates value of existing field",
			a:    map[string]any{"compression": "gzip"},
			b:    map[string]any{"compression": "s2"},
			want: map[string]any{"compression": "s2"},
		},
		{
			name: "ignores field that does not exist in a",
			a:    map[string]any{"compression": "gzip"},
			b:    map[string]any{"compression": "s2", "newField": "value"},
			want: map[string]any{"compression": "s2"},
		},
		{
			name: "keeps field of a that is absent from b",
			a:    map[string]any{"compression": "gzip", "level": 6},
			b:    map[string]any{"compression": "s2"},
			want: map[string]any{"compression": "s2", "level": 6},
		},
		{
			name: "recurses into nested maps and ignores unknown nested fields",
			a: map[string]any{
				"storage": map[string]any{
					"type": "s3",
					"s3": map[string]any{
						"bucket": "old-bucket",
						"region": "us-east-1",
					},
				},
			},
			b: map[string]any{
				"storage": map[string]any{
					"type": "s3",
					"s3": map[string]any{
						"bucket":         "new-bucket",
						"region":         "us-east-1",
						"maxUploadParts": 10000,
					},
				},
			},
			want: map[string]any{
				"storage": map[string]any{
					"type": "s3",
					"s3": map[string]any{
						"bucket": "new-bucket",
						"region": "us-east-1",
					},
				},
			},
		},
		{
			name: "overrides when a value is a map but b value is scalar",
			a:    map[string]any{"restore": map[string]any{"numInsertionWorkers": 1}},
			b:    map[string]any{"restore": "scalar"},
			want: map[string]any{"restore": "scalar"},
		},
		{
			name: "overrides when a value is scalar but b value is a map",
			a:    map[string]any{"restore": "scalar"},
			b:    map[string]any{"restore": map[string]any{"numInsertionWorkers": 1}},
			want: map[string]any{"restore": map[string]any{"numInsertionWorkers": 1}},
		},
		{
			name: "empty b returns copy of a",
			a:    map[string]any{"compression": "gzip"},
			b:    map[string]any{},
			want: map[string]any{"compression": "gzip"},
		},
		{
			name: "empty a ignores everything in b",
			a:    map[string]any{},
			b:    map[string]any{"compression": "s2"},
			want: map[string]any{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := updateKnownFields(tt.a, tt.b)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestUpdateKnownFieldsDoesNotMutateInputs(t *testing.T) {
	a := map[string]any{
		"storage": map[string]any{
			"s3": map[string]any{"bucket": "old-bucket"},
		},
	}
	b := map[string]any{
		"storage": map[string]any{
			"s3": map[string]any{"bucket": "new-bucket", "region": "us-east-1"},
		},
	}

	_ = updateKnownFields(a, b)

	assert.Equal(t, "old-bucket", a["storage"].(map[string]any)["s3"].(map[string]any)["bucket"])
	assert.NotContains(t, a["storage"].(map[string]any)["s3"].(map[string]any), "region")
	assert.Equal(t, "new-bucket", b["storage"].(map[string]any)["s3"].(map[string]any)["bucket"])
}

func TestIsTransientNetworkError(t *testing.T) {
	tests := map[string]struct {
		err  error
		want bool
	}{
		"api server dial timeout": {
			err:  errors.New(`describe restore stderr:  stdout: : error sending request: Post "https://34.118.224.1:443/api/v1/namespaces/ns/pods/some-name-rs0-0/exec": dial tcp 34.118.224.1:443: connect: connection timed out`),
			want: true,
		},
		"connection reset": {
			err:  errors.New("read tcp 10.0.0.1:1234->10.0.0.2:443: read: connection reset by peer"),
			want: true,
		},
		"connection refused": {
			err:  errors.New("dial tcp 10.0.0.2:443: connect: connection refused"),
			want: true,
		},
		"i/o timeout": {
			err:  errors.New("dial tcp 10.0.0.2:443: i/o timeout"),
			want: true,
		},
		"tls handshake timeout": {
			err:  errors.New("net/http: TLS handshake timeout"),
			want: true,
		},
		"kubelet unreachable": {
			err:  errors.New("error dialing backend: dial tcp 10.0.0.3:10250: connect: no route to host"),
			want: true,
		},
		"exec upgrade failure": {
			err:  errors.New("unable to upgrade connection: container not found"),
			want: true,
		},
		"pbm error": {
			err:  errors.New("describe restore stderr: Error: get restore meta: not found stdout: "),
			want: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, isTransientNetworkError(tt.err))
		})
	}
}

func TestCheckStatefulSetForPhysicalRestore(t *testing.T) {
	const ns = "some-ns"

	cluster := &psmdbv1.PerconaServerMongoDB{
		Name:      "some-name",
		Namespace: ns,
	}
	rs := &psmdbv1.ReplsetSpec{Name: "rs0"}
	ls := naming.RSLabels(cluster, rs)
	ls[naming.LabelKubernetesComponent] = naming.ComponentMongod

	newSts := func() *appsv1.StatefulSet {
		return &appsv1.StatefulSet{
			Name:       naming.MongodStatefulSetName(cluster, rs),
			Namespace:  ns,
			Generation: 2,
			Annotations: map[string]string{
				psmdbv1.AnnotationRestoreInProgress: "true",
			},
			Spec: appsv1.StatefulSetSpec{
				Replicas: new(int32(3)),
				Selector: &metav1.LabelSelector{MatchLabels: ls},
			},
			Status: appsv1.StatefulSetStatus{
				ObservedGeneration: 2,
				Replicas:           3,
				ReadyReplicas:      3,
				UpdateRevision:     "rev-2",
			},
		}
	}
	newPod := func(i int) *corev1.Pod {
		labels := maps.Clone(ls)
		labels["controller-revision-hash"] = "rev-2"
		return &corev1.Pod{
			Name:      fmt.Sprintf("%s-%d", naming.MongodStatefulSetName(cluster, rs), i),
			Namespace: ns,
			Labels:    labels,
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: naming.ComponentMongod}},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{
					{Type: corev1.ContainersReady, Status: corev1.ConditionTrue},
				},
			},
		}
	}

	tests := map[string]struct {
		modify    func(sts *appsv1.StatefulSet, pods []*corev1.Pod) []*corev1.Pod
		wantReady bool
	}{
		"ready": {
			wantReady: true,
		},
		"restore annotation missing": {
			modify: func(sts *appsv1.StatefulSet, pods []*corev1.Pod) []*corev1.Pod {
				sts.Annotations = nil
				return pods
			},
		},
		"not all replicas ready": {
			modify: func(sts *appsv1.StatefulSet, pods []*corev1.Pod) []*corev1.Pod {
				sts.Status.ReadyReplicas = 2
				return pods
			},
		},
		"statefulset spec change not observed yet": {
			modify: func(sts *appsv1.StatefulSet, pods []*corev1.Pod) []*corev1.Pod {
				sts.Status.ObservedGeneration = 1
				return pods
			},
		},
		"pod not created yet": {
			modify: func(sts *appsv1.StatefulSet, pods []*corev1.Pod) []*corev1.Pod {
				return pods[:2]
			},
		},
		"pod has old revision": {
			modify: func(sts *appsv1.StatefulSet, pods []*corev1.Pod) []*corev1.Pod {
				pods[1].Labels["controller-revision-hash"] = "rev-1"
				return pods
			},
		},
		"pod is not ready": {
			modify: func(sts *appsv1.StatefulSet, pods []*corev1.Pod) []*corev1.Pod {
				pods[2].Status.Conditions = nil
				return pods
			},
		},
		"pod still has backup-agent container": {
			modify: func(sts *appsv1.StatefulSet, pods []*corev1.Pod) []*corev1.Pod {
				pods[0].Spec.Containers = append(pods[0].Spec.Containers, corev1.Container{Name: naming.ContainerBackupAgent})
				return pods
			},
		},
		"pods of other statefulsets are ignored": {
			modify: func(sts *appsv1.StatefulSet, pods []*corev1.Pod) []*corev1.Pod {
				other := newPod(0)
				other.Name = naming.NonVotingStatefulSetName(cluster, rs) + "-0"
				other.Labels[naming.LabelKubernetesComponent] = naming.ComponentNonVoting
				other.Labels["controller-revision-hash"] = "other-rev"
				other.Status.Conditions = nil
				return append(pods, other)
			},
			wantReady: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			sts := newSts()
			pods := []*corev1.Pod{newPod(0), newPod(1), newPod(2)}
			if tt.modify != nil {
				pods = tt.modify(sts, pods)
			}

			objs := []client.Object{cluster, sts}
			for _, pod := range pods {
				objs = append(objs, pod)
			}
			r := fakeReconciler(objs...)

			ready, err := r.checkStatefulSetForPhysicalRestore(t.Context(), cluster, rs, naming.ComponentMongod)
			require.NoError(t, err)
			assert.Equal(t, tt.wantReady, ready)
		})
	}
}
