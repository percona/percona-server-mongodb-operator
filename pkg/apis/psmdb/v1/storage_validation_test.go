package v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func testStorageSpec() *StorageSpec {
	return &StorageSpec{
		PersistentVolumeClaim: &PVCSpec{
			PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
				},
			},
		},
	}
}

func pvcVolume() *VolumeSpec {
	return &VolumeSpec{PersistentVolumeClaim: PVCSpec{PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{}}}
}

func emptyDirVolume() *VolumeSpec {
	return &VolumeSpec{EmptyDir: &corev1.EmptyDirVolumeSource{}}
}

func TestValidateJournalStorage(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		crVersion string
		rs        func() *ReplsetSpec
		wantErr   error
	}{
		"no journal storage": {
			rs: func() *ReplsetSpec {
				return &ReplsetSpec{VolumeSpec: emptyDirVolume()}
			},
		},
		"journal on a pvc data volume": {
			rs: func() *ReplsetSpec {
				return &ReplsetSpec{VolumeSpec: pvcVolume(), JournalStorage: testStorageSpec()}
			},
		},
		"journal on an emptyDir data volume": {
			rs: func() *ReplsetSpec {
				return &ReplsetSpec{VolumeSpec: emptyDirVolume(), JournalStorage: testStorageSpec()}
			},
			wantErr: errJournalNoDataPVC,
		},
		"journal with the inMemory engine": {
			rs: func() *ReplsetSpec {
				return &ReplsetSpec{
					VolumeSpec:     pvcVolume(),
					Storage:        &MongodSpecStorage{Engine: StorageEngineInMemory},
					JournalStorage: testStorageSpec(),
				}
			},
			wantErr: errJournalInMemory,
		},
		"journal on an old crVersion": {
			crVersion: "1.23.0",
			rs: func() *ReplsetSpec {
				return &ReplsetSpec{VolumeSpec: pvcVolume(), JournalStorage: testStorageSpec()}
			},
			wantErr: errStorageCRVersion,
		},
		"nonvoting journal on a hostPath data volume": {
			rs: func() *ReplsetSpec {
				return &ReplsetSpec{
					VolumeSpec: pvcVolume(),
					NonVoting: NonVotingSpec{
						Enabled:        true,
						VolumeSpec:     &VolumeSpec{HostPath: &corev1.HostPathVolumeSource{Path: "/data"}},
						JournalStorage: testStorageSpec(),
					},
				}
			},
			wantErr: errJournalNoDataPVC,
		},
		"disabled nonvoting is ignored": {
			rs: func() *ReplsetSpec {
				return &ReplsetSpec{
					VolumeSpec: pvcVolume(),
					NonVoting:  NonVotingSpec{VolumeSpec: emptyDirVolume(), JournalStorage: testStorageSpec()},
				}
			},
		},
		"hidden journal on an emptyDir data volume": {
			rs: func() *ReplsetSpec {
				return &ReplsetSpec{
					VolumeSpec: pvcVolume(),
					Hidden: HiddenSpec{
						Enabled:        true,
						VolumeSpec:     emptyDirVolume(),
						JournalStorage: testStorageSpec(),
					},
				}
			},
			wantErr: errJournalNoDataPVC,
		},
		"hidden journal on a pvc data volume": {
			rs: func() *ReplsetSpec {
				return &ReplsetSpec{
					VolumeSpec: pvcVolume(),
					Hidden:     HiddenSpec{Enabled: true, VolumeSpec: pvcVolume(), JournalStorage: testStorageSpec()},
				}
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cr := &PerconaServerMongoDB{Spec: PerconaServerMongoDBSpec{CRVersion: version.Version()}}
			if tt.crVersion != "" {
				cr.Spec.CRVersion = tt.crVersion
			}

			err := cr.validateJournalStorage(tt.rs())
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateLogStorage(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		crVersion    string
		logCollector *LogCollectorSpec
		wantErr      error
	}{
		"no log collector":         {},
		"no storage":               {logCollector: &LogCollectorSpec{Enabled: true}},
		"storage on current crVer": {logCollector: &LogCollectorSpec{Enabled: true, Storage: testStorageSpec()}},
		"storage on old crVersion": {
			crVersion:    "1.23.0",
			logCollector: &LogCollectorSpec{Enabled: true, Storage: testStorageSpec()},
			wantErr:      errStorageCRVersion,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cr := &PerconaServerMongoDB{Spec: PerconaServerMongoDBSpec{CRVersion: version.Version(), LogCollector: tt.logCollector}}
			if tt.crVersion != "" {
				cr.Spec.CRVersion = tt.crVersion
			}

			err := cr.validateLogStorage()
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestSetDefaultAccessModes(t *testing.T) {
	t.Parallel()

	rwx := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}

	tests := map[string]struct {
		pvc  *PVCSpec
		want []corev1.PersistentVolumeAccessMode
	}{
		"nil spec": {},
		"nil claim spec": {
			pvc: &PVCSpec{},
		},
		"defaults to ReadWriteOnce": {
			pvc:  &PVCSpec{PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{}},
			want: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		},
		"keeps user access modes": {
			pvc:  &PVCSpec{PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{AccessModes: rwx}},
			want: rwx,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			setDefaultAccessModes(tt.pvc)

			if tt.pvc == nil || tt.pvc.PersistentVolumeClaimSpec == nil {
				assert.Nil(t, tt.want)
				return
			}
			assert.Equal(t, tt.want, tt.pvc.AccessModes)
		})
	}
}
