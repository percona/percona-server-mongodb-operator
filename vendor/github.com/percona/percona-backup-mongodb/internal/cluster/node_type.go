package cluster

import "github.com/globalsign/mgo"

const (
	NodeTypeUndefined = iota
	NodeTypeMongod
	NodeTypeMongodReplicaset
	NodeTypeMongodShardSvr
	NodeTypeMongodConfigSvr
	NodeTypeMongos
)

func getNodeType(session *mgo.Session) (int, error) {
	isMaster, err := NewIsMaster(session)
	if err != nil {
		return NodeTypeUndefined, err
	}
	if isMaster.IsConfigServer() {
		return NodeTypeMongodConfigSvr, nil
	}
	if isMaster.IsShardServer() {
		return NodeTypeMongodShardSvr, nil
	}
	if isMaster.IsReplset() {
		return NodeTypeMongodReplicaset, nil
	}
	if isMaster.IsMongos() {
		return NodeTypeMongos, nil
	}
	return NodeTypeMongod, nil
}
