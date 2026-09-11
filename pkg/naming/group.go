package naming

import (
	"strconv"
	"strings"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

// Reserved group names, re-exported from the API package so callers in the
// workload and controller packages need only one import for identity.
const (
	GroupMongod    = psmdbv1.ReservedGroupMongod
	GroupNonVoting = psmdbv1.ReservedGroupNonVoting
	GroupArbiter   = psmdbv1.ReservedGroupArbiter
	GroupHidden    = psmdbv1.ReservedGroupHidden
)

type groupIdentity struct {
	component    string
	container    string
	configSuffix string
}

var reservedGroups = map[string]groupIdentity{
	GroupMongod: {
		component:    ComponentMongod,
		container:    ContainerMongod,
		configSuffix: ComponentMongod,
	},
	GroupNonVoting: {
		component:    ComponentNonVoting,
		container:    ContainerNonVoting,
		configSuffix: ComponentNonVotingShort,
	},
	GroupHidden: {
		component:    ComponentHidden,
		container:    ContainerHidden,
		configSuffix: ComponentHidden,
	},
	GroupArbiter: {
		component:    ComponentArbiter,
		container:    ContainerArbiter,
		configSuffix: ComponentMongod,
	},
}

func GroupComponent(group string) string {
	if id, ok := reservedGroups[group]; ok {
		return id.component
	}
	return group
}

func GroupStatefulSetName(cr *psmdbv1.PerconaServerMongoDB, rs *psmdbv1.ReplsetSpec, group string) string {
	return psmdbv1.DerivedStatefulSetName(cr.Name, rs.Name, group)
}

func GroupContainerName(group string, arbiterOnly bool) string {
	if id, ok := reservedGroups[group]; ok {
		return id.container
	}
	if arbiterOnly {
		return ContainerArbiter
	}
	return ContainerMongod
}

func GroupConfigMapName(cr *psmdbv1.PerconaServerMongoDB, rs *psmdbv1.ReplsetSpec, group string) string {
	suffix := group
	if id, ok := reservedGroups[group]; ok {
		suffix = id.configSuffix
	}
	return cr.Name + "-" + rs.Name + "-" + suffix
}

func GroupHookScriptConfigMapName(cr *psmdbv1.PerconaServerMongoDB, rs *psmdbv1.ReplsetSpec, group string) string {
	return HookScriptConfigMapName(cr, rs, GroupComponent(group))
}

func GroupPodName(cr *psmdbv1.PerconaServerMongoDB, rs *psmdbv1.ReplsetSpec, group string, idx int) string {
	return GroupStatefulSetName(cr, rs, group) + "-" + strconv.Itoa(idx)
}

func GroupLabels(cr *psmdbv1.PerconaServerMongoDB, rs *psmdbv1.ReplsetSpec, group string) map[string]string {
	ls := RSLabels(cr, rs)
	ls[LabelKubernetesComponent] = GroupComponent(group)
	return ls
}

func PodOrdinal(podName string) (int, bool) {
	idx := strings.LastIndex(podName, "-")
	if idx < 0 || idx == len(podName)-1 {
		return -1, false
	}
	ordinal, err := strconv.Atoi(podName[idx+1:])
	if err != nil || ordinal < 0 {
		return -1, false
	}
	return ordinal, true
}
