package stub

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/sirupsen/logrus"
)

func setSafeDefault(replset *v1alpha1.ReplsetSpec) {
	arbiterDefaults(replset)

	if replset.Arbiter != nil && replset.Arbiter.Enabled {

		if replset.Arbiter.Size > 1 {
			logrus.Infof("Arbiter size will be decreased from %d to 1 due to safe config", replset.Arbiter.Size)
			logrus.Infof("Set allowUnsafeConfigurations=true to disable safe configuration")
			replset.Arbiter.Size = 1
		}

		if (replset.Size+replset.Arbiter.Size)%2 == 0 {
			logrus.Infof("Replset size will be increased from %d to %d", replset.Size, replset.Size+1)
			logrus.Infof("Set allowUnsafeConfigurations=true to disable safe configuration")

			replset.Size++
		}

	} else {

		if replset.Size%2 == 0 {
			logrus.Infof("Replset size will be increased from %d to %d", replset.Size, replset.Size+1)
			logrus.Infof("Set allowUnsafeConfigurations=true to disable safe configuration")
			replset.Size++
		}
	}
}
