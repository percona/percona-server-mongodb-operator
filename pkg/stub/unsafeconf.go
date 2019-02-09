package stub

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/sirupsen/logrus"
)

func setSafeDefault(replset *v1alpha1.ReplsetSpec) {
	logrus.Infof("allowUnsafeConfigurations option has been disabled. Config will be optimized for safe usage!")

	arbiterDefaults(replset)

	if replset.Arbiter != nil && replset.Arbiter.Enabled {

		if replset.Arbiter.Size > 1 {
			logrus.Infof("Arbiter size will be decreased from %d to 1", replset.Arbiter.Size)
			replset.Arbiter.Size = 1
		}

		if (replset.Size+replset.Arbiter.Size)%2 == 0 {
			replset.Size++
		}

	} else {

		if replset.Size%2 == 0 {
			replset.Size++
		}
	}
}
