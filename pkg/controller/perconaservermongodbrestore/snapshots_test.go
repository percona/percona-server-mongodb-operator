package perconaservermongodbrestore

import (
	"context"
	"fmt"
	"testing"

	volumesnapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

func TestGeneratePVCFromSnapshot(t *testing.T) {
	storageClassName := "fast"
	spec := corev1.PersistentVolumeClaimSpec{
		AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		StorageClassName: &storageClassName,
	}

	pvc := &corev1.PersistentVolumeClaim{}
	generatePVCFromSnapshot(pvc, spec, "my-snapshot", "my-restore")

	assert.Equal(t, spec.AccessModes, pvc.Spec.AccessModes)
	assert.Equal(t, spec.StorageClassName, pvc.Spec.StorageClassName)

	require.NotNil(t, pvc.Spec.DataSource)
	assert.Equal(t, "my-snapshot", pvc.Spec.DataSource.Name)
	assert.Equal(t, "VolumeSnapshot", pvc.Spec.DataSource.Kind)
	assert.Equal(t, ptr.To(volumesnapshotv1.SchemeGroupVersion.Group), pvc.Spec.DataSource.APIGroup)
	assert.Equal(t, "my-restore", pvc.Annotations[naming.AnnotationRestoreName])
}

func TestGeneratePVCFromSnapshot_OverwritesExistingSpec(t *testing.T) {
	oldClassName := "old-class"
	newClassName := "new-class"

	pvc := &corev1.PersistentVolumeClaim{
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &oldClassName,
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
		},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"some-old-annotation": "value",
			},
		},
	}

	newSpec := corev1.PersistentVolumeClaimSpec{
		StorageClassName: &newClassName,
		AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
	}
	generatePVCFromSnapshot(pvc, newSpec, "new-snapshot", "new-restore")

	assert.Equal(t, &newClassName, pvc.Spec.StorageClassName)
	assert.Equal(t, []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, pvc.Spec.AccessModes)
	assert.Equal(t, "new-snapshot", pvc.Spec.DataSource.Name)
	// Annotations should be replaced (only the restore-name annotation)
	assert.Equal(t, "new-restore", pvc.Annotations[naming.AnnotationRestoreName])
}

func TestReconcileSnapshotNew(t *testing.T) {
	r := fakeReconciler()

	restore := &psmdbv1.PerconaServerMongoDBRestore{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-restore",
			Namespace: "default",
		},
		Status: psmdbv1.PerconaServerMongoDBRestoreStatus{
			State:   psmdbv1.RestoreStateNew,
			PBMname: "my-pbm-restore",
		},
	}

	status, err := r.reconcileSnapshotNew(restore)
	assert.NoError(t, err)
	assert.Equal(t, psmdbv1.RestoreStateWaiting, status.State)
	// Other status fields should be preserved
	assert.Equal(t, "my-pbm-restore", status.PBMname)
}

func TestScaleDownStatefulSetsForSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	const ns = "default"

	cluster := &psmdbv1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: ns,
		},
		Spec: psmdbv1.PerconaServerMongoDBSpec{
			Replsets: []*psmdbv1.ReplsetSpec{
				{Name: "rs0", Size: 3},
			},
		},
	}
	rs := cluster.Spec.Replsets[0]

	t.Run("condition already set returns true immediately", func(t *testing.T) {
		r := fakeReconciler(cluster)
		status := &psmdbv1.PerconaServerMongoDBRestoreStatus{}
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:   psmdbv1.ConditionPBMAgentConfiguredForSnapshot,
			Status: metav1.ConditionTrue,
			Reason: "AlreadySet",
		})

		done, err := r.scaleDownStatefulSetsForSnapshotRestore(ctx, cluster, status)
		assert.NoError(t, err)
		assert.True(t, done)
	})

	t.Run("scales down statefulset and returns not done when ready replicas > 0", func(t *testing.T) {
		sfs := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      naming.MongodStatefulSetName(cluster, rs),
				Namespace: ns,
			},
			Spec: appsv1.StatefulSetSpec{
				Replicas: ptr.To(int32(3)),
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:           "mongod",
								LivenessProbe:  &corev1.Probe{},
								ReadinessProbe: &corev1.Probe{},
							},
						},
					},
				},
			},
			Status: appsv1.StatefulSetStatus{
				ReadyReplicas: 3,
			},
		}
		r := fakeReconciler(cluster, sfs)
		pbmName := "my-pbm-restore"
		status := &psmdbv1.PerconaServerMongoDBRestoreStatus{PBMname: pbmName}

		done, err := r.scaleDownStatefulSetsForSnapshotRestore(ctx, cluster, status)
		assert.NoError(t, err)
		assert.False(t, done)
		assert.False(t, apimeta.IsStatusConditionTrue(status.Conditions, psmdbv1.ConditionPBMAgentConfiguredForSnapshot))

		updated := &appsv1.StatefulSet{}
		err = r.client.Get(ctx, types.NamespacedName{Name: sfs.Name, Namespace: ns}, updated)
		require.NoError(t, err)
		require.NotNil(t, updated.Spec.Replicas)
		assert.Equal(t, int32(0), *updated.Spec.Replicas)
		assert.Equal(t, []string{"/opt/percona/pbm-agent"}, updated.Spec.Template.Spec.Containers[0].Command)
		assert.Equal(t, "restore-finish", updated.Spec.Template.Spec.Containers[0].Args[0])
		assert.Equal(t, pbmName, updated.Spec.Template.Spec.Containers[0].Args[1])
		assert.Nil(t, updated.Spec.Template.Spec.Containers[0].LivenessProbe)
		assert.Nil(t, updated.Spec.Template.Spec.Containers[0].ReadinessProbe)
	})

	t.Run("returns done and sets condition when all statefulsets at zero ready replicas", func(t *testing.T) {
		sfs := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      naming.MongodStatefulSetName(cluster, rs),
				Namespace: ns,
			},
			Spec: appsv1.StatefulSetSpec{
				Replicas: ptr.To(int32(0)),
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "mongod"}},
					},
				},
			},
			Status: appsv1.StatefulSetStatus{
				ReadyReplicas: 0,
			},
		}
		r := fakeReconciler(cluster, sfs)
		status := &psmdbv1.PerconaServerMongoDBRestoreStatus{PBMname: "my-pbm-restore"}

		done, err := r.scaleDownStatefulSetsForSnapshotRestore(ctx, cluster, status)
		assert.NoError(t, err)
		assert.True(t, done)
		assert.True(t, apimeta.IsStatusConditionTrue(status.Conditions, psmdbv1.ConditionPBMAgentConfiguredForSnapshot))
	})

	t.Run("includes nonvoting and hidden statefulsets when enabled", func(t *testing.T) {
		clusterWithExtra := &psmdbv1.PerconaServerMongoDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster-extra",
				Namespace: ns,
			},
			Spec: psmdbv1.PerconaServerMongoDBSpec{
				Replsets: []*psmdbv1.ReplsetSpec{
					{
						Name: "rs0",
						Size: 1,
						NonVoting: psmdbv1.NonVotingSpec{
							Enabled: true,
							Size:    1,
						},
						Hidden: psmdbv1.HiddenSpec{
							Enabled: true,
							Size:    1,
						},
					},
				},
			},
		}
		rsExtra := clusterWithExtra.Spec.Replsets[0]

		makeSFS := func(name string) *appsv1.StatefulSet {
			return &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: ns,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To(int32(0)),
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "mongod"}},
						},
					},
				},
				Status: appsv1.StatefulSetStatus{ReadyReplicas: 0},
			}
		}

		mongodSFS := makeSFS(naming.MongodStatefulSetName(clusterWithExtra, rsExtra))
		nvSFS := makeSFS(naming.NonVotingStatefulSetName(clusterWithExtra, rsExtra))
		hiddenSFS := makeSFS(naming.HiddenStatefulSetName(clusterWithExtra, rsExtra))

		r := fakeReconciler(clusterWithExtra, mongodSFS, nvSFS, hiddenSFS)
		status := &psmdbv1.PerconaServerMongoDBRestoreStatus{PBMname: "my-pbm-restore"}

		done, err := r.scaleDownStatefulSetsForSnapshotRestore(ctx, clusterWithExtra, status)
		assert.NoError(t, err)
		assert.True(t, done)
		assert.True(t, apimeta.IsStatusConditionTrue(status.Conditions, psmdbv1.ConditionPBMAgentConfiguredForSnapshot))
	})
}

func TestScaleUpStatefulSetsForSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	const ns = "default"

	cluster := &psmdbv1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: ns,
		},
		Spec: psmdbv1.PerconaServerMongoDBSpec{
			Replsets: []*psmdbv1.ReplsetSpec{
				{Name: "rs0", Size: 3},
			},
		},
	}
	rs := cluster.Spec.Replsets[0]

	t.Run("condition already set returns true immediately", func(t *testing.T) {
		r := fakeReconciler(cluster)
		status := &psmdbv1.PerconaServerMongoDBRestoreStatus{}
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:   psmdbv1.ConditionPBMAgentAwaitingRestoreFinish,
			Status: metav1.ConditionTrue,
			Reason: "AlreadySet",
		})

		done, err := r.scaleUpStatefulSetsForSnapshotRestore(ctx, cluster, status)
		assert.NoError(t, err)
		assert.True(t, done)
	})

	t.Run("scales up statefulset and returns not done when not yet ready", func(t *testing.T) {
		sfs := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      naming.MongodStatefulSetName(cluster, rs),
				Namespace: ns,
			},
			Spec: appsv1.StatefulSetSpec{
				Replicas: ptr.To(int32(0)),
			},
			Status: appsv1.StatefulSetStatus{
				ReadyReplicas: 0,
			},
		}
		r := fakeReconciler(cluster, sfs)
		status := &psmdbv1.PerconaServerMongoDBRestoreStatus{}

		done, err := r.scaleUpStatefulSetsForSnapshotRestore(ctx, cluster, status)
		assert.NoError(t, err)
		assert.False(t, done)
		assert.False(t, apimeta.IsStatusConditionTrue(status.Conditions, psmdbv1.ConditionPBMAgentAwaitingRestoreFinish))

		updated := &appsv1.StatefulSet{}
		err = r.client.Get(ctx, types.NamespacedName{Name: sfs.Name, Namespace: ns}, updated)
		require.NoError(t, err)
		require.NotNil(t, updated.Spec.Replicas)
		assert.Equal(t, rs.Size, *updated.Spec.Replicas)
	})

	t.Run("returns done and sets condition when all statefulsets ready", func(t *testing.T) {
		sfs := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      naming.MongodStatefulSetName(cluster, rs),
				Namespace: ns,
			},
			Spec: appsv1.StatefulSetSpec{
				Replicas: ptr.To(rs.Size),
			},
			Status: appsv1.StatefulSetStatus{
				ReadyReplicas: rs.Size,
			},
		}
		r := fakeReconciler(cluster, sfs)
		status := &psmdbv1.PerconaServerMongoDBRestoreStatus{}

		done, err := r.scaleUpStatefulSetsForSnapshotRestore(ctx, cluster, status)
		assert.NoError(t, err)
		assert.True(t, done)
		assert.True(t, apimeta.IsStatusConditionTrue(status.Conditions, psmdbv1.ConditionPBMAgentAwaitingRestoreFinish))
	})
}

func TestReconcilePVCForSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	const ns = "default"

	restore := &psmdbv1.PerconaServerMongoDBRestore{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-restore",
			Namespace: ns,
		},
	}

	spec := corev1.PersistentVolumeClaimSpec{
		AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
	}

	t.Run("creates PVC when not found and returns true", func(t *testing.T) {
		r := fakeReconciler(restore)

		done, err := r.reconcilePVCForSnapshotRestore(ctx, "my-pvc", "my-snapshot", spec, restore)
		assert.NoError(t, err)
		assert.True(t, done)

		pvc := &corev1.PersistentVolumeClaim{}
		err = r.client.Get(ctx, types.NamespacedName{Name: "my-pvc", Namespace: ns}, pvc)
		require.NoError(t, err)
		require.NotNil(t, pvc.Spec.DataSource)
		assert.Equal(t, "my-snapshot", pvc.Spec.DataSource.Name)
		assert.Equal(t, "VolumeSnapshot", pvc.Spec.DataSource.Kind)
		assert.Equal(t, "my-restore", pvc.Annotations[naming.AnnotationRestoreName])
	})

	t.Run("returns true when PVC has correct restore annotation", func(t *testing.T) {
		existingPVC := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-pvc",
				Namespace: ns,
				Annotations: map[string]string{
					naming.AnnotationRestoreName: "my-restore",
				},
			},
		}
		r := fakeReconciler(restore, existingPVC)

		done, err := r.reconcilePVCForSnapshotRestore(ctx, "my-pvc", "my-snapshot", spec, restore)
		assert.NoError(t, err)
		assert.True(t, done)
	})

	t.Run("deletes PVC with wrong restore annotation and returns false", func(t *testing.T) {
		existingPVC := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-pvc",
				Namespace: ns,
				Annotations: map[string]string{
					naming.AnnotationRestoreName: "other-restore",
				},
			},
		}
		r := fakeReconciler(restore, existingPVC)

		done, err := r.reconcilePVCForSnapshotRestore(ctx, "my-pvc", "my-snapshot", spec, restore)
		assert.NoError(t, err)
		assert.False(t, done)

		pvc := &corev1.PersistentVolumeClaim{}
		err = r.client.Get(ctx, types.NamespacedName{Name: "my-pvc", Namespace: ns}, pvc)
		assert.True(t, k8sErrors.IsNotFound(err), "expected PVC to be deleted")
	})

	t.Run("returns false without deleting when PVC has deletion timestamp", func(t *testing.T) {
		now := metav1.Now()
		existingPVC := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "my-pvc",
				Namespace:         ns,
				DeletionTimestamp: &now,
				Finalizers:        []string{"kubernetes.io/pvc-protection"},
				Annotations: map[string]string{
					naming.AnnotationRestoreName: "other-restore",
				},
			},
		}
		r := fakeReconciler(restore, existingPVC)

		done, err := r.reconcilePVCForSnapshotRestore(ctx, "my-pvc", "my-snapshot", spec, restore)
		assert.NoError(t, err)
		assert.False(t, done)

		// PVC should still exist (not deleted again)
		pvc := &corev1.PersistentVolumeClaim{}
		err = r.client.Get(ctx, types.NamespacedName{Name: "my-pvc", Namespace: ns}, pvc)
		assert.NoError(t, err)
	})
}

func TestReconcilePVCsForSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	const ns = "default"

	cluster := &psmdbv1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: ns,
		},
		Spec: psmdbv1.PerconaServerMongoDBSpec{
			Replsets: []*psmdbv1.ReplsetSpec{
				{Name: "rs0", Size: 2},
			},
		},
	}
	rs := cluster.Spec.Replsets[0]

	backup := &psmdbv1.PerconaServerMongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-backup",
			Namespace: ns,
		},
		Status: psmdbv1.PerconaServerMongoDBBackupStatus{
			Snapshots: psmdbv1.SnapshotInfos{
				{ReplsetName: "rs0", SnapshotName: "snapshot-rs0"},
			},
		},
	}

	sfs := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.MongodStatefulSetName(cluster, rs),
			Namespace: ns,
		},
		Spec: appsv1.StatefulSetSpec{
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: config.MongodDataVolClaimName,
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					},
				},
			},
		},
	}

	restore := &psmdbv1.PerconaServerMongoDBRestore{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-restore",
			Namespace: ns,
		},
	}

	t.Run("condition already set returns true immediately", func(t *testing.T) {
		r := fakeReconciler(cluster, sfs, restore, backup)
		status := &psmdbv1.PerconaServerMongoDBRestoreStatus{}
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:   psmdbv1.ConditionReplsetPVCsRestoredFromSnapshot,
			Status: metav1.ConditionTrue,
			Reason: "AlreadySet",
		})

		done, err := r.reconcilePVCsForSnapshotRestore(ctx, cluster, restore, backup, status)
		assert.NoError(t, err)
		assert.True(t, done)
	})

	t.Run("creates PVCs for all pods in replset and sets condition", func(t *testing.T) {
		r := fakeReconciler(cluster, sfs, restore, backup)
		status := &psmdbv1.PerconaServerMongoDBRestoreStatus{}

		done, err := r.reconcilePVCsForSnapshotRestore(ctx, cluster, restore, backup, status)
		assert.NoError(t, err)
		assert.True(t, done)
		assert.True(t, apimeta.IsStatusConditionTrue(status.Conditions, psmdbv1.ConditionReplsetPVCsRestoredFromSnapshot))

		for podIdx := 0; podIdx < int(rs.Size); podIdx++ {
			pvcName := config.MongodDataVolClaimName + "-" + rs.PodName(cluster, podIdx)
			pvc := &corev1.PersistentVolumeClaim{}
			err = r.client.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: ns}, pvc)
			require.NoError(t, err, "expected PVC %s to be created", pvcName)
			require.NotNil(t, pvc.Spec.DataSource)
			assert.Equal(t, "snapshot-rs0", pvc.Spec.DataSource.Name)
			assert.Equal(t, "my-restore", pvc.Annotations[naming.AnnotationRestoreName])
		}
	})

	t.Run("returns error when no snapshot exists for replset", func(t *testing.T) {
		backupNoSnapshot := &psmdbv1.PerconaServerMongoDBBackup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "backup-no-snapshot",
				Namespace: ns,
			},
			Status: psmdbv1.PerconaServerMongoDBBackupStatus{
				Snapshots: psmdbv1.SnapshotInfos{},
			},
		}
		r := fakeReconciler(cluster, sfs, restore, backupNoSnapshot)
		status := &psmdbv1.PerconaServerMongoDBRestoreStatus{}

		done, err := r.reconcilePVCsForSnapshotRestore(ctx, cluster, restore, backupNoSnapshot, status)
		assert.Error(t, err)
		assert.False(t, done)
		assert.Contains(t, err.Error(), "no snapshots found for replset rs0")
	})

	t.Run("creates PVCs for nonvoting pods when enabled", func(t *testing.T) {
		clusterWithNV := &psmdbv1.PerconaServerMongoDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster-nv",
				Namespace: ns,
			},
			Spec: psmdbv1.PerconaServerMongoDBSpec{
				Replsets: []*psmdbv1.ReplsetSpec{
					{
						Name: "rs0",
						Size: 1,
						NonVoting: psmdbv1.NonVotingSpec{
							Enabled: true,
							Size:    1,
						},
					},
				},
			},
		}
		rsNV := clusterWithNV.Spec.Replsets[0]

		backupNV := &psmdbv1.PerconaServerMongoDBBackup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "backup-nv",
				Namespace: ns,
			},
			Status: psmdbv1.PerconaServerMongoDBBackupStatus{
				Snapshots: psmdbv1.SnapshotInfos{
					{ReplsetName: "rs0", SnapshotName: "snapshot-rs0"},
				},
			},
		}

		mongodSFS := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      naming.MongodStatefulSetName(clusterWithNV, rsNV),
				Namespace: ns,
			},
			Spec: appsv1.StatefulSetSpec{
				VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
					{
						ObjectMeta: metav1.ObjectMeta{Name: config.MongodDataVolClaimName},
						Spec: corev1.PersistentVolumeClaimSpec{
							AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						},
					},
				},
			},
		}
		nvSFS := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      naming.NonVotingStatefulSetName(clusterWithNV, rsNV),
				Namespace: ns,
			},
			Spec: appsv1.StatefulSetSpec{
				VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
					{
						ObjectMeta: metav1.ObjectMeta{Name: config.MongodDataVolClaimName},
						Spec: corev1.PersistentVolumeClaimSpec{
							AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						},
					},
				},
			},
		}
		restoreNV := &psmdbv1.PerconaServerMongoDBRestore{
			ObjectMeta: metav1.ObjectMeta{Name: "restore-nv", Namespace: ns},
		}

		r := fakeReconciler(clusterWithNV, mongodSFS, nvSFS, restoreNV, backupNV)
		status := &psmdbv1.PerconaServerMongoDBRestoreStatus{}

		done, err := r.reconcilePVCsForSnapshotRestore(ctx, clusterWithNV, restoreNV, backupNV, status)
		assert.NoError(t, err)
		assert.True(t, done)

		// Verify a nonvoting PVC was created
		nvPVCName := config.MongodDataVolClaimName + "-" + naming.NonVotingPodName(clusterWithNV, rsNV, 0)
		nvPVC := &corev1.PersistentVolumeClaim{}
		err = r.client.Get(ctx, types.NamespacedName{Name: nvPVCName, Namespace: ns}, nvPVC)
		require.NoError(t, err, "expected nonvoting PVC to be created")
		assert.Equal(t, "snapshot-rs0", nvPVC.Spec.DataSource.Name)
	})
}

func TestDeleteStatefulSetsForSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	const ns = "default"

	cluster := &psmdbv1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: ns,
		},
		Spec: psmdbv1.PerconaServerMongoDBSpec{
			Replsets: []*psmdbv1.ReplsetSpec{
				{
					Name: "rs0",
					Size: 3,
					Arbiter: psmdbv1.Arbiter{
						Enabled: true,
						Size:    1,
					},
					NonVoting: psmdbv1.NonVotingSpec{
						Enabled: true,
						Size:    1,
					},
					Hidden: psmdbv1.HiddenSpec{
						Enabled: true,
						Size:    1,
					},
				},
			},
		},
	}
	rs := cluster.Spec.Replsets[0]

	t.Run("deletes all statefulsets including arbiter nonvoting and hidden", func(t *testing.T) {
		makeSFS := func(name string) *appsv1.StatefulSet {
			return &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: ns,
				},
			}
		}

		mongodSFS := makeSFS(naming.MongodStatefulSetName(cluster, rs))
		arbiterSFS := makeSFS(naming.ArbiterStatefulSetName(cluster, rs))
		nvSFS := makeSFS(naming.NonVotingStatefulSetName(cluster, rs))
		hiddenSFS := makeSFS(naming.HiddenStatefulSetName(cluster, rs))

		r := fakeReconciler(cluster, mongodSFS, arbiterSFS, nvSFS, hiddenSFS)

		err := r.deleteStatefulSetsForSnapshotRestore(ctx, cluster)
		assert.NoError(t, err)

		for _, sfsName := range []string{
			mongodSFS.Name, arbiterSFS.Name, nvSFS.Name, hiddenSFS.Name,
		} {
			got := &appsv1.StatefulSet{}
			err = r.client.Get(ctx, types.NamespacedName{Name: sfsName, Namespace: ns}, got)
			assert.True(t, k8sErrors.IsNotFound(err), "expected statefulset %s to be deleted", sfsName)
		}
	})

	t.Run("ignores not found statefulsets", func(t *testing.T) {
		// No statefulsets created, delete should not error
		r := fakeReconciler(cluster)

		err := r.deleteStatefulSetsForSnapshotRestore(ctx, cluster)
		assert.NoError(t, err)
	})
}

func TestReconcileExternalSnapshotRestoreStateNew(t *testing.T) {
	ctx := context.Background()

	restore := &psmdbv1.PerconaServerMongoDBRestore{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-restore",
			Namespace: "default",
		},
		Status: psmdbv1.PerconaServerMongoDBRestoreStatus{
			State: psmdbv1.RestoreStateNew,
		},
	}

	r := fakeReconciler()
	status, err := r.reconcileExternalSnapshotRestore(ctx, restore, nil, nil)
	assert.NoError(t, err)
	assert.Equal(t, psmdbv1.RestoreStateWaiting, status.State)
}

// Ensure the restore node address arg is built with DefaultMongoPort.
func TestScaleDownStatefulSetsNodeAddressArg(t *testing.T) {
	ctx := context.Background()
	const ns = "default"

	cluster := &psmdbv1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "cl", Namespace: ns},
		Spec: psmdbv1.PerconaServerMongoDBSpec{
			Replsets: []*psmdbv1.ReplsetSpec{{Name: "rs0", Size: 1}},
		},
	}
	rs := cluster.Spec.Replsets[0]
	pbmName := "pbm-op-123"

	sfs := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.MongodStatefulSetName(cluster, rs),
			Namespace: ns,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To(int32(1)),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "mongod"}},
				},
			},
		},
		Status: appsv1.StatefulSetStatus{ReadyReplicas: 1},
	}

	r := fakeReconciler(cluster, sfs)
	status := &psmdbv1.PerconaServerMongoDBRestoreStatus{PBMname: pbmName}

	_, err := r.scaleDownStatefulSetsForSnapshotRestore(ctx, cluster, status)
	require.NoError(t, err)

	updated := &appsv1.StatefulSet{}
	require.NoError(t, r.client.Get(ctx, types.NamespacedName{Name: sfs.Name, Namespace: ns}, updated))

	args := updated.Spec.Template.Spec.Containers[0].Args
	expectedNodeSuffix := fmt.Sprintf(":%d", psmdbv1.DefaultMongoPort)
	found := false
	for _, arg := range args {
		if len(arg) > len(expectedNodeSuffix) && arg[len(arg)-len(expectedNodeSuffix):] == expectedNodeSuffix {
			found = true
			break
		}
	}
	assert.True(t, found, "expected node address arg to end with port %d, args: %v", psmdbv1.DefaultMongoPort, args)
}
