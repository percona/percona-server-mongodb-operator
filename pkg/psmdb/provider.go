package psmdb

import (
	"context"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

type MongoClientProvider interface {
	Mongo(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole) (mongo.Client, error)
	Mongos(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole) (mongo.Client, error)
	Standalone(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole, pod corev1.Pod) (mongo.Client, error)
}

type mongoClientProvider struct {
	k8sclient client.Client
}

func (p *mongoClientProvider) Mongo(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole) (mongo.Client, error) {
	c, err := GetCredentials(ctx, p.k8sclient, cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	return MongoClient(ctx, p.k8sclient, cr, rs, c)
}

func (p *mongoClientProvider) Mongos(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole) (mongo.Client, error) {
	c, err := GetCredentials(ctx, p.k8sclient, cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	return mongosClient(ctx, p.k8sclient, cr, c)
}

func (p *mongoClientProvider) Standalone(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole, pod corev1.Pod) (mongo.Client, error) {
	c, err := GetCredentials(ctx, p.k8sclient, cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}
	host, err := MongoHost(ctx, p.k8sclient, cr, cr.Spec.ClusterServiceDNSMode, rs, rs.Expose.Enabled, pod)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get mongo host")
	}

	return standaloneClient(ctx, p.k8sclient, cr, c, host, cr.TLSEnabled())
}

type MongoProviderBase struct {
	cl client.Client

	provider MongoClientProvider
}

func NewProviderBase(cl client.Client, provider MongoClientProvider) MongoProviderBase {
	return MongoProviderBase{
		cl:       cl,
		provider: provider,
	}
}

func (provider *MongoProviderBase) MongoClient() MongoClientProvider {
	if provider.provider == nil {
		return &mongoClientProvider{k8sclient: provider.cl}
	}
	return provider.provider
}

func getUserSecret(ctx context.Context, cl client.Reader, cr *api.PerconaServerMongoDB, name string) (corev1.Secret, error) {
	secrets := corev1.Secret{}
	err := cl.Get(ctx, types.NamespacedName{Name: name, Namespace: cr.Namespace}, &secrets)
	return secrets, errors.Wrap(err, "get user secrets")
}

func GetCredentials(ctx context.Context, cl client.Reader, cr *api.PerconaServerMongoDB, role api.SystemUserRole) (Credentials, error) {
	creds := Credentials{}
	usersSecret, err := getUserSecret(ctx, cl, cr, api.UserSecretName(cr))
	if err != nil {
		return creds, errors.Wrap(err, "failed to get user secret")
	}

	switch role {
	case api.RoleDatabaseAdmin:
		creds.Username = string(usersSecret.Data[api.EnvMongoDBDatabaseAdminUser])
		creds.Password = string(usersSecret.Data[api.EnvMongoDBDatabaseAdminPassword])
	case api.RoleClusterAdmin:
		creds.Username = string(usersSecret.Data[api.EnvMongoDBClusterAdminUser])
		creds.Password = string(usersSecret.Data[api.EnvMongoDBClusterAdminPassword])
	case api.RoleUserAdmin:
		creds.Username = string(usersSecret.Data[api.EnvMongoDBUserAdminUser])
		creds.Password = string(usersSecret.Data[api.EnvMongoDBUserAdminPassword])
	case api.RoleClusterMonitor:
		creds.Username = string(usersSecret.Data[api.EnvMongoDBClusterMonitorUser])
		creds.Password = string(usersSecret.Data[api.EnvMongoDBClusterMonitorPassword])
	case api.RoleBackup:
		creds.Username = string(usersSecret.Data[api.EnvMongoDBBackupUser])
		creds.Password = string(usersSecret.Data[api.EnvMongoDBBackupPassword])
	default:
		return creds, errors.Errorf("not implemented for role: %s", role)
	}

	if creds.Username == "" || creds.Password == "" {
		return creds, errors.Errorf("can't find credentials for role %s", role)
	}

	return creds, nil
}
