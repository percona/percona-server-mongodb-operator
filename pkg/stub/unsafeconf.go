package stub

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/sirupsen/logrus"
)

func setSafeDefault(replset *v1alpha1.ReplsetSpec) {
	arbiterDefaults(replset)
	var dolog bool

	if replset.Arbiter != nil && replset.Arbiter.Enabled {

		if replset.Arbiter.Size > 1 {
			logrus.Infof("allowUnsafeConfigurations option has been disabled. Config will be optimized for safe usage!")
			logrus.Infof("Arbiter size will be decreased from %d to 1", replset.Arbiter.Size)
			replset.Arbiter.Size = 1
			dolog = true
		}

		if (replset.Size+replset.Arbiter.Size)%2 == 0 {
			if dolog {
				logrus.Infof("allowUnsafeConfigurations option has been disabled. Config will be optimized for safe usage!")
			}
			logrus.Infof("Replset size will be increased from %d to %d", replset.Size, replset.Size+1)
			replset.Size++
		}

	} else {

		if replset.Size%2 == 0 {
			logrus.Infof("allowUnsafeConfigurations option has been disabled. Config will be optimized for safe usage!")
			logrus.Infof("Replset size will be increased from %d to %d", replset.Size, replset.Size+1)
			replset.Size++
		}
	}
}
