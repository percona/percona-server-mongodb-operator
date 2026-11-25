package naming

import (
	"testing"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func TestMongodContainerName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		component string
		expected  string
	}{
		"mongod":        {component: ComponentMongod, expected: "mongod"},
		"config server": {component: ComponentConfigSrv, expected: "mongod"},
		"hidden":        {component: ComponentHidden, expected: "mongod-hidden"},
		"non-voting":    {component: ComponentNonVoting, expected: "mongod-nv"},
		"arbiter":       {component: ComponentArbiter, expected: "mongod-arbiter"},
		"unknown":       {component: "", expected: "mongod"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := MongodContainerName(tt.component); got != tt.expected {
				t.Errorf("MongodContainerName(%q) = %q, want %q", tt.component, got, tt.expected)
			}
		})
	}
}

func TestVPAStatusKey(t *testing.T) {
	tests := map[string]struct {
		rsName    string
		component string
		expected  string
	}{
		"mongod":        {rsName: "rs0", component: ComponentMongod, expected: "rs0"},
		"config server": {rsName: "cfg", component: ComponentConfigSrv, expected: "cfg"},
		"non-voting":    {rsName: "rs0", component: ComponentNonVoting, expected: "rs0-nv"},
		"hidden":        {rsName: "rs0", component: ComponentHidden, expected: "rs0-hidden"},
		"mongos":        {rsName: "", component: ComponentMongos, expected: "mongos"},
		// VPA does not manage the arbiter. Keying it on the replset name would
		// overwrite that replset's own recommendation.
		"arbiter": {rsName: "rs0", component: ComponentArbiter, expected: ""},
		"unknown": {rsName: "rs0", component: "", expected: ""},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := VPAStatusKey(tt.rsName, tt.component); got != tt.expected {
				t.Errorf("VPAStatusKey(%q, %q) = %q, want %q", tt.rsName, tt.component, got, tt.expected)
			}
		})
	}
}

func TestJournalVolumeSnapshotName(t *testing.T) {
	t.Parallel()

	bcp := &psmdbv1.PerconaServerMongoDBBackup{
		Name: "backup",
		Spec: psmdbv1.PerconaServerMongoDBBackupSpec{ClusterName: "cluster"},
	}

	if data, journal := VolumeSnapshotName(bcp, "rs0-journal"), JournalVolumeSnapshotName(bcp, "rs0"); data == journal {
		t.Errorf("data snapshot of rs0-journal and journal snapshot of rs0 share the name %q", data)
	}
}
