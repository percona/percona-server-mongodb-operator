package psmdb

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestCollectStorageCABundles(t *testing.T) {
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
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "minio-ca",
										},
										Key: "ca.crt",
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
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "minio-ca",
										},
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
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "shared-ca",
										},
										Key: "ca.crt",
									},
								},
							},
							"minio2": {
								Type: api.BackupStorageMinio,
								Minio: api.BackupStorageMinioSpec{
									CABundle: &corev1.SecretKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "shared-ca",
										},
										Key: "ca.crt",
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
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "multi-ca",
										},
										Key: "ca1.crt",
									},
								},
							},
							"minio2": {
								Type: api.BackupStorageMinio,
								Minio: api.BackupStorageMinioSpec{
									CABundle: &corev1.SecretKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "multi-ca",
										},
										Key: "ca2.crt",
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
	liveness := &corev1.Probe{
		InitialDelaySeconds: 15,
		PeriodSeconds:       7,
		ProbeHandler: corev1.ProbeHandler{
			Exec: &corev1.ExecAction{Command: []string{"/bin/true"}},
		},
	}
	readiness := &corev1.Probe{
		InitialDelaySeconds: 5,
		PeriodSeconds:       3,
		ProbeHandler: corev1.ProbeHandler{
			TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(27017)},
		},
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
