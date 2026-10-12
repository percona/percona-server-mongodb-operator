package v1

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
)

func TestPBMBackupType(t *testing.T) {
	tests := map[string]struct {
		specType defs.BackupType
		expected defs.BackupType
	}{
		"empty defaults to logical": {
			specType: "",
			expected: defs.LogicalBackup,
		},
		"logical": {
			specType: defs.LogicalBackup,
			expected: defs.LogicalBackup,
		},
		"physical": {
			specType: defs.PhysicalBackup,
			expected: defs.PhysicalBackup,
		},
		"external": {
			specType: defs.ExternalBackup,
			expected: defs.ExternalBackup,
		},
		"incremental": {
			specType: defs.IncrementalBackup,
			expected: defs.IncrementalBackup,
		},
		"incremental-base maps to incremental": {
			specType: BackupTypeIncrementalBase,
			expected: defs.IncrementalBackup,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			bcp := &PerconaServerMongoDBBackup{
				Spec: PerconaServerMongoDBBackupSpec{
					Type: tt.specType,
				},
			}
			assert.Equal(t, tt.expected, bcp.PBMBackupType())
		})
	}
}
