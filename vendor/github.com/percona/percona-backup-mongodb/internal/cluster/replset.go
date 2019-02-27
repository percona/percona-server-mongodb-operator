package cluster

import (
	"sync"

	"github.com/globalsign/mgo"
	"github.com/globalsign/mgo/bson"
	"github.com/percona/percona-backup-mongodb/mdbstructs"
)

// HasReplsetMemberTags returns a boolean reflecting whether or not
// a replica set config member matches a list of replica set tags
//
// https://docs.mongodb.com/manual/reference/replica-configuration/#rsconf.members[n].tags
//
func HasReplsetMemberTags(member *mdbstructs.ReplsetConfigMember, tags map[string]string) bool {
	if len(member.Tags) == 0 || len(tags) == 0 {
		return false
	}
	for key, val := range tags {
		if tagVal, ok := member.Tags[key]; ok {
			if tagVal != val {
				return false
			}
		} else {
			return false
		}
	}
	return true
}

// getReplsetConfig returns a struct representing the "replSetGetConfig" server
// command
//
// https://docs.mongodb.com/manual/reference/command/replSetGetConfig/
//
func getReplsetConfig(session *mgo.Session) (*mdbstructs.ReplsetConfig, error) {
	rsGetConfig := mdbstructs.ReplSetGetConfig{}
	err := session.Run(bson.D{{Name: "replSetGetConfig", Value: "1"}}, &rsGetConfig)
	return rsGetConfig.Config, err
}

// getReplsetStatus returns a struct representing the "replSetGetStatus" server
// command
//
// https://docs.mongodb.com/manual/reference/command/replSetGetStatus/
//
func getReplsetStatus(session *mgo.Session) (*mdbstructs.ReplsetStatus, error) {
	status := mdbstructs.ReplsetStatus{}
	err := session.Run(bson.D{{Name: "replSetGetStatus", Value: "1"}}, &status)
	return &status, err
}

type Replset struct {
	sync.Mutex
	session *mgo.Session
	config  *mdbstructs.ReplsetConfig
	status  *mdbstructs.ReplsetStatus
}

func NewReplset(session *mgo.Session) (*Replset, error) {
	r := &Replset{session: session}
	return r, r.RefreshState()

}

// RefreshState updates the replica set state
func (r *Replset) RefreshState() error {
	r.Lock()
	defer r.Unlock()

	var err error

	r.config, err = getReplsetConfig(r.session)
	if err != nil {
		return err
	}

	r.status, err = getReplsetStatus(r.session)
	return err
}

// Name returns the replica set name as a string
func (r *Replset) Name() string {
	r.Lock()
	defer r.Unlock()
	return r.config.Name
}

// Config returns the replica set status
func (r *Replset) Config() *mdbstructs.ReplsetConfig {
	r.Lock()
	defer r.Unlock()
	return r.config
}

// Status returns the replica set config
func (r *Replset) Status() *mdbstructs.ReplsetStatus {
	r.Lock()
	defer r.Unlock()
	return r.status
}

// ID returns the replica set ID as a bson.ObjectId
func (r *Replset) ID() *bson.ObjectId {
	r.Lock()
	defer r.Unlock()
	return &r.config.Settings.ReplicaSetId
}

// BackupSource returns the the most appropriate replica set member
// to become the source of the backup. The chosen node should cause
// the least impact/risk possible during backup
//func (r *Replset) BackupSource() (*mdbstructs.ReplsetConfigMember, error) {
//	// todo: pass replset-tags instead of nil
//	scorer, err := r.scoreMembers(nil)
//	if err != nil {
//		return nil, err
//	}
//	return scorer.Winner().config, nil
//}
