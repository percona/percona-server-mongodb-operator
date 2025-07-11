package mongodbdatabase

import (
	"context"
	"time"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

// MongoDBDatabaseReconciler reconciles a MongoDBDatabase object
type MongoDBDatabaseReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=psmdb.percona.com,resources=mongodbdatabases,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=psmdb.percona.com,resources=mongodbdatabases/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=psmdb.percona.com,resources=mongodbdatabases/finalizers,verbs=update
//+kubebuilder:rbac:groups=psmdb.percona.com,resources=perconaservermongodbs,verbs=get;list;watch
//+kubebuilder:rbac:groups=psmdb.percona.com,resources=perconaservermongodbs/status,verbs=get

func (r *MongoDBDatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the MongoDBDatabase instance
	dbCR := &api.MongoDBDatabase{}
	err := r.Get(ctx, req.NamespacedName, dbCR)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if dbCR.DeletionTimestamp != nil {
		log.Info("MongoDBDatabase is being deleted, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Get the referenced MongoDB cluster
	cluster, err := r.getMongoDBCluster(ctx, dbCR)
	if err != nil {
		log.Error(err, "Failed to get MongoDB cluster")
		r.updateStatus(ctx, dbCR, api.AppStateError, err.Error())
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	if cluster.Status.State != api.AppStateReady {
		log.Info("MongoDB cluster is not ready, waiting", "clusterState", cluster.Status.State)
		r.updateStatus(ctx, dbCR, api.AppStateInit, "Waiting for MongoDB cluster to be ready")
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// 获取MongoDB客户端（此处为伪代码，需结合实际实现）
	mongoClient, err := r.getMongoClient(ctx, cluster)
	if err != nil {
		log.Error(err, "Failed to get MongoDB client")
		r.updateStatus(ctx, dbCR, api.AppStateError, err.Error())
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	defer mongoClient.Disconnect(ctx)

	// 1. 创建数据库（MongoDB中数据库在插入第一个集合/文档时自动创建）
	// 2. 创建集合及索引
	// 3. 授权用户
	// 这里只做结构示例，具体细节可后续完善
	log.Info("Reconciling MongoDBDatabase", "db", dbCR.Spec.Name)

	r.updateStatus(ctx, dbCR, api.AppStateReady, "Database successfully reconciled")
	return ctrl.Result{}, nil
}

func (r *MongoDBDatabaseReconciler) getMongoDBCluster(ctx context.Context, db *api.MongoDBDatabase) (*api.PerconaServerMongoDB, error) {
	clusterName := db.Spec.ClusterRef.Name
	clusterNamespace := db.Spec.ClusterRef.Namespace
	if clusterNamespace == "" {
		clusterNamespace = db.Namespace
	}
	cluster := &api.PerconaServerMongoDB{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      clusterName,
		Namespace: clusterNamespace,
	}, cluster)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get MongoDB cluster %s in namespace %s", clusterName, clusterNamespace)
	}
	return cluster, nil
}

func (r *MongoDBDatabaseReconciler) getMongoClient(ctx context.Context, cluster *api.PerconaServerMongoDB) (mongo.Client, error) {
	// 这里应实现与MongoDBUser类似的连接逻辑
	return nil, errors.New("getMongoClient not implemented")
}

func (r *MongoDBDatabaseReconciler) updateStatus(ctx context.Context, db *api.MongoDBDatabase, state api.AppState, message string) {
	db.Status.State = state
	db.Status.Message = message
	db.Status.ObservedGeneration = db.Generation
	now := metav1.Now()
	db.Status.LastSyncTime = &now
	_ = r.Status().Update(ctx, db)
}

func (r *MongoDBDatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&api.MongoDBDatabase{}).
		Complete(r)
}
