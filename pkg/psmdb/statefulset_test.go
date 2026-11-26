package psmdb

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestCollectStorageCABundles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cr       *api.PerconaServerMongoDB
		expected []api.SecretKeySelector
	}{
		{
			name: "no storages",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Backup:    api.BackupSpec{},
				},
			},
			expected: nil,
		},
		{
			name: "storage without CA bundle",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Backup: api.BackupSpec{
						Storages: map[string]api.BackupStorageSpec{
							"minio": {
								Type: api.BackupStorageMinio,
								Minio: api.BackupStorageMinioSpec{
									Bucket: "backups",
								},
							},
						},
					},
				},
			},
			expected: nil,
		},
		{
			name: "single CA bundle",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Backup: api.BackupSpec{
						Storages: map[string]api.BackupStorageSpec{
							"minio": {
								Type: api.BackupStorageMinio,
								Minio: api.BackupStorageMinioSpec{
									CABundle: &corev1.SecretKeySelector{
										Name: "minio-ca",
										Key:  "ca.crt",
									},
								},
							},
						},
					},
				},
			},
			expected: []api.SecretKeySelector{
				{
					Name: "minio-ca",
					Key:  "ca.crt",
				},
			},
		},
		{
			name: "default key to ca.crt",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Backup: api.BackupSpec{
						Storages: map[string]api.BackupStorageSpec{
							"minio": {
								Type: api.BackupStorageMinio,
								Minio: api.BackupStorageMinioSpec{
									CABundle: &corev1.SecretKeySelector{
										Name: "minio-ca",
										// Key not specified
									},
								},
							},
						},
					},
				},
			},
			expected: []api.SecretKeySelector{
				{
					Name: "minio-ca",
					Key:  "ca.crt", // defaulted
				},
			},
		},
		{
			name: "deduplicate same CA",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Backup: api.BackupSpec{
						Storages: map[string]api.BackupStorageSpec{
							"minio1": {
								Type: api.BackupStorageMinio,
								Minio: api.BackupStorageMinioSpec{
									CABundle: &corev1.SecretKeySelector{
										Name: "shared-ca",
										Key:  "ca.crt",
									},
								},
							},
							"minio2": {
								Type: api.BackupStorageMinio,
								Minio: api.BackupStorageMinioSpec{
									CABundle: &corev1.SecretKeySelector{
										Name: "shared-ca",
										Key:  "ca.crt",
									},
								},
							},
						},
					},
				},
			},
			expected: []api.SecretKeySelector{
				{
					Name: "shared-ca",
					Key:  "ca.crt",
				},
			},
		},
		{
			name: "different keys from same secret",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Backup: api.BackupSpec{
						Storages: map[string]api.BackupStorageSpec{
							"minio1": {
								Type: api.BackupStorageMinio,
								Minio: api.BackupStorageMinioSpec{
									CABundle: &corev1.SecretKeySelector{
										Name: "multi-ca",
										Key:  "ca1.crt",
									},
								},
							},
							"minio2": {
								Type: api.BackupStorageMinio,
								Minio: api.BackupStorageMinioSpec{
									CABundle: &corev1.SecretKeySelector{
										Name: "multi-ca",
										Key:  "ca2.crt",
									},
								},
							},
						},
					},
				},
			},
			expected: []api.SecretKeySelector{
				{
					Name: "multi-ca",
					Key:  "ca1.crt",
				},
				{
					Name: "multi-ca",
					Key:  "ca2.crt",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CollectStorageCABundles(tt.cr)

			if tt.expected == nil {
				assert.Nil(t, result)
				return
			}

			require.Len(t, result, len(tt.expected))

			for i, expected := range tt.expected {
				assert.Equal(t, expected.Name, result[i].Name)
				assert.Equal(t, expected.Key, result[i].Key)
			}
		})
	}
}

func TestGetCAVolumeMounts(t *testing.T) {
	t.Parallel()

	mounts := GetCAVolumeMounts()

	tests := []struct {
		name     string
		index    int
		wantName string
		wantPath string
		wantRO   bool
	}{
		{
			name:     "input mount",
			index:    0,
			wantName: naming.BackupStorageCAInputVolumeName,
			wantPath: "/etc/s3/certs-in",
			wantRO:   true,
		},
		{
			name:     "output mount",
			index:    1,
			wantName: naming.BackupStorageCAFileVolumeName,
			wantPath: "/etc/s3/certs",
			wantRO:   false,
		},
	}

	require.Len(t, mounts, len(tests))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mount := mounts[tt.index]
			assert.Equal(t, tt.wantName, mount.Name)
			assert.Equal(t, tt.wantPath, mount.MountPath)
			assert.Equal(t, tt.wantRO, mount.ReadOnly)
		})
	}
}

func TestGetCAVolumes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cas  []api.SecretKeySelector
	}{
		{
			name: "single CA",
			cas: []api.SecretKeySelector{
				{
					Name: "minio-ca",
					Key:  "ca.crt",
				},
			},
		},
		{
			name: "multiple CAs",
			cas: []api.SecretKeySelector{
				{Name: "minio-ca", Key: "ca.crt"},
				{Name: "s3-ca", Key: "root.crt"},
				{Name: "custom-ca", Key: "my-ca.crt"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			volumes := getCAVolumes(tt.cas)

			require.Len(t, volumes, 2)

			inputVol := volumes[0]
			assert.Equal(t, naming.BackupStorageCAInputVolumeName, inputVol.Name)
			require.NotNil(t, inputVol.Projected)
			require.Len(t, inputVol.Projected.Sources, len(tt.cas))

			for i, ca := range tt.cas {
				secretProj := inputVol.Projected.Sources[i].Secret
				require.NotNil(t, secretProj, "source %d", i)
				assert.Equal(t, ca.Name, secretProj.Name, "source %d name", i)
				require.Len(t, secretProj.Items, 1, "source %d items", i)
				assert.Equal(t, ca.Key, secretProj.Items[0].Key, "source %d key", i)
				assert.Equal(t, fmt.Sprintf("ca-%d.crt", i), secretProj.Items[0].Path, "source %d path", i)
			}

			outputVol := volumes[1]
			assert.Equal(t, naming.BackupStorageCAFileVolumeName, outputVol.Name)
			assert.NotNil(t, outputVol.EmptyDir)
		})
	}
}

func TestShouldSetAWSSDKChecksumEnvVars(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cr   *api.PerconaServerMongoDB
		want bool
	}{
		{
			name: "s3-compatible oss with pbm 2.11",
			cr: backupChecksumTestCR("2.11.0", api.BackupStorageSpec{
				Type: api.BackupStorageS3,
				S3: api.BackupStorageS3Spec{
					EndpointURL: "https://s3.oss-eu-central-1.aliyuncs.com",
				},
			}),
			want: true,
		},
		{
			name: "s3-compatible oss with pbm 2.10",
			cr: backupChecksumTestCR("2.10.0", api.BackupStorageSpec{
				Type: api.BackupStorageS3,
				S3: api.BackupStorageS3Spec{
					EndpointURL: "https://s3.oss-eu-central-1.aliyuncs.com",
				},
			}),
			want: true,
		},
		{
			name: "s3-compatible oss with pbm 2.12",
			cr: backupChecksumTestCR("2.12.0", api.BackupStorageSpec{
				Type: api.BackupStorageS3,
				S3: api.BackupStorageS3Spec{
					EndpointURL: "https://s3.oss-eu-central-1.aliyuncs.com",
				},
			}),
			want: false,
		},
		{
			name: "native oss with pbm 2.11",
			cr: backupChecksumTestCR("2.11.0", api.BackupStorageSpec{
				Type: api.BackupStorageOSS,
				OSS: api.BackupStorageOSSSpec{
					EndpointURL: "https://oss-eu-central-1.aliyuncs.com",
				},
			}),
			want: false,
		},
		{
			name: "regular s3 with pbm 2.11",
			cr: backupChecksumTestCR("2.11.0", api.BackupStorageSpec{
				Type: api.BackupStorageS3,
				S3: api.BackupStorageS3Spec{
					EndpointURL: "https://s3.amazonaws.com",
				},
			}),
			want: false,
		},
		{
			name: "missing pbm version",
			cr: backupChecksumTestCR("", api.BackupStorageSpec{
				Type: api.BackupStorageS3,
				S3: api.BackupStorageS3Spec{
					EndpointURL: "https://s3.oss-eu-central-1.aliyuncs.com",
				},
			}),
			want: false,
		},
		{
			name: "multiple storages include s3-compatible oss",
			cr: backupChecksumTestCR("2.11.0",
				api.BackupStorageSpec{
					Type: api.BackupStorageS3,
					S3: api.BackupStorageS3Spec{
						EndpointURL: "https://s3.amazonaws.com",
					},
				},
				api.BackupStorageSpec{
					Type: api.BackupStorageS3,
					S3: api.BackupStorageS3Spec{
						EndpointURL: "https://s3.oss-eu-central-1.aliyuncs.com",
					},
				},
			),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ShouldSetAWSSDKChecksumEnvVars(tt.cr))
		})
	}
}

func TestBackupAgentContainerAWSSDKChecksumEnvVars(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cr   *api.PerconaServerMongoDB
		want bool
	}{
		{
			name: "sets env vars for s3-compatible oss with pbm 2.11",
			cr: backupChecksumTestCR("2.11.0", api.BackupStorageSpec{
				Type: api.BackupStorageS3,
				S3: api.BackupStorageS3Spec{
					EndpointURL: "https://s3.oss-eu-central-1.aliyuncs.com",
				},
			}),
			want: true,
		},
		{
			name: "does not set env vars for pbm 2.12",
			cr: backupChecksumTestCR("2.12.0", api.BackupStorageSpec{
				Type: api.BackupStorageS3,
				S3: api.BackupStorageS3Spec{
					EndpointURL: "https://s3.oss-eu-central-1.aliyuncs.com",
				},
			}),
			want: false,
		},
		{
			name: "does not set env vars before operator 1.23",
			cr: func() *api.PerconaServerMongoDB {
				cr := backupChecksumTestCR("2.11.0", api.BackupStorageSpec{
					Type: api.BackupStorageS3,
					S3: api.BackupStorageS3Spec{
						EndpointURL: "https://s3.oss-eu-central-1.aliyuncs.com",
					},
				})
				cr.Spec.CRVersion = "1.22.0"
				return cr
			}(),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := backupAgentContainer(context.Background(), tt.cr, "rs0", 27017, false, &corev1.Secret{})

			hasRequestChecksumEnv := slices.ContainsFunc(c.Env, func(env corev1.EnvVar) bool {
				return env.Name == "AWS_REQUEST_CHECKSUM_CALCULATION" && env.Value == "when_required"
			})
			hasResponseChecksumEnv := slices.ContainsFunc(c.Env, func(env corev1.EnvVar) bool {
				return env.Name == "AWS_RESPONSE_CHECKSUM_VALIDATION" && env.Value == "when_required"
			})

			assert.Equal(t, tt.want, hasRequestChecksumEnv)
			assert.Equal(t, tt.want, hasResponseChecksumEnv)
		})
	}
}

func backupChecksumTestCR(pbmVersion string, storages ...api.BackupStorageSpec) *api.PerconaServerMongoDB {
	cr := &api.PerconaServerMongoDB{
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: version.Version(),
			Secrets:   &api.SecretsSpec{Users: "some-users"},
			Backup: api.BackupSpec{
				Enabled:  true,
				Image:    "backup-image",
				Storages: make(map[string]api.BackupStorageSpec, len(storages)),
			},
		},
		Status: api.PerconaServerMongoDBStatus{
			BackupVersion: pbmVersion,
		},
	}

	for i, storage := range storages {
		cr.Spec.Backup.Storages[fmt.Sprintf("storage-%d", i)] = storage
	}

	return cr
}

func TestBackupAgentContainerProbes(t *testing.T) {
	t.Parallel()

	liveness := &corev1.Probe{
		InitialDelaySeconds: 15,
		PeriodSeconds:       7,
		Exec:                &corev1.ExecAction{Command: []string{"/bin/true"}},
	}
	readiness := &corev1.Probe{
		InitialDelaySeconds: 5,
		PeriodSeconds:       3,
		TCPSocket:           &corev1.TCPSocketAction{Port: intstr.FromInt(27017)},
	}

	newCR := func() *api.PerconaServerMongoDB {
		return &api.PerconaServerMongoDB{
			Spec: api.PerconaServerMongoDBSpec{
				CRVersion: version.Version(),
				Secrets:   &api.SecretsSpec{Users: "some-users"},
				Backup: api.BackupSpec{
					Enabled: true,
					Image:   "backup-image",
				},
			},
		}
	}

	tests := []struct {
		name          string
		setup         func(cr *api.PerconaServerMongoDB)
		wantLiveness  *corev1.Probe
		wantReadiness *corev1.Probe
	}{
		{
			name:          "no probes by default",
			setup:         func(*api.PerconaServerMongoDB) {},
			wantLiveness:  nil,
			wantReadiness: nil,
		},
		{
			name: "custom probes honored",
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Backup.LivenessProbe = liveness
				cr.Spec.Backup.ReadinessProbe = readiness
			},
			wantLiveness:  liveness,
			wantReadiness: readiness,
		},
		{
			name: "custom probes ignored on <1.23.0",
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.CRVersion = "1.22.0"
				cr.Spec.Backup.LivenessProbe = liveness
				cr.Spec.Backup.ReadinessProbe = readiness
			},
			wantLiveness:  nil,
			wantReadiness: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := newCR()
			tt.setup(cr)

			c := backupAgentContainer(context.Background(), cr, "rs0", 27017, false, &corev1.Secret{})

			assert.Equal(t, tt.wantLiveness, c.LivenessProbe)
			assert.Equal(t, tt.wantReadiness, c.ReadinessProbe)
		})
	}
}

func rorfsSecurityContext(enabled bool) *corev1.SecurityContext {
	return &corev1.SecurityContext{ReadOnlyRootFilesystem: &enabled}
}

func TestReadOnlyRootFilesystemEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sc   *corev1.SecurityContext
		want bool
	}{
		{name: "nil context", sc: nil, want: false},
		{name: "nil flag", sc: &corev1.SecurityContext{}, want: false},
		{name: "explicitly false", sc: rorfsSecurityContext(false), want: false},
		{name: "explicitly true", sc: rorfsSecurityContext(true), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, readOnlyRootFilesystemEnabled(tt.sc))
		})
	}
}

func TestNeedsTmpVolume(t *testing.T) {
	t.Parallel()

	newCR := func(setup func(cr *api.PerconaServerMongoDB)) *api.PerconaServerMongoDB {
		cr := &api.PerconaServerMongoDB{
			Spec: api.PerconaServerMongoDBSpec{
				CRVersion: version.Version(),
			},
		}
		setup(cr)
		return cr
	}

	tests := []struct {
		name     string
		cr       *api.PerconaServerMongoDB
		mongodSC *corev1.SecurityContext
		want     bool
	}{
		{
			name:     "no readOnlyRootFilesystem anywhere",
			cr:       newCR(func(*api.PerconaServerMongoDB) {}),
			mongodSC: nil,
			want:     false,
		},
		{
			name:     "mongod readOnlyRootFilesystem",
			cr:       newCR(func(*api.PerconaServerMongoDB) {}),
			mongodSC: rorfsSecurityContext(true),
			want:     true,
		},
		{
			name: "backup agent readOnlyRootFilesystem with backup enabled",
			cr: newCR(func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Backup.Enabled = true
				cr.Spec.Backup.ContainerSecurityContext = rorfsSecurityContext(true)
			}),
			mongodSC: nil,
			want:     true,
		},
		{
			name: "backup agent readOnlyRootFilesystem but backup disabled",
			cr: newCR(func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Backup.Enabled = false
				cr.Spec.Backup.ContainerSecurityContext = rorfsSecurityContext(true)
			}),
			mongodSC: nil,
			want:     false,
		},
		{
			name: "mongod readOnlyRootFilesystem ignored on <1.23.0",
			cr: newCR(func(cr *api.PerconaServerMongoDB) {
				cr.Spec.CRVersion = "1.22.0"
			}),
			mongodSC: rorfsSecurityContext(true),
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, needsTmpVolume(tt.cr, tt.mongodSC))
		})
	}
}

func TestContainerReadOnlyRootFilesystemTmpMount(t *testing.T) {
	t.Parallel()

	allowInvalidCerts := true
	tmpMount := corev1.VolumeMount{Name: tmpVolumeName, MountPath: tmpMountPath}

	newCR := func(crVersion string) *api.PerconaServerMongoDB {
		return &api.PerconaServerMongoDB{
			Spec: api.PerconaServerMongoDBSpec{
				CRVersion: crVersion,
				Secrets:   &api.SecretsSpec{Users: "some-users"},
				TLS: &api.TLSSpec{
					Mode:                     api.TLSModePrefer,
					AllowInvalidCertificates: &allowInvalidCerts,
				},
			},
		}
	}

	newParams := func(sc *corev1.SecurityContext) containerFnParams {
		return containerFnParams{
			replset: &api.ReplsetSpec{
				Name:    "rs0",
				Storage: &api.MongodSpecStorage{},
			},
			name:                     naming.ContainerMongod,
			livenessProbe:            &api.LivenessProbeExtended{},
			containerSecurityContext: sc,
		}
	}

	tests := []struct {
		name      string
		crVersion string
		sc        *corev1.SecurityContext
		wantMount bool
	}{
		{name: "no security context", crVersion: version.Version(), sc: nil, wantMount: false},
		{name: "readOnlyRootFilesystem false", crVersion: version.Version(), sc: rorfsSecurityContext(false), wantMount: false},
		{name: "readOnlyRootFilesystem true", crVersion: version.Version(), sc: rorfsSecurityContext(true), wantMount: true},
		{name: "readOnlyRootFilesystem true ignored on <1.23.0", crVersion: "1.22.0", sc: rorfsSecurityContext(true), wantMount: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := container(context.Background(), newCR(tt.crVersion), newParams(tt.sc))
			require.NoError(t, err)

			if tt.wantMount {
				assert.Contains(t, c.VolumeMounts, tmpMount)
			} else {
				assert.NotContains(t, c.VolumeMounts, tmpMount)
			}
		})
	}
}

func TestBackupAgentContainerReadOnlyRootFilesystemTmpMount(t *testing.T) {
	t.Parallel()

	tmpMount := corev1.VolumeMount{Name: tmpVolumeName, MountPath: tmpMountPath}

	newCR := func() *api.PerconaServerMongoDB {
		return &api.PerconaServerMongoDB{
			Spec: api.PerconaServerMongoDBSpec{
				CRVersion: version.Version(),
				Secrets:   &api.SecretsSpec{Users: "some-users"},
				Backup: api.BackupSpec{
					Enabled: true,
					Image:   "backup-image",
				},
			},
		}
	}

	tests := []struct {
		name      string
		setup     func(cr *api.PerconaServerMongoDB)
		wantMount bool
	}{
		{
			name:      "no security context",
			setup:     func(*api.PerconaServerMongoDB) {},
			wantMount: false,
		},
		{
			name: "readOnlyRootFilesystem false",
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Backup.ContainerSecurityContext = rorfsSecurityContext(false)
			},
			wantMount: false,
		},
		{
			name: "readOnlyRootFilesystem true",
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Backup.ContainerSecurityContext = rorfsSecurityContext(true)
			},
			wantMount: true,
		},
		{
			name: "readOnlyRootFilesystem true ignored on <1.23.0",
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.CRVersion = "1.22.0"
				cr.Spec.Backup.ContainerSecurityContext = rorfsSecurityContext(true)
			},
			wantMount: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := newCR()
			tt.setup(cr)

			c := backupAgentContainer(context.Background(), cr, "rs0", 27017, false, &corev1.Secret{})

			if tt.wantMount {
				assert.Contains(t, c.VolumeMounts, tmpMount)
			} else {
				assert.NotContains(t, c.VolumeMounts, tmpMount)
			}
		})
	}
}

func TestAddPMMTmpVolume(t *testing.T) {
	t.Parallel()

	tmpMount := corev1.VolumeMount{Name: tmpVolumeName, MountPath: tmpMountPath}
	tmpVolume := corev1.Volume{
		Name:     tmpVolumeName,
		EmptyDir: &corev1.EmptyDirVolumeSource{},
	}

	newCR := func(crVersion string, sc *corev1.SecurityContext) *api.PerconaServerMongoDB {
		return &api.PerconaServerMongoDB{
			Spec: api.PerconaServerMongoDBSpec{
				CRVersion: crVersion,
				PMM: api.PMMSpec{
					Enabled:                  true,
					ContainerSecurityContext: sc,
				},
			},
		}
	}

	tests := []struct {
		name string
		cr   *api.PerconaServerMongoDB
		pmmC *corev1.Container
		want bool
	}{
		{
			name: "readOnlyRootFilesystem true injects tmp",
			cr:   newCR(version.Version(), rorfsSecurityContext(true)),
			pmmC: &corev1.Container{Name: "pmm-client"},
			want: true,
		},
		{
			name: "readOnlyRootFilesystem false does nothing",
			cr:   newCR(version.Version(), rorfsSecurityContext(false)),
			pmmC: &corev1.Container{Name: "pmm-client"},
			want: false,
		},
		{
			name: "no security context does nothing",
			cr:   newCR(version.Version(), nil),
			pmmC: &corev1.Container{Name: "pmm-client"},
			want: false,
		},
		{
			name: "ignored before 1.24.0",
			cr:   newCR("1.23.0", rorfsSecurityContext(true)),
			pmmC: &corev1.Container{Name: "pmm-client"},
			want: false,
		},
		{
			name: "nil pmm container does nothing",
			cr:   newCR(version.Version(), rorfsSecurityContext(true)),
			pmmC: nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			volumes := AddPMMTmpVolume(tt.cr, tt.pmmC, nil)
			if tt.want {
				require.NotNil(t, tt.pmmC)
				assert.Contains(t, tt.pmmC.VolumeMounts, tmpMount)
				assert.Contains(t, volumes, tmpVolume)
			} else {
				if tt.pmmC != nil {
					assert.NotContains(t, tt.pmmC.VolumeMounts, tmpMount)
				}
				assert.NotContains(t, volumes, tmpVolume)
			}
		})
	}

	t.Run("does not duplicate an existing tmp volume", func(t *testing.T) {
		cr := newCR(version.Version(), rorfsSecurityContext(true))
		pmmC := &corev1.Container{Name: "pmm-client"}
		volumes := AddPMMTmpVolume(cr, pmmC, []corev1.Volume{tmpVolume})
		assert.Equal(t, 1, len(volumes))
	})
}

func TestBuildMongoDBURI(t *testing.T) {
	const baseURI = "mongodb://$(PBM_AGENT_MONGODB_USERNAME):$(PBM_AGENT_MONGODB_PASSWORD)@localhost:$(PBM_MONGODB_PORT)"
	const tlsOpts = "/?tls=true&tlsCertificateKeyFile=/tmp/pbm-tls.pem&tlsCAFile=/etc/mongodb-ssl/ca.crt"

	tests := map[string]struct {
		crVersion    string
		tlsEnabled   bool
		allowInvalid bool
		expectedURI  string
	}{
		"tls disabled": {
			tlsEnabled:  false,
			expectedURI: baseURI,
		},
		"tls enabled": {
			tlsEnabled:   true,
			allowInvalid: false,
			expectedURI:  baseURI + tlsOpts,
		},
		"allow invalid certificates": {
			tlsEnabled:   true,
			allowInvalid: true,
			expectedURI:  baseURI + tlsOpts + "&tlsInsecure=true",
		},
		"crVersion before dedicated pbm certificate": {
			crVersion:   "1.23.0",
			tlsEnabled:  true,
			expectedURI: baseURI + "/?tls=true&tlsCertificateKeyFile=/tmp/tls.pem&tlsCAFile=/etc/mongodb-ssl/ca.crt&tlsInsecure=true",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			crVersion := tt.crVersion
			if crVersion == "" {
				crVersion = version.Version()
			}
			cr := &api.PerconaServerMongoDB{
				Name:      "cluster",
				Namespace: "ns",
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: crVersion,
					Replsets:  []*api.ReplsetSpec{{Name: "rs0"}},
					TLS:       &api.TLSSpec{AllowInvalidCertificates: &tt.allowInvalid},
				},
			}

			sslSecret := new(corev1.Secret)
			sans := tls.GetCertificateSans(cr)
			caCert, tlsCert, tlsKey, err := tls.Issue(sans)
			require.NoError(t, err)
			sslSecret.Data = map[string][]byte{
				"ca.crt":  caCert,
				"tls.crt": tlsCert,
				"tls.key": tlsKey,
			}

			assert.Equal(t, tt.expectedURI, BuildMongoDBURI(t.Context(), cr, tt.tlsEnabled, sslSecret))
		})
	}
}

func logStorageSpec(size string) *api.StorageSpec {
	return &api.StorageSpec{
		PersistentVolumeClaim: &api.PVCSpec{
			Labels:      map[string]string{"custom-label": "custom"},
			Annotations: map[string]string{"custom-annotation": "custom"},
			PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse(size),
					},
				},
			},
		},
	}
}

func TestLogStorageVersionGate(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		crVersion string
		storage   *api.StorageSpec
		disabled  bool
		want      bool
	}{
		"not configured":    {crVersion: version.Version(), storage: nil, want: false},
		"configured":        {crVersion: version.Version(), storage: logStorageSpec("2Gi"), want: true},
		"collector off":     {crVersion: version.Version(), storage: logStorageSpec("2Gi"), disabled: true, want: false},
		"ignored on 1.23.1": {crVersion: "1.23.1", storage: logStorageSpec("2Gi"), want: false},
		"ignored on 1.21.0": {crVersion: "1.21.0", storage: logStorageSpec("2Gi"), want: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: tt.crVersion,
					LogCollector: &api.LogCollectorSpec{
						Enabled: !tt.disabled,
					},
				},
			}

			assert.Equal(t, tt.want, (&api.ReplsetSpec{LogStorage: tt.storage}).LogPVC(cr, naming.ComponentMongod) != nil)
		})
	}
}

func TestExtraPVC(t *testing.T) {
	t.Parallel()

	cr := &api.PerconaServerMongoDB{
		Name:      "test-cr",
		Namespace: "test-ns",
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: version.Version(),
			LogCollector: &api.LogCollectorSpec{
				Enabled: true,
			},
		},
	}

	pvc := extraPVC(cr, config.MongodLogVolClaimName, (&api.ReplsetSpec{LogStorage: logStorageSpec("2Gi")}).LogPVC(cr, naming.ComponentMongod))

	assert.Equal(t, config.MongodLogVolClaimName, pvc.Name)
	assert.Equal(t, "test-ns", pvc.Namespace)
	assert.Empty(t, pvc.Labels, "labels go to the PVCs through fixVolumeLabels, a template change would be rejected")
	assert.Empty(t, pvc.Annotations)
	assert.Equal(t, resource.MustParse("2Gi"), pvc.Spec.Resources.Requests[corev1.ResourceStorage])
}

func TestContainerLogVolumeMount(t *testing.T) {
	t.Parallel()

	logMount := corev1.VolumeMount{
		Name:      config.MongodLogVolClaimName,
		MountPath: config.MongodContainerDataLogsDir,
	}
	dataMount := corev1.VolumeMount{
		Name:      config.MongodDataVolClaimName,
		MountPath: config.MongodContainerDataDir,
	}

	pvcLogVol := config.MongodPVCLogVolume()

	tests := map[string]struct {
		logVolume *config.LogVolume
		wantMount bool
	}{
		"logs stay on the data volume": {
			logVolume: nil,
			wantMount: false,
		},
		"logs get a dedicated volume": {
			logVolume: &pvcLogVol,
			wantMount: true,
		},
	}

	allowInvalidCerts := true
	cr := &api.PerconaServerMongoDB{
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: version.Version(),
			Secrets:   &api.SecretsSpec{Users: "some-users"},
			TLS: &api.TLSSpec{
				Mode:                     api.TLSModePrefer,
				AllowInvalidCertificates: &allowInvalidCerts,
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c, err := container(t.Context(), cr, containerFnParams{
				replset: &api.ReplsetSpec{
					Name:    "rs0",
					Storage: &api.MongodSpecStorage{},
				},
				name:          naming.ContainerMongod,
				livenessProbe: &api.LivenessProbeExtended{},
				logVolume:     tt.logVolume,
			})
			require.NoError(t, err)

			if tt.wantMount {
				assert.Contains(t, c.VolumeMounts, logMount)
			} else {
				assert.NotContains(t, c.VolumeMounts, logMount)
			}

			assert.Contains(t, c.VolumeMounts, dataMount)
		})
	}
}

func TestStatefulSpecLogStoragePerComponent(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		setStorage func(rs *api.ReplsetSpec)
		want       map[string]string
	}{
		"replset only": {
			setStorage: func(rs *api.ReplsetSpec) { rs.LogStorage = logStorageSpec("2Gi") },
			want:       map[string]string{naming.ComponentMongod: "2Gi"},
		},
		"hidden only": {
			setStorage: func(rs *api.ReplsetSpec) { rs.Hidden.LogStorage = logStorageSpec("1Gi") },
			want:       map[string]string{naming.ComponentHidden: "1Gi"},
		},
		"different size per component": {
			setStorage: func(rs *api.ReplsetSpec) {
				rs.LogStorage = logStorageSpec("2Gi")
				rs.NonVoting.LogStorage = logStorageSpec("3Gi")
				rs.Hidden.LogStorage = logStorageSpec("4Gi")
			},
			want: map[string]string{
				naming.ComponentMongod:    "2Gi",
				naming.ComponentNonVoting: "3Gi",
				naming.ComponentHidden:    "4Gi",
			},
		},
	}

	labels := map[string]func(*api.PerconaServerMongoDB, *api.ReplsetSpec) map[string]string{
		naming.ComponentMongod:    naming.MongodLabels,
		naming.ComponentNonVoting: naming.NonVotingLabels,
		naming.ComponentHidden:    naming.HiddenLabels,
		naming.ComponentArbiter:   naming.ArbiterLabels,
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cr, err := readDefaultCR("test-cr", "test-ns")
			require.NoError(t, err)
			cr.Spec.LogCollector.Enabled = true
			cr.Spec.Unsafe.ReplsetSize = true
			cr.Spec.Backup.Enabled = false

			rs := cr.Spec.Replsets[0]
			rs.NonVoting.Enabled = true
			rs.Hidden.Enabled = true
			rs.Arbiter.Enabled = true
			rs.Arbiter.Size = 1
			tt.setStorage(rs)
			require.NoError(t, cr.CheckNSetDefaults(t.Context(), version.PlatformKubernetes))

			for component, ls := range labels {
				spec, err := StatefulSpec(t.Context(), cr, rs, ls(cr, rs), cr.Spec.InitImage, StatefulConfigParams{}, StatefulSpecSecretParams{})
				require.NoError(t, err, component)

				idx := slices.IndexFunc(spec.VolumeClaimTemplates, func(pvc corev1.PersistentVolumeClaim) bool {
					return pvc.Name == config.MongodLogVolClaimName
				})

				want, ok := tt.want[component]
				if !ok {
					assert.Equal(t, -1, idx, "%s must not get a log claim", component)
					continue
				}
				require.NotEqual(t, -1, idx, "%s must get a log claim", component)
				got := spec.VolumeClaimTemplates[idx].Spec.Resources.Requests[corev1.ResourceStorage]
				assert.Equal(t, want, got.String(), component)
			}
		})
	}
}

func TestStatefulSpecMongod(t *testing.T) {
	t.Parallel()

	tlsFlags := []string{"--ssl", "--sslInsecure", "--sslCAFile", "/etc/mongodb-ssl/ca.crt", "--sslPEMKeyFile", "/tmp/tls.pem"}
	livenessCmd := func(delay string) []string {
		return slices.Concat([]string{"/opt/percona/mongodb-healthcheck", "k8s", "liveness"}, tlsFlags, []string{"--startupDelaySeconds", delay})
	}
	readinessCmd := slices.Concat([]string{"/opt/percona/mongodb-healthcheck", "k8s", "readiness", "--component", "mongod"}, tlsFlags)

	quantities := func(cpu, mem string) corev1.ResourceList {
		return corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(mem)}
	}
	vault := func(cr *api.PerconaServerMongoDB) {
		cr.Spec.Secrets.Vault = "vault-secret"
		for _, rs := range []*api.ReplsetSpec{cr.Spec.Replsets[0], cr.Spec.Sharding.ConfigsvrReplSet} {
			rs.Configuration = "security:\n  enableEncryption: true\n  vault:\n    serverName: vault-service\n"
		}
	}
	userSecurityContext := func(cr *api.PerconaServerMongoDB) {
		cr.Spec.InitContainerSecurityContext = &corev1.SecurityContext{Privileged: new(false), RunAsUser: new(int64(1001))}
		rs := cr.Spec.Replsets[0]
		rs.ContainerSecurityContext = &corev1.SecurityContext{Privileged: new(true)}
		rs.PodSecurityContext = &corev1.PodSecurityContext{FSGroup: new(int64(1001)), SupplementalGroups: []int64{1001, 1002, 1003}}
		rs.ServiceAccountName = "percona-server-mongodb-operator-workload"
	}
	checkUserSecurityContext := func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
		pod := spec.Template.Spec
		assert.Equal(t, &corev1.SecurityContext{Privileged: new(true)}, pod.Containers[0].SecurityContext)
		assert.Equal(t, &corev1.PodSecurityContext{FSGroup: new(int64(1001)), SupplementalGroups: []int64{1001, 1002, 1003}}, pod.SecurityContext)
		assert.Equal(t, &corev1.SecurityContext{Privileged: new(false), RunAsUser: new(int64(1001))}, pod.InitContainers[0].SecurityContext)
		assert.Equal(t, "percona-server-mongodb-operator-workload", pod.ServiceAccountName)
	}

	tests := map[string]struct {
		platform version.Platform
		rs       string
		labels   func(*api.PerconaServerMongoDB, *api.ReplsetSpec) map[string]string
		modify   func(cr *api.PerconaServerMongoDB)
		check    func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec)
	}{
		"requests and limits": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Resources = corev1.ResourceRequirements{Limits: quantities("500m", "500M"), Requests: quantities("100m", "100M")}
			},
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				want := corev1.ResourceRequirements{Limits: quantities("500m", "500M"), Requests: quantities("100m", "100M")}
				assert.Equal(t, want, spec.Template.Spec.Containers[0].Resources)
				assert.Equal(t, want, spec.Template.Spec.InitContainers[0].Resources)
				assert.Equal(t, []string{"--wiredTigerCacheSizeGB=0.25"}, cacheSizeArgs(spec))
			},
		},
		"limits only": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Resources = corev1.ResourceRequirements{Limits: quantities("600m", "4Gi")}
			},
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				want := corev1.ResourceRequirements{Limits: quantities("600m", "4Gi")}
				assert.Equal(t, want, spec.Template.Spec.Containers[0].Resources)
				assert.Equal(t, want, spec.Template.Spec.InitContainers[0].Resources)
				assert.Equal(t, []string{"--wiredTigerCacheSizeGB=1.50"}, cacheSizeArgs(spec))
			},
		},
		"requests only": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Resources = corev1.ResourceRequirements{Requests: quantities("600m", "1G")}
			},
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				want := corev1.ResourceRequirements{Requests: quantities("600m", "1G")}
				assert.Equal(t, want, spec.Template.Spec.Containers[0].Resources)
				assert.Equal(t, want, spec.Template.Spec.InitContainers[0].Resources)
				assert.Empty(t, cacheSizeArgs(spec))
			},
		},
		"no requests and no limits": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Resources = corev1.ResourceRequirements{}
			},
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				assert.Equal(t, corev1.ResourceRequirements{}, spec.Template.Spec.Containers[0].Resources)
				assert.Equal(t, corev1.ResourceRequirements{}, spec.Template.Spec.InitContainers[0].Resources)
				assert.Empty(t, cacheSizeArgs(spec))
			},
		},
		"nonvoting uses its own resources": {
			labels: naming.NonVotingLabels,
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].NonVoting.Enabled = true
				cr.Spec.Replsets[0].NonVoting.Resources = corev1.ResourceRequirements{Limits: quantities("300m", "4Gi")}
			},
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				assert.Equal(t, corev1.ResourceRequirements{Limits: quantities("300m", "4Gi")}, spec.Template.Spec.Containers[0].Resources)
				assert.Equal(t, []string{"--wiredTigerCacheSizeGB=1.50"}, cacheSizeArgs(spec))
			},
		},
		"hidden uses its own resources": {
			labels: naming.HiddenLabels,
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Hidden.Enabled = true
				cr.Spec.Replsets[0].Hidden.Resources = corev1.ResourceRequirements{Limits: quantities("300m", "4Gi")}
			},
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				assert.Equal(t, corev1.ResourceRequirements{Limits: quantities("300m", "4Gi")}, spec.Template.Spec.Containers[0].Resources)
			},
		},
		"arbiter uses its own resources": {
			labels: naming.ArbiterLabels,
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Arbiter.Enabled = true
				cr.Spec.Replsets[0].Arbiter.Size = 1
				cr.Spec.Replsets[0].Size = 4
				cr.Spec.Replsets[0].Arbiter.Resources = corev1.ResourceRequirements{Limits: quantities("300m", "300M")}
			},
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				assert.Equal(t, corev1.ResourceRequirements{Limits: quantities("300m", "300M")}, spec.Template.Spec.Containers[0].Resources)
			},
		},
		"default probes": {
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				c := spec.Template.Spec.Containers[0]
				assert.Equal(t, &corev1.Probe{
					ProbeHandler:        corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: livenessCmd("7200")}},
					InitialDelaySeconds: 60, TimeoutSeconds: 10, PeriodSeconds: 30, FailureThreshold: 4,
				}, c.LivenessProbe)
				assert.Equal(t, &corev1.Probe{
					ProbeHandler:        corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: readinessCmd}},
					InitialDelaySeconds: 10, TimeoutSeconds: 2, PeriodSeconds: 3, SuccessThreshold: 1, FailureThreshold: 8,
				}, c.ReadinessProbe)
			},
		},
		"user probes": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].LivenessProbe = &api.LivenessProbeExtended{
					Probe:               corev1.Probe{InitialDelaySeconds: 61, TimeoutSeconds: 6, PeriodSeconds: 31, SuccessThreshold: 1, FailureThreshold: 5},
					StartupDelaySeconds: 7201, //nolint:staticcheck // still rendered as --startupDelaySeconds
				}
				cr.Spec.Replsets[0].ReadinessProbe = &corev1.Probe{
					ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(27017)}},
					InitialDelaySeconds: 11, TimeoutSeconds: 3, PeriodSeconds: 4, SuccessThreshold: 2, FailureThreshold: 9,
				}
			},
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				c := spec.Template.Spec.Containers[0]
				assert.Equal(t, &corev1.Probe{
					ProbeHandler:        corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: livenessCmd("7201")}},
					InitialDelaySeconds: 61, TimeoutSeconds: 6, PeriodSeconds: 31, SuccessThreshold: 1, FailureThreshold: 5,
				}, c.LivenessProbe)
				assert.Equal(t, &corev1.Probe{
					ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(27017)}},
					InitialDelaySeconds: 11, TimeoutSeconds: 3, PeriodSeconds: 4, SuccessThreshold: 2, FailureThreshold: 9,
				}, c.ReadinessProbe)
			},
		},
		"user probes keep unset defaults": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].LivenessProbe = &api.LivenessProbeExtended{Probe: corev1.Probe{FailureThreshold: 3}}
				cr.Spec.Replsets[0].ReadinessProbe = &corev1.Probe{FailureThreshold: 5}
			},
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				c := spec.Template.Spec.Containers[0]
				assert.Equal(t, &corev1.Probe{
					ProbeHandler:        corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: livenessCmd("7200")}},
					InitialDelaySeconds: 60, TimeoutSeconds: 10, PeriodSeconds: 30, FailureThreshold: 3,
				}, c.LivenessProbe)
				assert.Equal(t, &corev1.Probe{
					ProbeHandler:        corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: readinessCmd}},
					InitialDelaySeconds: 10, TimeoutSeconds: 2, PeriodSeconds: 3, SuccessThreshold: 1, FailureThreshold: 5,
				}, c.ReadinessProbe)
			},
		},
		"nonvoting user liveness probe": {
			labels: naming.NonVotingLabels,
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].NonVoting.Enabled = true
				cr.Spec.Replsets[0].NonVoting.LivenessProbe = &api.LivenessProbeExtended{
					Probe:               corev1.Probe{InitialDelaySeconds: 70, PeriodSeconds: 31, FailureThreshold: 3},
					StartupDelaySeconds: 7201, //nolint:staticcheck // still rendered as --startupDelaySeconds
				}
			},
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				assert.Equal(t, &corev1.Probe{
					ProbeHandler:        corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: livenessCmd("7201")}},
					InitialDelaySeconds: 70, TimeoutSeconds: 10, PeriodSeconds: 31, FailureThreshold: 3,
				}, spec.Template.Spec.Containers[0].LivenessProbe)
			},
		},
		"emptyDir data volume": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].VolumeSpec = &api.VolumeSpec{EmptyDir: &corev1.EmptyDirVolumeSource{}}
			},
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				v := findVolume(t, spec, config.MongodDataVolClaimName)
				assert.Equal(t, corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}, v.VolumeSource)
				assert.False(t, hasClaimTemplate(spec, config.MongodDataVolClaimName))
			},
		},
		"hostPath data volume": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].VolumeSpec = &api.VolumeSpec{HostPath: &corev1.HostPathVolumeSource{Path: "/run/data-dir", Type: new(corev1.HostPathDirectory)}}
			},
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				v := findVolume(t, spec, config.MongodDataVolClaimName)
				assert.Equal(t, corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/run/data-dir", Type: new(corev1.HostPathDirectory)}}, v.VolumeSource)
				assert.False(t, hasClaimTemplate(spec, config.MongodDataVolClaimName))
			},
		},
		"vault encryption": {
			modify: vault,
			check:  checkVaultEncryption,
		},
		"vault encryption on cfg": {
			rs:     api.ConfigReplSetName,
			modify: vault,
			check:  checkVaultEncryption,
		},
		"encryption disabled and quiet off": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Configuration = "systemLog:\n  quiet: false\nsecurity:\n  enableEncryption: false\n"
			},
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				c := spec.Template.Spec.Containers[0]
				assert.NotContains(t, c.Args, "--enableEncryption")
				assert.NotContains(t, c.Args, "--quiet")
				assert.False(t, slices.ContainsFunc(c.VolumeMounts, func(m corev1.VolumeMount) bool { return m.MountPath == api.MongodRESTencryptDir }))
				assert.False(t, slices.ContainsFunc(spec.Template.Spec.Volumes, func(v corev1.Volume) bool { return v.Name == cr.Spec.Secrets.EncryptionKey }))
			},
		},
		"preferTLS": {
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				c := spec.Template.Spec.Containers[0]
				assert.Subset(t, c.Args, []string{"--clusterAuthMode=x509", "--tlsMode=preferTLS"})
				assert.NotContains(t, c.Args, "--clusterAuthMode=keyFile")
				assert.False(t, slices.ContainsFunc(spec.Template.Spec.Volumes, func(v corev1.Volume) bool { return v.Name == cr.Spec.Secrets.GetInternalKey(cr) }))
				assert.False(t, *findVolume(t, spec, "ssl").Secret.Optional)
			},
		},
		"allowTLS": {
			modify: func(cr *api.PerconaServerMongoDB) { cr.Spec.TLS = &api.TLSSpec{Mode: api.TLSModeAllow} },
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				c := spec.Template.Spec.Containers[0]
				assert.Subset(t, c.Args, []string{"--clusterAuthMode=keyFile", "--keyFile=/etc/mongodb-secrets/mongodb-key", "--tlsMode=allowTLS"})
				assert.NotContains(t, c.Args, "--clusterAuthMode=x509")
			},
		},
		"TLS disabled": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Unsafe.TLS = true
				cr.Spec.TLS = &api.TLSSpec{Mode: api.TLSModeDisabled}
			},
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				c := spec.Template.Spec.Containers[0]
				assert.Subset(t, c.Args, []string{"--clusterAuthMode=keyFile", "--keyFile=/etc/mongodb-secrets/mongodb-key", "--tlsMode=disabled"})
				assert.NotContains(t, c.Args, "--clusterAuthMode=x509")
				for _, p := range []*corev1.Probe{c.LivenessProbe, c.ReadinessProbe, c.StartupProbe} {
					assert.NotContains(t, p.Exec.Command, "--ssl")
				}
				keyfile := findVolume(t, spec, cr.Spec.Secrets.GetInternalKey(cr))
				assert.Equal(t, cr.Spec.Secrets.GetInternalKey(cr), keyfile.Secret.SecretName)
				assert.Contains(t, c.VolumeMounts, corev1.VolumeMount{Name: keyfile.Name, MountPath: config.MongodSecretsDir, ReadOnly: true})
				assert.True(t, *findVolume(t, spec, "ssl").Secret.Optional)
			},
		},
		"custom secret names": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Secrets.SSL = "custom-ssl"
				cr.Spec.Secrets.SSLInternal = "custom-ssl-internal"
				cr.Spec.Secrets.EncryptionKey = "custom-encryption-key"
				cr.Spec.Secrets.InternalKey = "custom-keyfile"
			},
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				for volume, secret := range map[string]string{
					"ssl":                   "custom-ssl",
					"ssl-internal":          "custom-ssl-internal",
					"custom-encryption-key": "custom-encryption-key",
					"custom-keyfile":        "custom-keyfile",
				} {
					assert.Equal(t, secret, findVolume(t, spec, volume).Secret.SecretName, volume)
				}
			},
		},
		"sharding disabled": {
			modify: func(cr *api.PerconaServerMongoDB) { cr.Spec.Sharding.Enabled = false },
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				assert.NotContains(t, spec.Template.Spec.Containers[0].Args, "--shardsvr")
				assert.NotContains(t, spec.Template.Spec.Containers[0].Args, "--configsvr")
			},
		},
		"kubernetes security context defaults": {
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				assert.Equal(t, &corev1.SecurityContext{RunAsNonRoot: new(true), RunAsUser: new(int64(1001))}, spec.Template.Spec.Containers[0].SecurityContext)
				assert.Equal(t, &corev1.PodSecurityContext{FSGroup: new(int64(1001))}, spec.Template.Spec.SecurityContext)
			},
		},
		"openshift security context defaults": {
			platform: version.PlatformOpenshift,
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				assert.Equal(t, &corev1.SecurityContext{RunAsNonRoot: new(true)}, spec.Template.Spec.Containers[0].SecurityContext)
				assert.Equal(t, &corev1.PodSecurityContext{}, spec.Template.Spec.SecurityContext)
			},
		},
		"security context from the CR": {
			modify: userSecurityContext,
			check:  checkUserSecurityContext,
		},
		"security context from the CR on openshift": {
			platform: version.PlatformOpenshift,
			modify:   userSecurityContext,
			check:    checkUserSecurityContext,
		},
		"sidecars and pod settings": {
			modify: func(cr *api.PerconaServerMongoDB) {
				rs := cr.Spec.Replsets[0]
				rs.Sidecars = []corev1.Container{{Name: "my-sidecar-1", Image: "busybox"}, {Name: naming.ContainerMongod, Image: "busybox"}}
				rs.RuntimeClassName = new("container-rc")
				rs.TerminationGracePeriodSeconds = new(int64(30))
				rs.Annotations = map[string]string{"openshift.io/scc": "privileged"}
			},
			check: func(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
				pod := spec.Template.Spec
				assert.Equal(t, naming.ContainerMongod, pod.Containers[0].Name)
				assert.Equal(t, corev1.Container{Name: "my-sidecar-1", Image: "busybox"}, pod.Containers[1])
				mongods := slices.DeleteFunc(slices.Clone(pod.Containers), func(c corev1.Container) bool { return c.Name != naming.ContainerMongod })
				assert.Len(t, mongods, 1, "the sidecar named like mongod must be skipped")
				assert.Equal(t, new("container-rc"), pod.RuntimeClassName)
				assert.Equal(t, new(int64(30)), pod.TerminationGracePeriodSeconds)
				assert.Equal(t, "privileged", spec.Template.Annotations["openshift.io/scc"])
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cr, err := readDefaultCR("test-cr", "test-ns")
			require.NoError(t, err)
			cr.Spec.Backup.Enabled = false
			if tt.modify != nil {
				tt.modify(cr)
			}
			require.NoError(t, cr.CheckNSetDefaults(t.Context(), cmp.Or(tt.platform, version.PlatformKubernetes)))

			rs := cr.Spec.Replset(cmp.Or(tt.rs, "rs0"))
			labels := naming.MongodLabels
			if tt.labels != nil {
				labels = tt.labels
			}
			secrets := StatefulSpecSecretParams{SSLSecret: new(corev1.Secret), UsersSecret: new(corev1.Secret)}
			spec, err := StatefulSpec(t.Context(), cr, rs, labels(cr, rs), cr.Spec.InitImage, StatefulConfigParams{}, secrets)
			require.NoError(t, err)

			tt.check(t, cr, spec)
		})
	}
}

func checkVaultEncryption(t *testing.T, cr *api.PerconaServerMongoDB, spec appsv1.StatefulSetSpec) {
	c := spec.Template.Spec.Containers[0]
	assert.NotContains(t, c.Args, "--enableEncryption")
	assert.False(t, slices.ContainsFunc(c.Args, func(a string) bool { return strings.HasPrefix(a, "--encryptionKeyFile") }))
	assert.Contains(t, c.VolumeMounts, corev1.VolumeMount{Name: "vault-secret", MountPath: config.VaultDir, ReadOnly: true})
	assert.Equal(t, "vault-secret", findVolume(t, spec, "vault-secret").Secret.SecretName)
	assert.False(t, slices.ContainsFunc(spec.Template.Spec.Volumes, func(v corev1.Volume) bool { return v.Name == cr.Spec.Secrets.EncryptionKey }))
}

func cacheSizeArgs(spec appsv1.StatefulSetSpec) []string {
	var args []string
	for _, a := range spec.Template.Spec.Containers[0].Args {
		if strings.HasPrefix(a, "--wiredTigerCacheSizeGB") {
			args = append(args, a)
		}
	}
	return args
}

func findVolume(t *testing.T, spec appsv1.StatefulSetSpec, name string) corev1.Volume {
	t.Helper()
	i := slices.IndexFunc(spec.Template.Spec.Volumes, func(v corev1.Volume) bool { return v.Name == name })
	require.NotEqual(t, -1, i, "volume %s not found", name)
	return spec.Template.Spec.Volumes[i]
}

func hasClaimTemplate(spec appsv1.StatefulSetSpec, name string) bool {
	return slices.ContainsFunc(spec.VolumeClaimTemplates, func(pvc corev1.PersistentVolumeClaim) bool { return pvc.Name == name })
}

func TestBackupAgentContainer(t *testing.T) {
	t.Parallel()

	const uri = "mongodb://$(PBM_AGENT_MONGODB_USERNAME):$(PBM_AGENT_MONGODB_PASSWORD)@localhost:$(PBM_MONGODB_PORT)"

	pbmTLSMount := corev1.VolumeMount{Name: naming.PBMClientTLSVolumeName, MountPath: naming.PBMClientTLSDirectory, ReadOnly: true}
	nfsMount := corev1.VolumeMount{Name: "backup-nfs", MountPath: "/mnt/nfs/"}
	resources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("300m")},
	}

	tests := map[string]struct {
		setup         func(cr *api.PerconaServerMongoDB)
		tlsDisabled   bool
		wantEnv       []corev1.EnvVar
		wantNoEnv     []string
		wantMounts    []corev1.VolumeMount
		wantNoMounts  []string
		wantResources corev1.ResourceRequirements
	}{
		"tls enabled": {
			wantEnv: []corev1.EnvVar{
				{Name: "PBM_AGENT_TLS_DIR", Value: naming.PBMClientTLSDirectory},
				{Name: "PBM_AGENT_TLS_ENABLED", Value: "true"},
			},
			wantNoEnv:    []string{"SHARDED", "SSL_CERT_FILE"},
			wantMounts:   []corev1.VolumeMount{pbmTLSMount},
			wantNoMounts: []string{naming.BackupStorageCAInputVolumeName, naming.BackupStorageCAFileVolumeName},
		},
		"tls disabled": {
			tlsDisabled: true,
			wantEnv: []corev1.EnvVar{
				{Name: "PBM_MONGODB_URI", Value: uri},
				{Name: "PBM_AGENT_TLS_ENABLED", Value: "false"},
			},
			wantNoEnv:    []string{"PBM_AGENT_TLS_DIR"},
			wantNoMounts: []string{naming.PBMClientTLSVolumeName},
		},
		"sharded": {
			setup:   func(cr *api.PerconaServerMongoDB) { cr.Spec.Sharding.Enabled = true },
			wantEnv: []corev1.EnvVar{{Name: "SHARDED", Value: "TRUE"}},
		},
		"minio storage with caBundle": {
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Backup.Storages = map[string]api.BackupStorageSpec{
					"minio": {
						Type:  api.BackupStorageMinio,
						Minio: api.BackupStorageMinioSpec{CABundle: &corev1.SecretKeySelector{Name: "minio-ca"}},
					},
				}
			},
			wantEnv:    []corev1.EnvVar{{Name: "SSL_CERT_FILE", Value: "/etc/s3/certs/ca-bundle.crt"}},
			wantMounts: GetCAVolumeMounts(),
		},
		"backup volumeMounts": {
			setup:      func(cr *api.PerconaServerMongoDB) { cr.Spec.Backup.VolumeMounts = []corev1.VolumeMount{nfsMount} },
			wantMounts: []corev1.VolumeMount{nfsMount},
		},
		"backup resources": {
			setup:         func(cr *api.PerconaServerMongoDB) { cr.Spec.Backup.Resources = resources },
			wantResources: resources,
		},
		"backup hookScript": {
			setup: func(cr *api.PerconaServerMongoDB) { cr.Spec.Backup.HookScript.Script = "echo hook" },
			wantMounts: []corev1.VolumeMount{
				{Name: config.BinVolumeName, MountPath: config.BinMountPath},
				{Name: config.PBMHookscriptVolClaimName, MountPath: config.PBMHookscriptMountPath},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cr := &api.PerconaServerMongoDB{
				Name:      "some-name",
				Namespace: "some-ns",
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Secrets:   &api.SecretsSpec{Users: "some-users"},
					Replsets:  []*api.ReplsetSpec{{Name: "rs0"}},
					TLS:       &api.TLSSpec{Mode: api.TLSModePrefer, AllowInvalidCertificates: new(false)},
					Backup:    api.BackupSpec{Enabled: true, Image: "backup-image"},
				},
			}
			if tt.setup != nil {
				tt.setup(cr)
			}

			caCert, tlsCert, tlsKey, err := tls.Issue(tls.GetCertificateSans(cr))
			require.NoError(t, err)
			sslSecret := &corev1.Secret{Data: map[string][]byte{"ca.crt": caCert, "tls.crt": tlsCert, "tls.key": tlsKey}}

			c := backupAgentContainer(t.Context(), cr, "rs0", 27018, !tt.tlsDisabled, sslSecret)

			assert.Contains(t, c.Env, corev1.EnvVar{Name: "PBM_MONGODB_REPLSET", Value: "rs0"})
			assert.Contains(t, c.Env, corev1.EnvVar{Name: "PBM_MONGODB_PORT", Value: "27018"})
			for _, env := range tt.wantEnv {
				assert.Contains(t, c.Env, env)
			}
			for _, name := range tt.wantNoEnv {
				assert.False(t, slices.ContainsFunc(c.Env, func(e corev1.EnvVar) bool { return e.Name == name }), name)
			}
			assert.Equal(t, "PBM_MONGODB_URI", c.Env[slices.IndexFunc(c.Env, func(e corev1.EnvVar) bool { return e.Name == "POD_NAME" })+1].Name,
				"PBM_MONGODB_URI references POD_NAME and must follow it")

			for _, m := range tt.wantMounts {
				assert.Contains(t, c.VolumeMounts, m)
			}
			for _, name := range tt.wantNoMounts {
				assert.False(t, slices.ContainsFunc(c.VolumeMounts, func(m corev1.VolumeMount) bool { return m.Name == name }), name)
			}
			assert.Equal(t, tt.wantResources, c.Resources)
		})
	}
}

func TestStatefulSpecBackupAgent(t *testing.T) {
	t.Parallel()

	nfs := corev1.Volume{
		Name: "backup-nfs",
		VolumeSource: corev1.VolumeSource{
			NFS: &corev1.NFSVolumeSource{Server: "nfs-service", Path: "/psmdb"},
		},
	}

	tests := map[string]struct {
		setup         func(cr *api.PerconaServerMongoDB)
		wantAgent     bool
		wantVolumes   []string
		wantNoVolumes []string
	}{
		"backup disabled": {
			setup:         func(cr *api.PerconaServerMongoDB) { cr.Spec.Backup.Enabled = false },
			wantNoVolumes: []string{naming.PBMClientTLSVolumeName},
		},
		"backup enabled": {
			wantAgent:     true,
			wantVolumes:   []string{naming.PBMClientTLSVolumeName},
			wantNoVolumes: []string{naming.BackupStorageCAInputVolumeName, config.PBMHookscriptVolClaimName},
		},
		"tls disabled": {
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Unsafe.TLS = true
				cr.Spec.TLS = &api.TLSSpec{Mode: api.TLSModeDisabled}
			},
			wantAgent:     true,
			wantNoVolumes: []string{naming.PBMClientTLSVolumeName},
		},
		"minio storage with caBundle": {
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Backup.Storages = map[string]api.BackupStorageSpec{
					"minio": {
						Type:  api.BackupStorageMinio,
						Minio: api.BackupStorageMinioSpec{CABundle: &corev1.SecretKeySelector{Name: "minio-ca"}},
					},
				}
			},
			wantAgent:   true,
			wantVolumes: []string{naming.BackupStorageCAInputVolumeName, naming.BackupStorageCAFileVolumeName},
		},
		"backup hookScript": {
			setup:       func(cr *api.PerconaServerMongoDB) { cr.Spec.Backup.HookScript.Script = "echo hook" },
			wantAgent:   true,
			wantVolumes: []string{config.PBMHookscriptVolClaimName},
		},
		"replset sidecarVolumes": {
			setup:       func(cr *api.PerconaServerMongoDB) { cr.Spec.Replsets[0].SidecarVolumes = []corev1.Volume{nfs} },
			wantAgent:   true,
			wantVolumes: []string{nfs.Name},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cr, err := readDefaultCR("test-cr", "test-ns")
			require.NoError(t, err)
			rs := cr.Spec.Replsets[0]
			rs.Configuration = "net:\n  port: 27018\n"
			if tt.setup != nil {
				tt.setup(cr)
			}
			require.NoError(t, cr.CheckNSetDefaults(t.Context(), version.PlatformKubernetes))

			spec, err := StatefulSpec(t.Context(), cr, rs, naming.MongodLabels(cr, rs), cr.Spec.InitImage,
				StatefulConfigParams{}, StatefulSpecSecretParams{SSLSecret: &corev1.Secret{}})
			require.NoError(t, err)

			idx := slices.IndexFunc(spec.Template.Spec.Containers, func(c corev1.Container) bool {
				return c.Name == naming.ContainerBackupAgent
			})
			require.Equal(t, tt.wantAgent, idx != -1)
			if tt.wantAgent {
				assert.Contains(t, spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{Name: "PBM_MONGODB_PORT", Value: "27018"})
			}

			volumes := spec.Template.Spec.Volumes
			for _, name := range tt.wantVolumes {
				assert.True(t, slices.ContainsFunc(volumes, func(v corev1.Volume) bool { return v.Name == name }), name)
			}
			for _, name := range tt.wantNoVolumes {
				assert.False(t, slices.ContainsFunc(volumes, func(v corev1.Volume) bool { return v.Name == name }), name)
			}
			if slices.Contains(tt.wantVolumes, naming.PBMClientTLSVolumeName) {
				assert.Contains(t, volumes, PBMClientTLSVolume(cr))
				assert.Equal(t, "test-cr-pbm", PBMClientTLSVolume(cr).Secret.SecretName)
			}
			if slices.Contains(tt.wantVolumes, config.PBMHookscriptVolClaimName) {
				assert.Contains(t, volumes, corev1.Volume{
					Name: config.PBMHookscriptVolClaimName,
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "test-cr-pbm-hookscript"},
							Optional:             new(true),
						},
					},
				})
			}
			if slices.Contains(tt.wantVolumes, nfs.Name) {
				assert.Contains(t, volumes, nfs)
			}
		})
	}
}
