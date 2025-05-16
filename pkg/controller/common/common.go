package common

import (
	"context"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

func New(client client.Client, scheme *runtime.Scheme, newPBMFunc backup.NewPBMFunc, mongoClientProvider psmdb.MongoClientProvider) CommonReconciler {
	return CommonReconciler{
		client:              client,
		scheme:              scheme,
		newPBMFunc:          newPBMFunc,
		mongoClientProvider: mongoClientProvider,
	}
}

type CommonReconciler struct {
	client              client.Client
	scheme              *runtime.Scheme
	newPBMFunc          backup.NewPBMFunc
	mongoClientProvider psmdb.MongoClientProvider
}

func (r *CommonReconciler) Client() client.Client {
	return r.client
}

func (r *CommonReconciler) Scheme() *runtime.Scheme {
	return r.scheme
}

func (r *CommonReconciler) NewPBM(ctx context.Context, cluster *api.PerconaServerMongoDB) (backup.PBM, error) {
	return r.newPBMFunc(ctx, r.client, cluster)
}

func (r *CommonReconciler) NewPBMFunc() backup.NewPBMFunc {
	return r.newPBMFunc
}

func (r *CommonReconciler) getMongoClientProvider() psmdb.MongoClientProvider {
	if r.mongoClientProvider == nil {
		return psmdb.NewProvider(r.client)
	}
	return r.mongoClientProvider
}

func (r *CommonReconciler) MongoClientWithRole(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole) (mongo.Client, error) {
	return r.getMongoClientProvider().Mongo(ctx, cr, rs, role)
}

func (r *CommonReconciler) MongosClientWithRole(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole) (mongo.Client, error) {
	return r.getMongoClientProvider().Mongos(ctx, cr, role)
}

func (r *CommonReconciler) StandaloneClientWithRole(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole, pod corev1.Pod) (mongo.Client, error) {
	host, err := psmdb.MongoHost(ctx, r.client, cr, cr.Spec.ClusterServiceDNSMode, rs, rs.Expose.Enabled, pod)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get mongo host")
	}
	return r.getMongoClientProvider().Standalone(ctx, cr, role, host, cr.TLSEnabled())
}

/*
// ReconcilePerconaServerMongoDB reconciles a PerconaServerMongoDB object
type ReconcilePerconaServerMongoDB struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client     client.Client
	scheme     *runtime.Scheme
	restConfig *rest.Config

	crons               CronRegistry
	clientcmd           *clientcmd.Client
	serverVersion       *version.ServerVersion
	reconcileIn         time.Duration
	mongoClientProvider psmdb.MongoClientProvider

	newCertManagerCtrlFunc tls.NewCertManagerControllerFunc

	newPBM backup.NewPBMFunc

	initImage string

	lockers lockStore
}

// ReconcilePerconaServerMongoDBRestore reconciles a PerconaServerMongoDBRestore object
type ReconcilePerconaServerMongoDBRestore struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client    client.Client
	scheme    *runtime.Scheme
	clientcmd *clientcmd.Client

	newPBMFunc backup.NewPBMFunc
}
// ReconcilePerconaServerMongoDBBackup reconciles a PerconaServerMongoDBBackup object
type ReconcilePerconaServerMongoDBBackup struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client    client.Client
	scheme    *runtime.Scheme
	clientcmd *clientcmd.Client

	newPBMFunc backup.NewPBMFunc
}
*/
