package stub

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/config"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/sirupsen/logrus"
)

func setSafeDefault(replset *v1alpha1.ReplsetSpec) {
	// Replset size can't be 0 or 1.
	// But 2 + the Arbiter is possible.
	if replset.Size < 2 {
		replset.Size = config.DefaultMongodSize
		logrus.Infof("Replset size will be changed from %d to %d due to safe config", replset.Size, config.DefaultMongodSize)
		logrus.Infof("Set allowUnsafeConfigurations=true to disable safe configuration")
	}

	if replset.Arbiter != nil && replset.Arbiter.Enabled {
		if replset.Arbiter.Size != 1 {
			logrus.Infof("Arbiter size will be changed from %d to 1 due to safe config", replset.Arbiter.Size)
			logrus.Infof("Set allowUnsafeConfigurations=true to disable safe configuration")
			replset.Arbiter.Size = 1
		}
		if replset.Size%2 != 0 {
			logrus.Infof("Arbiter will be switched off. There is no need in arbiter with odd replset size (%d)", replset.Size)
			logrus.Infof("Set allowUnsafeConfigurations=true to disable safe configuration")
			replset.Arbiter.Enabled = false
			replset.Arbiter.Size = 0
		}
	} else {
		if replset.Size%2 == 0 {
			logrus.Infof("Replset size will be increased from %d to %d", replset.Size, replset.Size+1)
			logrus.Infof("Set allowUnsafeConfigurations=true to disable safe configuration")
			replset.Size++
		}
	}
}
