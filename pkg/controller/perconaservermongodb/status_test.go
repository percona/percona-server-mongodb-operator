package perconaservermongodb

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake" // nolint
	mcs "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
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

func TestUpdateStatus(t *testing.T) {
	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "psmdb-mock", Namespace: "psmdb"},
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: "1.12.0",
			Replsets:  []*api.ReplsetSpec{{Name: "rs0", Size: 3}, {Name: "rs1", Size: 3}},
			Sharding:  api.Sharding{Enabled: true, Mongos: &api.MongosSpec{Size: 3}},
			TLS: &api.TLSSpec{
				Mode: api.TLSModePrefer,
			},
		},
	}

	rs0 := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "psmdb-mock-rs0", Namespace: "psmdb"}}
	rs1 := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "psmdb-mock-rs1", Namespace: "psmdb"}}

	r := buildFakeClient(cr, rs0, rs1)

	if err := r.updateStatus(context.TODO(), cr, nil, api.AppStateInit); err != nil {
		t.Error(err)
	}

	if cr.Status.State != api.AppStateInit {
		t.Errorf("cr.Status.State got %#v, want %#v", cr.Status.State, api.AppStateInit)
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
