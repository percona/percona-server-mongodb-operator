package perconaservermongodb

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake" // nolint
	mcs "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	fakeBackup "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup/fake"
	faketls "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls/fake"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

// creates a fake client to mock API calls with the mock objects
func buildFakeClient(objs ...client.Object) *ReconcilePerconaServerMongoDB {
	s := scheme.Scheme

	s.AddKnownTypes(api.SchemeGroupVersion,
		new(api.PerconaServerMongoDB),
		new(api.PerconaServerMongoDBBackup),
		new(api.PerconaServerMongoDBBackupList),
		new(api.PerconaServerMongoDBRestore),
		new(api.PerconaServerMongoDBRestoreList),
		new(mcs.ServiceExport),
		new(mcs.ServiceExportList),
		new(mcs.ServiceImport),
		new(mcs.ServiceImportList),
	)

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).WithStatusSubresource(objs...).Build()

	return &ReconcilePerconaServerMongoDB{
		client:                 cl,
		scheme:                 s,
		lockers:                newLockStore(),
		newPBM:                 fakeBackup.NewPBM,
		newCertManagerCtrlFunc: faketls.NewCertManagerController,
	}
}

func mockReadyReplsetSts(name, namespace, crName, rsName, component string, replicas int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				naming.LabelKubernetesInstance:  crName,
				naming.LabelKubernetesReplset:   rsName,
				naming.LabelKubernetesComponent: component,
			},
		},
		Status: appsv1.StatefulSetStatus{
			ReadyReplicas:     replicas,
			UpdatedReplicas:   replicas,
			CurrentReplicas:   replicas,
			AvailableReplicas: replicas,
		},
	}
}

func mockReadyReplsetPod(name, namespace, crName, rsName, component string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				naming.LabelKubernetesName:      "percona-server-mongodb",
				naming.LabelKubernetesManagedBy: "percona-server-mongodb-operator",
				naming.LabelKubernetesPartOf:    "percona-server-mongodb",
				naming.LabelKubernetesInstance:  crName,
				naming.LabelKubernetesReplset:   rsName,
				naming.LabelKubernetesComponent: component,
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.ContainersReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
}

func TestUpdateStatus(t *testing.T) {
	tests := []struct {
		name          string
		cr            *api.PerconaServerMongoDB
		reconcileErr  error
		clusterState  api.AppState
		expectedState api.AppState
		runtimeObjs   []client.Object
	}{
		{
			name: "single replset-initializing",
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-name",
					Namespace: "some-namespace",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Replsets: []*api.ReplsetSpec{
						{
							Name: "rs0",
							Size: 3,
						},
					},
					Sharding: api.Sharding{
						Enabled: false,
					},
				},
			},
			clusterState:  api.AppStateInit,
			expectedState: api.AppStateInit,
		},
		{
			name: "single replset-reconcile error",
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-name",
					Namespace: "some-namespace",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Replsets: []*api.ReplsetSpec{
						{
							Name: "rs0",
							Size: 3,
						},
					},
					Sharding: api.Sharding{
						Enabled: false,
					},
				},
			},
			reconcileErr:  fmt.Errorf("test"),
			clusterState:  api.AppStateInit,
			expectedState: api.AppStateError,
		},
		{
			name: "single replset-ready",
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-name",
					Namespace: "some-namespace",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Replsets: []*api.ReplsetSpec{
						{
							Name: "rs0",
							Size: 3,
						},
					},
					Sharding: api.Sharding{
						Enabled: false,
					},
					Backup: api.BackupSpec{
						Enabled: false,
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					Host: "some-name-rs0.some-namespace",
				},
			},
			clusterState:  api.AppStateReady,
			expectedState: api.AppStateReady,
			runtimeObjs: []client.Object{
				mockReadyReplsetSts("some-name-rs0", "some-namespace", "some-name", "rs0", "mongod", 3),
				mockReadyReplsetPod("some-name-rs0-0", "some-namespace", "some-name", "rs0", "mongod"),
				mockReadyReplsetPod("some-name-rs0-1", "some-namespace", "some-name", "rs0", "mongod"),
				mockReadyReplsetPod("some-name-rs0-2", "some-namespace", "some-name", "rs0", "mongod"),
			},
		},
		{
			name: "single replset-backup version is empty",
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-name",
					Namespace: "some-namespace",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Replsets: []*api.ReplsetSpec{
						{
							Name: "rs0",
							Size: 3,
						},
					},
					Sharding: api.Sharding{
						Enabled: false,
					},
					Backup: api.BackupSpec{
						Enabled: true,
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					Host: "some-name-rs0.some-namespace",
				},
			},
			clusterState:  api.AppStateReady,
			expectedState: api.AppStateInit,
			runtimeObjs: []client.Object{
				mockReadyReplsetSts("some-name-rs0", "some-namespace", "some-name", "rs0", "mongod", 3),
				mockReadyReplsetPod("some-name-rs0-0", "some-namespace", "some-name", "rs0", "mongod"),
				mockReadyReplsetPod("some-name-rs0-1", "some-namespace", "some-name", "rs0", "mongod"),
				mockReadyReplsetPod("some-name-rs0-2", "some-namespace", "some-name", "rs0", "mongod"),
			},
		},
		{
			name: "single replset-no storages",
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-name",
					Namespace: "some-namespace",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Replsets: []*api.ReplsetSpec{
						{
							Name: "rs0",
							Size: 3,
						},
					},
					Sharding: api.Sharding{
						Enabled: false,
					},
					Backup: api.BackupSpec{
						Enabled: true,
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					Host:          "some-name-rs0.some-namespace",
					BackupVersion: "2.12.0",
				},
			},
			clusterState:  api.AppStateReady,
			expectedState: api.AppStateReady,
			runtimeObjs: []client.Object{
				mockReadyReplsetSts("some-name-rs0", "some-namespace", "some-name", "rs0", "mongod", 3),
				mockReadyReplsetPod("some-name-rs0-0", "some-namespace", "some-name", "rs0", "mongod"),
				mockReadyReplsetPod("some-name-rs0-1", "some-namespace", "some-name", "rs0", "mongod"),
				mockReadyReplsetPod("some-name-rs0-2", "some-namespace", "some-name", "rs0", "mongod"),
			},
		},
		{
			name: "single replset-backup config hash is empty",
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-name",
					Namespace: "some-namespace",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Replsets: []*api.ReplsetSpec{
						{
							Name: "rs0",
							Size: 3,
						},
					},
					Sharding: api.Sharding{
						Enabled: false,
					},
					Backup: api.BackupSpec{
						Enabled: true,
						Storages: map[string]api.BackupStorageSpec{
							"s3": api.BackupStorageSpec{
								S3: api.BackupStorageS3Spec{
									Bucket: "operator-testing",
								},
							},
						},
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					Host:          "some-name-rs0.some-namespace",
					BackupVersion: "2.12.0",
				},
			},
			clusterState:  api.AppStateReady,
			expectedState: api.AppStateInit,
			runtimeObjs: []client.Object{
				mockReadyReplsetSts("some-name-rs0", "some-namespace", "some-name", "rs0", "mongod", 3),
				mockReadyReplsetPod("some-name-rs0-0", "some-namespace", "some-name", "rs0", "mongod"),
				mockReadyReplsetPod("some-name-rs0-1", "some-namespace", "some-name", "rs0", "mongod"),
				mockReadyReplsetPod("some-name-rs0-2", "some-namespace", "some-name", "rs0", "mongod"),
			},
		},
		{
			name: "single replset-PBM is ready",
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-name",
					Namespace: "some-namespace",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Replsets: []*api.ReplsetSpec{
						{
							Name: "rs0",
							Size: 3,
						},
					},
					Sharding: api.Sharding{
						Enabled: false,
					},
					Backup: api.BackupSpec{
						Enabled: true,
						Storages: map[string]api.BackupStorageSpec{
							"s3": api.BackupStorageSpec{
								S3: api.BackupStorageS3Spec{
									Bucket: "operator-testing",
								},
							},
						},
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					Host:             "some-name-rs0.some-namespace",
					BackupVersion:    "2.12.0",
					BackupConfigHash: "9a8a1b4b11b0605c99c5f7575a5c83be0a2567115984ee0046b50f80a3503eb5",
				},
			},
			clusterState:  api.AppStateReady,
			expectedState: api.AppStateReady,
			runtimeObjs: []client.Object{
				mockReadyReplsetSts("some-name-rs0", "some-namespace", "some-name", "rs0", "mongod", 3),
				mockReadyReplsetPod("some-name-rs0-0", "some-namespace", "some-name", "rs0", "mongod"),
				mockReadyReplsetPod("some-name-rs0-1", "some-namespace", "some-name", "rs0", "mongod"),
				mockReadyReplsetPod("some-name-rs0-2", "some-namespace", "some-name", "rs0", "mongod"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := append([]client.Object{tt.cr}, tt.runtimeObjs...)
			r := buildFakeClient(objs...)
			err := r.updateStatus(t.Context(), tt.cr, tt.reconcileErr, tt.clusterState)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedState, tt.cr.Status.State)
		})
	}
}

func fakeVolumeSpec(t *testing.T) *api.VolumeSpec {
	q, err := resource.ParseQuantity("1Gi")
	if err != nil {
		t.Fatal(err)
	}
	return &api.VolumeSpec{
		PersistentVolumeClaim: api.PVCSpec{
			PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceStorage: q,
					},
				},
			},
		},
	}
}

func TestConnectionEndpoint(t *testing.T) {
	ctx := context.Background()
	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "psmdb-mock", Namespace: "psmdb"},
		Spec: api.PerconaServerMongoDBSpec{
			Image:     "some-image",
			CRVersion: version.Version(),
			Replsets: []*api.ReplsetSpec{
				{
					Name:       "rs0",
					Size:       3,
					VolumeSpec: fakeVolumeSpec(t),
				},
			},
			Sharding: api.Sharding{Enabled: false},
		},
	}

	tests := []struct {
		name        string
		cr          *api.PerconaServerMongoDB
		expected    string
		withoutPods bool
	}{
		{
			name:     "test connection endpoint",
			cr:       cr.DeepCopy(),
			expected: "psmdb-mock-rs0.psmdb.svc.cluster.local",
		},
		{
			name: "cluster-ip expose",
			cr: updateResource(cr.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Expose.Enabled = true
				cr.Spec.Replsets[0].Expose.ExposeType = corev1.ServiceTypeClusterIP
			}),
			expected: "psmdb-mock-rs0-0.psmdb-mock-rs0.psmdb.svc.cluster.local:27017,psmdb-mock-rs0-1.psmdb-mock-rs0.psmdb.svc.cluster.local:27017,psmdb-mock-rs0-2.psmdb-mock-rs0.psmdb.svc.cluster.local:27017",
		},
		{
			name: "load balancer expose without pods",
			cr: updateResource(cr.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Expose.Enabled = true
				cr.Spec.Replsets[0].Expose.ExposeType = corev1.ServiceTypeLoadBalancer
			}),
			withoutPods: true,
			expected:    "",
		},
		{
			name: "load balancer expose",
			cr: updateResource(cr.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Expose.Enabled = true
				cr.Spec.Replsets[0].Expose.ExposeType = corev1.ServiceTypeLoadBalancer
			}),
			expected: "some-ip:0,some-ip:0,some-ip:0",
		},
		{
			name: "load balancer expose with multicluster",
			cr: updateResource(cr.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Expose.Enabled = true
				cr.Spec.Replsets[0].Expose.ExposeType = corev1.ServiceTypeLoadBalancer
				cr.Spec.MultiCluster.Enabled = true
				cr.Spec.MultiCluster.DNSSuffix = "some-dns-suffix"
			}),
			expected: "psmdb-mock-rs0-0.psmdb.some-dns-suffix:27017,psmdb-mock-rs0-1.psmdb.some-dns-suffix:27017,psmdb-mock-rs0-2.psmdb.some-dns-suffix:27017",
		},
		{
			name: "load balancer expose for sharding",
			cr: updateResource(cr.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Sharding.Enabled = true
				cr.Spec.Sharding.Mongos = &api.MongosSpec{
					Size: 3,
					Expose: api.MongosExpose{
						Expose: api.Expose{
							ExposeType: corev1.ServiceTypeLoadBalancer,
						},
					},
				}
				cr.Spec.Sharding.ConfigsvrReplSet = &api.ReplsetSpec{
					Size:       3,
					VolumeSpec: fakeVolumeSpec(t),
				}
			}),
			expected: "mongos-ip",
		},
		{
			name: "cluster ip expose for sharding",
			cr: updateResource(cr.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Sharding.Enabled = true
				cr.Spec.Sharding.Mongos = &api.MongosSpec{
					Size: 3,
					Expose: api.MongosExpose{
						Expose: api.Expose{
							ExposeType: corev1.ServiceTypeClusterIP,
						},
					},
				}
				cr.Spec.Sharding.ConfigsvrReplSet = &api.ReplsetSpec{
					Size:       3,
					VolumeSpec: fakeVolumeSpec(t),
				}
			}),
			expected: "psmdb-mock-mongos.psmdb.svc.cluster.local:27017",
		},
		{
			name: "loadbalancer expose for sharding with service per pod",
			cr: updateResource(cr.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Sharding.Enabled = true
				cr.Spec.Sharding.Mongos = &api.MongosSpec{
					Size: 3,
					Expose: api.MongosExpose{
						ServicePerPod: true,
						Expose: api.Expose{
							ExposeType: corev1.ServiceTypeLoadBalancer,
						},
					},
				}
				cr.Spec.Sharding.ConfigsvrReplSet = &api.ReplsetSpec{
					Size:       3,
					VolumeSpec: fakeVolumeSpec(t),
				}
			}),
			expected: "mongos-ip,mongos-ip,mongos-ip",
		},
		{
			name: "cluster ip expose for sharding with service per pod",
			cr: updateResource(cr.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Sharding.Enabled = true
				cr.Spec.Sharding.Mongos = &api.MongosSpec{
					Size: 3,
					Expose: api.MongosExpose{
						ServicePerPod: true,
						Expose: api.Expose{
							ExposeType: corev1.ServiceTypeClusterIP,
						},
					},
				}
				cr.Spec.Sharding.ConfigsvrReplSet = &api.ReplsetSpec{
					Size:       3,
					VolumeSpec: fakeVolumeSpec(t),
				}
			}),
			expected: "psmdb-mock-mongos-0.psmdb.svc.cluster.local:27017,psmdb-mock-mongos-1.psmdb.svc.cluster.local:27017,psmdb-mock-mongos-2.psmdb.svc.cluster.local:27017",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := tt.cr
			if err := cr.CheckNSetDefaults(ctx, version.PlatformKubernetes); err != nil {
				t.Fatal(err)
			}
			obj := []client.Object{cr}

			if !tt.withoutPods {
				pods := fakePodsForRS(cr, cr.Spec.Replsets[0])
				obj = append(obj, pods...)
				if cr.Spec.Replsets[0].Expose.Enabled {
					services := []client.Object{}
					for _, pod := range pods {
						services = append(services, fakeSvc(pod.GetName(), pod.GetNamespace(), cr.Spec.Replsets[0].Expose.ExposeType, "some-ip", "some-hostname"))
					}
					obj = append(obj, services...)
					if cr.Spec.MultiCluster.Enabled {
						for _, svc := range services {
							obj = append(obj, &mcs.ServiceImport{
								ObjectMeta: metav1.ObjectMeta{
									Name:      svc.GetName(),
									Namespace: svc.GetNamespace(),
								},
							})
						}
					}
				}
			}

			if cr.Spec.Sharding.Enabled {
				pods := fakePodsForMongos(cr)
				obj = append(obj, pods...)
				if cr.Spec.Sharding.Mongos.Expose.ServicePerPod {
					for _, pod := range pods {
						obj = append(obj, fakeSvc(pod.GetName(), pod.GetNamespace(), cr.Spec.Sharding.Mongos.Expose.ExposeType, "mongos-ip", "mongos-hostname"))
					}
				} else {
					obj = append(obj, fakeSvc(cr.Name+"-mongos", cr.Namespace, corev1.ServiceTypeLoadBalancer, "mongos-ip", "mongos-hostname"))
				}
			}

			r := buildFakeClient(obj...)
			endpoint, err := r.connectionEndpoint(ctx, cr)
			if err != nil {
				t.Fatal(err)
			}
			if endpoint != tt.expected {
				t.Fatalf("connectionEndpoint got %s, want %s", endpoint, tt.expected)
			}
		})
	}
}

func fakeSvc(name, namespace string, svcType corev1.ServiceType, ip, hostname string) *corev1.Service {
	ingress := []corev1.LoadBalancerIngress{
		{
			IP:       ip,
			Hostname: hostname,
		},
	}
	clusterIP := ""
	if svcType == corev1.ServiceTypeClusterIP {
		clusterIP = ip
		ingress = nil
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Type:      svcType,
			ClusterIP: clusterIP,
			Ports: []corev1.ServicePort{
				{
					Name: "mongos",
					Port: 27017,
				},
			},
		},
		Status: corev1.ServiceStatus{
			LoadBalancer: corev1.LoadBalancerStatus{
				Ingress: ingress,
			},
		},
	}
}

func TestIsAwaitingSmartUpdate(t *testing.T) {
	ctx := t.Context()
	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "psmdb-mock",
			Namespace:  "psmdb",
			Generation: 1,
		},
		Spec: api.PerconaServerMongoDBSpec{
			Backup: api.BackupSpec{
				Enabled: false,
			},
			CRVersion: version.Version(),
			Image:     "percona/percona-server-mongodb:latest",
			Replsets: []*api.ReplsetSpec{
				{
					Name:       "rs0",
					Size:       3,
					VolumeSpec: fakeVolumeSpec(t),
				},
			},
			UpdateStrategy: api.SmartUpdateStatefulSetStrategyType,
			UpgradeOptions: api.UpgradeOptions{
				SetFCV: true,
			},
			Sharding: api.Sharding{Enabled: false},
		},
		Status: api.PerconaServerMongoDBStatus{
			MongoVersion:       "4.2",
			ObservedGeneration: 1,
			State:              api.AppStateReady,
			MongoImage:         "percona/percona-server-mongodb:4.0",
		},
	}
	sts := fakeStatefulset(cr, cr.Spec.Replsets[0], cr.Spec.Replsets[0].Size, "some-revision", "mongod")
	pods := fakePodsForRS(cr, cr.Spec.Replsets[0])

	testCases := []struct {
		desc     string
		expected bool
		mock     func(cl client.Client) error
		cluster  *api.PerconaServerMongoDB
	}{
		{
			desc:     "smart update is disabled",
			expected: false,
			cluster: updateResource(cr.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Spec.UpdateStrategy = ""
			}),
		},
		{
			desc:     "statefulset has not changed",
			expected: false,
			mock: func(cl client.Client) error {
				for _, pod := range pods {
					labels := pod.GetLabels()
					labels["controller-revision-hash"] = "some-revision"
					pod.SetLabels(labels)
					if err := cl.Update(ctx, pod); err != nil {
						return err
					}
				}
				return nil
			},
			cluster: cr.DeepCopy(),
		},
		{
			desc:     "statefulset has changed, no pods updated",
			expected: true,
			mock: func(cl client.Client) error {
				for _, pod := range pods {
					labels := pod.GetLabels()
					labels["controller-revision-hash"] = "previous-revision"
					pod.SetLabels(labels)
					if err := cl.Update(ctx, pod); err != nil {
						return err
					}
				}
				fakeSts := sts.DeepCopyObject().(*appsv1.StatefulSet)
				fakeSts.Status.UpdatedReplicas = 0
				return cl.Status().Update(ctx, fakeSts)
			},
			cluster: cr.DeepCopy(),
		},
		{
			desc:     "statefulset has changed, some pods updated",
			expected: false,
			mock: func(cl client.Client) error {
				outdatedPod := pods[2]

				labels := outdatedPod.GetLabels()
				labels["controller-revision-hash"] = "previous-revision"
				outdatedPod.SetLabels(labels)
				if err := cl.Update(ctx, outdatedPod); err != nil {
					return err
				}
				fakeSts := sts.DeepCopyObject().(*appsv1.StatefulSet)
				fakeSts.Status.UpdatedReplicas = 2
				return cl.Status().Update(ctx, fakeSts)
			},
			cluster: cr.DeepCopy(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			// Setup mocks
			objs := []client.Object{}
			objs = append(objs, tc.cluster, sts)
			objs = append(objs, pods...)
			r := buildFakeClient(objs...)
			if tc.mock != nil {
				err := tc.mock(r.client)
				assert.NoError(t, err)
			}

			actual, err := r.isAwaitingSmartUpdate(ctx, tc.cluster)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, actual)
		})

	}
}
