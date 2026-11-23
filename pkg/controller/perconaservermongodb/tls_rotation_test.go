package perconaservermongodb

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	mongoFake "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo/fake"
)

type tlsRotationDial struct {
	host       string
	serverName string
}

// tlsRotationMongoProvider records the connections made to run
// rotateCertificates and fails them while connectErr is set.
type tlsRotationMongoProvider struct {
	MongoClientProvider
	connectErr error
	dials      []tlsRotationDial
}

func (p *tlsRotationMongoProvider) Standalone(_ context.Context, _ *api.PerconaServerMongoDB, _ api.SystemUserRole, host string, _ bool, opts ...mongo.ConfigOption) (mongo.Client, error) {
	conf := mongo.DialConfig{Config: mongo.Config{TLSConf: new(tls.Config)}}
	for _, opt := range opts {
		opt(&conf)
	}
	p.dials = append(p.dials, tlsRotationDial{host: host, serverName: conf.TLSConf.ServerName})
	if p.connectErr != nil {
		return nil, p.connectErr
	}
	return mongoFake.NewClient(), nil
}

func tlsRotationObjectMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      name,
		Namespace: "test-ns",
		Labels:    map[string]string{naming.LabelKubernetesInstance: "test-cluster"},
	}
}

func tlsRotationPod(name, subdomain, container, ip string, ready bool) *corev1.Pod {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Pod{
		ObjectMeta: tlsRotationObjectMeta(name),
		Spec: corev1.PodSpec{
			Subdomain: subdomain,
			Containers: []corev1.Container{
				{Name: container, Ports: []corev1.ContainerPort{{ContainerPort: 27017}}},
				{Name: naming.ContainerBackupAgent},
			},
		},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			PodIP:      ip,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}},
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: container, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			},
		},
	}
}

func TestRotateTLSInPlace(t *testing.T) {
	ctx := t.Context()

	cr := newTestCR()
	cr.Spec.ClusterServiceDNSSuffix = "svc.cluster.local"
	cr.Status.MongoVersion = "8.0.32-14"

	leafHash := func(cert string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(cert))) }
	wantLeaf := leafHash("leaf") + "/" + leafHash("internal-leaf")

	objs := []client.Object{
		cr,
		&corev1.Secret{ObjectMeta: tlsRotationObjectMeta("test-cluster-ssl"), Data: map[string][]byte{"tls.crt": []byte("leaf")}},
		&corev1.Secret{ObjectMeta: tlsRotationObjectMeta("test-cluster-ssl-internal"), Data: map[string][]byte{"tls.crt": []byte("internal-leaf")}},
		tlsRotationPod("test-cluster-rs0-0", "test-cluster-rs0", naming.ContainerMongod, "10.0.0.1", true),
		tlsRotationPod("test-cluster-rs0-nv-0", "test-cluster-rs0", naming.ContainerNonVoting, "10.0.0.2", true),
		tlsRotationPod("test-cluster-rs0-hidden-0", "test-cluster-rs0", naming.ContainerHidden, "10.0.0.3", true),
		tlsRotationPod("test-cluster-mongos-0", "", naming.ContainerMongos, "10.0.0.4", true),
		tlsRotationPod("test-cluster-rs0-arbiter-0", "test-cluster-rs0", naming.ContainerArbiter, "10.0.0.5", true),
		tlsRotationPod("test-cluster-rs0-1", "test-cluster-rs0", naming.ContainerMongod, "10.0.0.6", false),
		tlsRotationPod("test-cluster-rs0-2", "test-cluster-rs0", naming.ContainerMongod, "10.0.0.7", true),
		tlsRotationPod("test-cluster-rs0-3", "test-cluster-rs0", naming.ContainerMongod, "10.0.0.8", true),
	}

	// What the rebuild script reports per pod. The other pods rebuild their
	// PEM files on the first run and report it until the clean up.
	execOut := map[string]string{
		"test-cluster-rs0-2": tlsRotationNotSynced,
		"test-cluster-rs0-3": tlsRotationUpToDate, // started with the current certificates
	}
	var execs []string
	provider := &tlsRotationMongoProvider{connectErr: errors.New("connection refused")}

	r := buildFakeClient(objs...)
	r.mongoClientProvider = provider
	r.clientcmd = &mockClientCmd{
		execFunc: func(_ context.Context, pod *corev1.Pod, container string, cmd []string, _ io.Reader, stdout, _ io.Writer, _ bool) error {
			if cmd[0] == "rm" {
				assert.Equal(t, tlsRotationRebuilt, execOut[pod.Name], pod.Name)
				execOut[pod.Name] = tlsRotationUpToDate
				return nil
			}
			execs = append(execs, pod.Name+"/"+container)
			assert.Equal(t, []string{leafHash("leaf"), leafHash("internal-leaf")}, cmd[len(cmd)-2:])
			if _, ok := execOut[pod.Name]; !ok {
				execOut[pod.Name] = tlsRotationRebuilt
			}
			_, err := io.WriteString(stdout, execOut[pod.Name]+"\n")
			return err
		},
	}

	leafAnnotation := func(name string) string {
		pod := new(corev1.Pod)
		require.NoError(t, r.client.Get(ctx, types.NamespacedName{Namespace: cr.Namespace, Name: name}, pod))
		return pod.Annotations[naming.AnnotationSSLLeafHash]
	}
	rotated := []string{"test-cluster-mongos-0", "test-cluster-rs0-0", "test-cluster-rs0-hidden-0", "test-cluster-rs0-nv-0"}
	skipped := []string{"test-cluster-rs0-arbiter-0", "test-cluster-rs0-1", "test-cluster-rs0-2"}

	// PEM files are rebuilt, but mongod can't be told to reload them.
	require.Error(t, r.rotateTLSInPlace(ctx, cr))
	assert.ElementsMatch(t, []string{
		"test-cluster-mongos-0/mongos",
		"test-cluster-rs0-0/mongod",
		"test-cluster-rs0-hidden-0/mongod-hidden",
		"test-cluster-rs0-nv-0/mongod-nv",
		"test-cluster-rs0-2/mongod",
		"test-cluster-rs0-3/mongod",
	}, execs)
	assert.ElementsMatch(t, []tlsRotationDial{
		{host: "10.0.0.4:27017", serverName: "test-cluster-mongos.test-ns.svc.cluster.local"},
		{host: "10.0.0.1:27017", serverName: "test-cluster-rs0-0.test-cluster-rs0.test-ns.svc.cluster.local"},
		{host: "10.0.0.3:27017", serverName: "test-cluster-rs0-hidden-0.test-cluster-rs0.test-ns.svc.cluster.local"},
		{host: "10.0.0.2:27017", serverName: "test-cluster-rs0-nv-0.test-cluster-rs0.test-ns.svc.cluster.local"},
	}, provider.dials)
	for _, name := range append(rotated, skipped...) {
		assert.Empty(t, leafAnnotation(name), name)
	}
	assert.Equal(t, wantLeaf, leafAnnotation("test-cluster-rs0-3"))

	// The files are rebuilt already, the reload still has to be retried.
	provider.connectErr = nil
	provider.dials = nil
	require.NoError(t, r.rotateTLSInPlace(ctx, cr))
	assert.Len(t, provider.dials, len(rotated))
	for _, name := range rotated {
		assert.Equal(t, wantLeaf, leafAnnotation(name), name)
		assert.Equal(t, tlsRotationUpToDate, execOut[name], name)
	}
	for _, name := range skipped {
		assert.Empty(t, leafAnnotation(name), name)
	}

	// Pods that have loaded the certificates are left alone.
	execs, provider.dials = nil, nil
	require.NoError(t, r.rotateTLSInPlace(ctx, cr))
	assert.Equal(t, []string{"test-cluster-rs0-2/mongod"}, execs)
	assert.Empty(t, provider.dials)
}
