package membergroup

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

// testCR is the minimal cluster the resolve tests derive identity from. Resolve
// reads only the name, so no defaulting is needed here; tests that exercise
// defaulting build their own CR.
func testCR() *api.PerconaServerMongoDB {
	return &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster1", Namespace: "psmdb"},
		Spec:       api.PerconaServerMongoDBSpec{CRVersion: "1.24.0"},
	}
}

func vol(size string) *api.VolumeSpec {
	return &api.VolumeSpec{PersistentVolumeClaim: api.PVCSpec{
		PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)},
			},
		},
	}}
}

// inst builds an instance carrying an rsConfig object. A non-nil rsConfig —
// even an empty one — is what makes UseImplicitVotePolicy report false, so the
// choice between inst and implicitInst decides the resolved policy.
func inst(name string, replicas int32, cfg *api.MemberConfigSpec) api.InstanceSpec {
	if cfg == nil {
		cfg = &api.MemberConfigSpec{}
	}
	return api.InstanceSpec{Name: name, Replicas: replicas, RSConfig: cfg, VolumeSpec: vol("1Gi")}
}

// implicitInst builds an instance with no rsConfig at all. A replica set made
// only of these, all under reserved names, keeps the implicit vote policy.
func implicitInst(name string, replicas int32) api.InstanceSpec {
	return api.InstanceSpec{Name: name, Replicas: replicas, VolumeSpec: vol("1Gi")}
}

// arbiterInst builds an arbiter-only instance. It declares no volumeSpec: a CEL
// rule rejects one, and Resolve drops it regardless.
func arbiterInst(name string, replicas int32) api.InstanceSpec {
	return api.InstanceSpec{
		Name:     name,
		Replicas: replicas,
		RSConfig: &api.MemberConfigSpec{ArbiterOnly: new(true), Votes: new(int32(1)), Priority: new(int32(0))},
	}
}

// legacyRS builds a replica set in the pre-instances[] shape.
func legacyRS(name string, size int32) *api.ReplsetSpec {
	return &api.ReplsetSpec{Name: name, Size: size, VolumeSpec: vol("1Gi")}
}

// instanceRS builds a replica set whose topology comes from instances[].
func instanceRS(name string, instances ...api.InstanceSpec) *api.ReplsetSpec {
	return &api.ReplsetSpec{Name: name, Instances: instances}
}
