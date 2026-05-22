package v1

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestUnmanagedUsesTCPLivenessProbe(t *testing.T) {
	t.Parallel()

	cr := &PerconaServerMongoDB{}
	cr.Name = "my-cluster-name"
	cr.Spec.CRVersion = "1.22.0"
	cr.Spec.Image = "percona/percona-server-mongodb:8.0.19-7"
	cr.Spec.Unmanaged = true
	cr.Spec.UpdateStrategy = appsv1.RollingUpdateStatefulSetStrategyType
	volumeSpec := &VolumeSpec{
		PersistentVolumeClaim: PVCSpec{
			PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("3Gi"),
					},
				},
			},
		},
	}
	cr.Spec.Replsets = []*ReplsetSpec{{
		Name: "rs0",
		Size: 3,
		VolumeSpec: volumeSpec,
		NonVoting: NonVotingSpec{
			Enabled: true,
			Size:    1,
			VolumeSpec: volumeSpec,
		},
		Hidden: HiddenSpec{
			Enabled: true,
			Size:    1,
			VolumeSpec: volumeSpec,
		},
	}}

	if err := cr.CheckNSetDefaults(context.Background(), version.PlatformKubernetes); err != nil {
		t.Fatalf("CheckNSetDefaults() error = %v", err)
	}

	rs := cr.Spec.Replsets[0]

	assertTCPLivenessProbe(t, rs.GetPort(), rs.LivenessProbe)
	assertTCPLivenessProbe(t, rs.GetPort(), rs.NonVoting.LivenessProbe)
	assertTCPLivenessProbe(t, rs.GetPort(), rs.Hidden.LivenessProbe)
}

func assertTCPLivenessProbe(t *testing.T, port int32, probe *LivenessProbeExtended) {
	t.Helper()

	if probe == nil {
		t.Fatal("liveness probe is nil")
	}
	if probe.Exec != nil {
		t.Fatalf("expected exec liveness probe to be nil, got %v", probe.Exec.Command)
	}
	if probe.TCPSocket == nil {
		t.Fatal("expected tcp socket liveness probe")
	}
	if got := probe.TCPSocket.Port.IntValue(); got != int(port) {
		t.Fatalf("tcp socket port = %d, want %d", got, port)
	}
}
