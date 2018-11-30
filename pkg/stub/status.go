package stub

import (
	"fmt"
	"reflect"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/operator-framework/operator-sdk/pkg/sdk"
	podk8s "github.com/percona/mongodb-orchestration-tools/pkg/pod/k8s"
	"github.com/sirupsen/logrus"
	mgo "gopkg.in/mgo.v2"
	corev1 "k8s.io/api/core/v1"
)

// getReplsetStatus returns a ReplsetStatus object for a given replica set
func getReplsetStatus(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) *v1alpha1.ReplsetStatus {
	for _, rs := range m.Status.Replsets {
		if rs.Name == replset.Name {
			return rs
		}
	}
	status := &v1alpha1.ReplsetStatus{
		Name:    replset.Name,
		Members: []*v1alpha1.ReplsetMemberStatus{},
	}
	m.Status.Replsets = append(m.Status.Replsets, status)
	return status
}

// getReplsetMemberStatuses returns a list of ReplsetMemberStatus structs for a given replset
func getReplsetMemberStatuses(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod, usersSecret *corev1.Secret) []*v1alpha1.ReplsetMemberStatus {
	members := make([]*v1alpha1.ReplsetMemberStatus, 0)
	for _, pod := range pods {
		dialInfo := getReplsetDialInfo(m, replset, []corev1.Pod{pod}, usersSecret)
		dialInfo.Direct = true
		session, err := mgo.DialWithInfo(dialInfo)
		if err != nil {
			logrus.Debugf("Cannot connect to mongodb host %s: %v", dialInfo.Addrs[0], err)
			continue
		}
		session.SetMode(mgo.Eventual, true)

		logrus.Debugf("Updating status for host: %s", dialInfo.Addrs[0])

		buildInfo, err := session.BuildInfo()
		if err != nil {
			logrus.Debugf("Cannot get buildInfo from mongodb host %s: %v", dialInfo.Addrs[0], err)
			continue
		}

		members = append(members, &v1alpha1.ReplsetMemberStatus{
			Name:    dialInfo.Addrs[0],
			Version: buildInfo.Version,
		})
		session.Close()
	}
	return members
}

// updateStatus updates the PerconaServerMongoDB status
func (h *Handler) updateStatus(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, usersSecret *corev1.Secret) (*corev1.PodList, error) {
	var doUpdate bool

	podsList := podList()
	err := h.client.List(m.Namespace, podsList, sdk.WithListOptions(
		internal.GetLabelSelectorListOpts(m, replset),
	))
	if err != nil {
		return nil, fmt.Errorf("failed to list pods for replset %s: %v", replset.Name, err)
	}

	// Update status pods list
	podNames := getPodNames(podsList.Items)
	status := getReplsetStatus(m, replset)
	if !reflect.DeepEqual(podNames, status.Pods) {
		status.Pods = podNames
		doUpdate = true
	}

	// Update mongodb replset member status list
	members := getReplsetMemberStatuses(m, replset, podsList.Items, usersSecret)
	if !reflect.DeepEqual(members, status.Members) {
		status.Members = members
		doUpdate = true
	}

	// Send update to SDK if something changed
	if doUpdate {
		err = h.client.Update(m)
		if err != nil {
			return nil, fmt.Errorf("failed to update status for replset %s: %v", replset.Name, err)
		}
	}

	// Update the pods list that is read by the watchdog
	if h.pods == nil {
		h.pods = podk8s.NewPods(m.Name, m.Namespace)
	}
	h.pods.SetPods(podsList.Items)

	return podsList, nil
}
