package psmdb

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	mcsv1alpha1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func TestMongoConfigURIFromReplsetAddrs(t *testing.T) {
	tests := map[string]struct {
		dnsMode         api.DNSMode
		rsExposed       bool
		mcsEnabled      bool
		serviceImported bool
		replsetSize     int32
		expectedURI     string
		expectedSRVURI  string
	}{
		"service mesh": {
			dnsMode:        api.DNSModeServiceMesh,
			expectedURI:    "mongodb://app-user:p%40ss%2Fword@cluster-rs0-0.database.svc.cluster.local:27017/?authSource=application&replicaSet=rs0",
			expectedSRVURI: "mongodb+srv://app-user:p%40ss%2Fword@cluster-rs0.database.svc.cluster.local/?authSource=application&replicaSet=rs0",
		},
		"internal": {
			dnsMode:        api.DNSModeInternal,
			expectedURI:    "mongodb://app-user:p%40ss%2Fword@cluster-rs0-0.cluster-rs0.database.svc.cluster.local:27017/?authSource=application&replicaSet=rs0",
			expectedSRVURI: "mongodb+srv://app-user:p%40ss%2Fword@cluster-rs0.database.svc.cluster.local/?authSource=application&replicaSet=rs0",
		},
		"internal multiple hosts": {
			dnsMode:        api.DNSModeInternal,
			replsetSize:    3,
			expectedURI:    "mongodb://app-user:p%40ss%2Fword@cluster-rs0-0.cluster-rs0.database.svc.cluster.local:27017,cluster-rs0-1.cluster-rs0.database.svc.cluster.local:27017,cluster-rs0-2.cluster-rs0.database.svc.cluster.local:27017/?authSource=application&replicaSet=rs0",
			expectedSRVURI: "mongodb+srv://app-user:p%40ss%2Fword@cluster-rs0.database.svc.cluster.local/?authSource=application&replicaSet=rs0",
		},
		"internal MCS service not imported": {
			dnsMode:        api.DNSModeInternal,
			rsExposed:      true,
			mcsEnabled:     true,
			expectedURI:    "mongodb://app-user:p%40ss%2Fword@cluster-rs0-0.cluster-rs0.database.svc.cluster.local:27017/?authSource=application&replicaSet=rs0",
			expectedSRVURI: "mongodb+srv://app-user:p%40ss%2Fword@cluster-rs0.database.svc.cluster.local/?authSource=application&replicaSet=rs0",
		},
		"internal MCS service imported": {
			dnsMode:         api.DNSModeInternal,
			rsExposed:       true,
			mcsEnabled:      true,
			serviceImported: true,
			expectedURI:     "mongodb://app-user:p%40ss%2Fword@cluster-rs0-0.database.svc.clusterset.local:27017/?authSource=application&replicaSet=rs0",
			expectedSRVURI:  "mongodb+srv://app-user:p%40ss%2Fword@cluster-rs0.database.svc.cluster.local/?authSource=application&replicaSet=rs0",
		},
		"external not exposed": {
			dnsMode:        api.DNSModeExternal,
			expectedURI:    "mongodb://app-user:p%40ss%2Fword@cluster-rs0-0.cluster-rs0.database.svc.cluster.local:27017/?authSource=application&replicaSet=rs0",
			expectedSRVURI: "mongodb+srv://app-user:p%40ss%2Fword@cluster-rs0.database.svc.cluster.local/?authSource=application&replicaSet=rs0",
		},
		"external exposed": {
			dnsMode:        api.DNSModeExternal,
			rsExposed:      true,
			expectedURI:    "mongodb://app-user:p%40ss%2Fword@10.0.0.10:27017/?authSource=application&replicaSet=rs0",
			expectedSRVURI: "mongodb+srv://app-user:p%40ss%2Fword@cluster-rs0.database.svc.cluster.local/?authSource=application&replicaSet=rs0",
		},
		"external MCS service not imported": {
			dnsMode:        api.DNSModeExternal,
			rsExposed:      true,
			mcsEnabled:     true,
			expectedURI:    "mongodb://app-user:p%40ss%2Fword@10.0.0.10:27017/?authSource=application&replicaSet=rs0",
			expectedSRVURI: "mongodb+srv://app-user:p%40ss%2Fword@cluster-rs0.database.svc.cluster.local/?authSource=application&replicaSet=rs0",
		},
		"external MCS service imported": {
			dnsMode:         api.DNSModeExternal,
			rsExposed:       true,
			mcsEnabled:      true,
			serviceImported: true,
			expectedURI:     "mongodb://app-user:p%40ss%2Fword@cluster-rs0-0.database.svc.clusterset.local:27017/?authSource=application&replicaSet=rs0",
			expectedSRVURI:  "mongodb+srv://app-user:p%40ss%2Fword@cluster-rs0.database.svc.cluster.local/?authSource=application&replicaSet=rs0",
		},
		"default": {
			dnsMode:        api.DNSMode("unsupported"),
			expectedURI:    "mongodb://app-user:p%40ss%2Fword@cluster-rs0-0.cluster-rs0.database.svc.cluster.local:27017/?authSource=application&replicaSet=rs0",
			expectedSRVURI: "mongodb+srv://app-user:p%40ss%2Fword@cluster-rs0.database.svc.cluster.local/?authSource=application&replicaSet=rs0",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := clientTestCluster()
			cr.Spec.ClusterServiceDNSMode = tt.dnsMode
			cr.Spec.MultiCluster = api.MultiCluster{
				Enabled:   tt.mcsEnabled,
				DNSSuffix: "svc.clusterset.local",
			}
			rs := cr.Spec.Replsets[0]
			if tt.replsetSize > 0 {
				rs.Size = tt.replsetSize
			}

			stsLabels := naming.RSLabels(cr, rs)
			stsLabels[naming.LabelKubernetesComponent] = naming.ComponentMongod
			objects := []client.Object{
				&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "cluster-rs0",
						Namespace: cr.Namespace,
						Labels:    stsLabels,
					},
				},
			}
			for i := range rs.Size {
				pod := clientTestPod(cr, fmt.Sprintf("cluster-rs0-%d", i), naming.RSLabels(cr, rs))
				pod.Labels[naming.LabelKubernetesComponent] = naming.ComponentMongod
				objects = append(objects, pod, &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pod.Name,
						Namespace: cr.Namespace,
					},
					Spec: corev1.ServiceSpec{
						Type:      corev1.ServiceTypeClusterIP,
						ClusterIP: fmt.Sprintf("10.0.0.%d", 10+i),
						Ports: []corev1.ServicePort{
							{Name: "mongodb", Port: 27017},
						},
					},
				})
				if tt.serviceImported {
					objects = append(objects, &mcsv1alpha1.ServiceImport{
						ObjectMeta: metav1.ObjectMeta{
							Name:      pod.Name,
							Namespace: cr.Namespace,
						},
					})
				}
			}

			cl := fake.NewClientBuilder().
				WithScheme(clientTestScheme(t)).
				WithObjects(objects...).
				Build()

			cfg, err := MongoConfig(
				t.Context(),
				cl,
				cr,
				tt.dnsMode,
				rs,
				clientTestCredentials(),
				tt.rsExposed,
			)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedURI, cfg.URI())
			assert.Equal(t, tt.expectedSRVURI, cfg.SRVURI("cluster-rs0.database.svc.cluster.local"))
		})
	}
}

func TestMongoConfigReturnsServiceImportLookupError(t *testing.T) {
	for _, dnsMode := range []api.DNSMode{api.DNSModeInternal, api.DNSModeExternal} {
		t.Run(string(dnsMode), func(t *testing.T) {
			cr := clientTestCluster()
			cr.Spec.ClusterServiceDNSMode = dnsMode
			cr.Spec.MultiCluster.Enabled = true
			rs := cr.Spec.Replsets[0]
			pod := clientTestPod(cr, "cluster-rs0-0", naming.RSLabels(cr, rs))
			pod.Labels[naming.LabelKubernetesComponent] = naming.ComponentMongod

			stsLabels := naming.RSLabels(cr, rs)
			stsLabels[naming.LabelKubernetesComponent] = naming.ComponentMongod
			cl := fake.NewClientBuilder().
				WithScheme(clientTestScheme(t)).
				WithObjects(
					&appsv1.StatefulSet{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "cluster-rs0",
							Namespace: cr.Namespace,
							Labels:    stsLabels,
						},
					},
					pod,
				).
				Build()

			_, err := MongoConfig(
				t.Context(),
				serviceImportErrorClient{Client: cl},
				cr,
				dnsMode,
				rs,
				clientTestCredentials(),
				true,
			)
			require.EqualError(t, err,
				"get replset addr: failed to get external hostname for pod cluster-rs0-0: "+
					"check if service imported for cluster-rs0-0: service import lookup failed",
			)
		})
	}
}

func TestMongosConfigURIFromMongosAddrs(t *testing.T) {
	tests := map[string]struct {
		servicePerPod   bool
		exposeType      corev1.ServiceType
		useInternalAddr bool
		objects         func(*api.PerconaServerMongoDB) []client.Object
		expectedURI     string
	}{
		"shared service": {
			useInternalAddr: true,
			objects: func(cr *api.PerconaServerMongoDB) []client.Object {
				return []client.Object{clientTestMongosService(cr, naming.MongosServiceName(cr))}
			},
			expectedURI: "mongodb://app-user:p%40ss%2Fword@cluster-mongos.database.svc.cluster.local:27017/?authSource=application",
		},
		"service per pod": {
			servicePerPod:   true,
			useInternalAddr: true,
			objects: func(cr *api.PerconaServerMongoDB) []client.Object {
				labels := naming.MongosLabels(cr)
				return []client.Object{
					clientTestPod(cr, "cluster-mongos-0", labels),
					clientTestPod(cr, "cluster-mongos-1", labels),
					clientTestMongosService(cr, "cluster-mongos-0"),
					clientTestMongosService(cr, "cluster-mongos-1"),
				}
			},
			expectedURI: "mongodb://app-user:p%40ss%2Fword@cluster-mongos-0.database.svc.cluster.local:27017,cluster-mongos-1.database.svc.cluster.local:27017/?authSource=application",
		},
		"load balancer internal address": {
			exposeType:      corev1.ServiceTypeLoadBalancer,
			useInternalAddr: true,
			objects: func(cr *api.PerconaServerMongoDB) []client.Object {
				svc := clientTestMongosService(cr, naming.MongosServiceName(cr))
				svc.Spec.Type = corev1.ServiceTypeLoadBalancer
				svc.Spec.ClusterIP = "10.0.0.20"
				return []client.Object{svc}
			},
			expectedURI: "mongodb://app-user:p%40ss%2Fword@10.0.0.20/?authSource=application",
		},
		"load balancer external address": {
			exposeType: corev1.ServiceTypeLoadBalancer,
			objects: func(cr *api.PerconaServerMongoDB) []client.Object {
				svc := clientTestMongosService(cr, naming.MongosServiceName(cr))
				svc.Spec.Type = corev1.ServiceTypeLoadBalancer
				svc.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{
					{Hostname: "mongos.example.com"},
				}
				return []client.Object{svc}
			},
			expectedURI: "mongodb://app-user:p%40ss%2Fword@mongos.example.com/?authSource=application",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := clientTestCluster()
			cr.Spec.Sharding = api.Sharding{
				Enabled: true,
				Mongos: &api.MongosSpec{
					Expose: api.MongosExpose{
						ServicePerPod: tt.servicePerPod,
						Expose: api.Expose{
							ExposeType: tt.exposeType,
						},
					},
				},
			}
			cl := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(tt.objects(cr)...).
				Build()

			cfg, err := MongosConfig(
				t.Context(),
				cl,
				cr,
				clientTestCredentials(),
				tt.useInternalAddr,
				tt.servicePerPod,
			)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedURI, cfg.URI())
		})
	}
}

func clientTestCluster() *api.PerconaServerMongoDB {
	return &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster",
			Namespace: "database",
		},
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion:               "1.20.0",
			ClusterServiceDNSMode:   api.DNSModeInternal,
			ClusterServiceDNSSuffix: "svc.cluster.local",
			TLS:                     &api.TLSSpec{Mode: api.TLSModeDisabled},
			Replsets: []*api.ReplsetSpec{
				{
					Name: "rs0",
					Size: 1,
				},
			},
		},
	}
}

func clientTestCredentials() Credentials {
	return Credentials{
		Username:   "app-user",
		Password:   "p@ss/word",
		AuthSource: "application",
	}
}

func clientTestPod(cr *api.PerconaServerMongoDB, name string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cr.Namespace,
			Labels:    labels,
		},
	}
}

func clientTestMongosService(cr *api.PerconaServerMongoDB, name string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cr.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "mongos", Port: 27017},
			},
		},
	}
}

func clientTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, mcsv1alpha1.Install(s))
	return s
}

type serviceImportErrorClient struct {
	client.Client
}

func (c serviceImportErrorClient) Get(
	ctx context.Context,
	key types.NamespacedName,
	obj client.Object,
	opts ...client.GetOption,
) error {
	if _, ok := obj.(*mcsv1alpha1.ServiceImport); ok {
		return errors.New("service import lookup failed")
	}
	return c.Client.Get(ctx, key, obj, opts...)
}
