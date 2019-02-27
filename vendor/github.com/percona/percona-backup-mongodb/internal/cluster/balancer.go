package cluster

import (
	"errors"
	"time"

	"github.com/globalsign/mgo"
	"github.com/globalsign/mgo/bson"
	"github.com/percona/percona-backup-mongodb/mdbstructs"
)

// BalancerCmdTimeoutMs is the amount of time in milliseconds
// to wait for a balancer command to run. 60sec is the same
// default used in the 'mongo' shell
var BalancerCmdTimeoutMs = 60000

type Balancer struct {
	session    *mgo.Session
	wasEnabled bool
}

func NewBalancer(session *mgo.Session) (*Balancer, error) {
	var err error
	b := &Balancer{session: session}
	b.wasEnabled, err = b.IsEnabled()
	return b, err
}

// RestoreState ensures the balancer is restored to its original state
func (b *Balancer) RestoreState() error {
	isEnabled, err := b.IsEnabled()
	if err != nil {
		return err
	}
	if b.wasEnabled && !isEnabled {
		return b.Start()
	}
	return nil
}

// getStatus returns a struct representing the result of the
// MongoDB 'balancerStatus' command. This command will only
// succeed on a session to a mongos process (as of MongoDB 3.6)
//
// https://docs.mongodb.com/manual/reference/command/balancerStatus/
//
func (b *Balancer) getStatus() (*mdbstructs.BalancerStatus, error) {
	status := mdbstructs.BalancerStatus{}
	err := b.session.Run(bson.D{{"balancerStatus", "1"}}, &status)
	return &status, err
}

// runBalancerCommand is a helper for running a
// balancerStart/balancerStop server command
func (b *Balancer) runBalancerCommand(balancerCommand string) error {
	okResp := mdbstructs.OkResponse{}
	err := b.session.Run(bson.D{
		{balancerCommand, "1"},
		{"maxTimeMS", BalancerCmdTimeoutMs},
	}, &okResp)
	if err != nil {
		return err
	} else if okResp.Ok != 1 {
		return errors.New("got failed response from server")
	}
	return nil
}

// IsEnabled returns a boolean reflecting if the balancer
// is enabled
func (b *Balancer) IsEnabled() (bool, error) {
	status, err := b.getStatus()
	if err != nil {
		return false, err
	}
	return status.Mode == mdbstructs.BalancerModeFull, nil
}

// IsRunning returns a boolean reflecting if the balancer
// is currently running
func (b *Balancer) IsRunning() (bool, error) {
	status, err := b.getStatus()
	if err != nil {
		return false, err
	}
	return status.InBalancerRound, nil
}

// Stop performs a 'balancerStop' server command on
// the provided session
//
// https://docs.mongodb.com/manual/reference/command/balancerStop/
//
func (b *Balancer) Stop() error {
	return b.runBalancerCommand("balancerStop")
}

// Start performs a 'balancerStart' server command on
// the provided session
//
// https://docs.mongodb.com/manual/reference/command/balancerStart/
//
func (b *Balancer) Start() error {
	return b.runBalancerCommand("balancerStart")
}

// StopAndWait performs a Stop and then waits for
// the balancer to stop running any balancer operations
func (b *Balancer) StopAndWait(retries int, retryInterval time.Duration) error {
	err := b.Stop()
	if err != nil {
		return err
	}
	var tries int
	for tries < retries {
		isRunning, err := b.IsRunning()
		if err != nil {
			return err
		} else if !isRunning {
			return nil
		}
		tries++
		time.Sleep(retryInterval)
	}
	return errors.New("balancer did not stop")
}
