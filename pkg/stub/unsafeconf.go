package stub

import "github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

func setSafeDefault(replset *v1alpha1.ReplsetSpec) {
	if replset.Arbiter != nil && replset.Arbiter.Enabled {
		replset.Arbiter.Size = 1

		if (replset.Size+replset.Arbiter.Size)%2 == 0 {
			replset.Size += 1
		}
	}

	if replset.Size%2 == 0 {
		replset.Size += 1
	}
}
