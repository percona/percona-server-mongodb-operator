package stub

import "github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

func setSafeDefault(replset *v1alpha1.ReplsetSpec) {
	if replset.Size > 3 {
		replset.Size = 3
	}

	if replset.Arbiter != nil && replset.Arbiter.Enabled {
		replset.Arbiter.Size = 1
		replset.Size = 2
	}
}
