package mongodbuser

import (
	"context"
	"reflect"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

// MongoDBUserReconciler reconciles a MongoDBUser object
type MongoDBUserReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=psmdb.percona.com,resources=mongodbusers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=psmdb.percona.com,resources=mongodbusers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=psmdb.percona.com,resources=mongodbusers/finalizers,verbs=update
//+kubebuilder:rbac:groups=psmdb.percona.com,resources=perconaservermongodbs,verbs=get;list;watch
//+kubebuilder:rbac:groups=psmdb.percona.com,resources=perconaservermongodbs/status,verbs=get
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *MongoDBUserReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the MongoDBUser instance
	mongoDBUser := &api.MongoDBUser{}
	err := r.Get(ctx, req.NamespacedName, mongoDBUser)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Return and don't requeue
			log.Info("MongoDBUser resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get MongoDBUser")
		return ctrl.Result{}, err
	}

	// Check if the MongoDBUser is being deleted
	if mongoDBUser.DeletionTimestamp != nil {
		log.Info("MongoDBUser is being deleted, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Get the referenced MongoDB cluster
	cluster, err := r.getMongoDBCluster(ctx, mongoDBUser)
	if err != nil {
		log.Error(err, "Failed to get MongoDB cluster")
		r.updateStatus(ctx, mongoDBUser, api.AppStateError, err.Error())
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// Check if the cluster is ready
	if cluster.Status.State != api.AppStateReady {
		log.Info("MongoDB cluster is not ready, waiting", "clusterState", cluster.Status.State)
		r.updateStatus(ctx, mongoDBUser, api.AppStateInit, "Waiting for MongoDB cluster to be ready")
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// Get MongoDB client
	mongoClient, err := r.getMongoClient(ctx, cluster, mongoDBUser)
	if err != nil {
		log.Error(err, "Failed to get MongoDB client")
		r.updateStatus(ctx, mongoDBUser, api.AppStateError, err.Error())
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	defer func() {
		if err := mongoClient.Disconnect(ctx); err != nil {
			log.Error(err, "Failed to disconnect MongoDB client")
		}
	}()

	// Reconcile the user
	err = r.reconcileUser(ctx, mongoDBUser, mongoClient)
	if err != nil {
		log.Error(err, "Failed to reconcile user")
		r.updateStatus(ctx, mongoDBUser, api.AppStateError, err.Error())
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// Update status to ready
	r.updateStatus(ctx, mongoDBUser, api.AppStateReady, "User successfully reconciled")

	return ctrl.Result{}, nil
}

// getMongoDBCluster retrieves the MongoDB cluster referenced by the user
func (r *MongoDBUserReconciler) getMongoDBCluster(ctx context.Context, user *api.MongoDBUser) (*api.PerconaServerMongoDB, error) {
	clusterName := user.Spec.ClusterRef.Name
	clusterNamespace := user.Spec.ClusterRef.Namespace
	if clusterNamespace == "" {
		clusterNamespace = user.Namespace
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

// getMongoClient creates a MongoDB client with appropriate credentials
func (r *MongoDBUserReconciler) getMongoClient(ctx context.Context, cluster *api.PerconaServerMongoDB, user *api.MongoDBUser) (mongo.Client, error) {
	// Get the internal user secret for authentication
	secretName := api.InternalUserSecretName(cluster)
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: cluster.Namespace,
	}, secret)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get internal user secret %s", secretName)
	}

	// Use userAdmin credentials for user management
	username := string(secret.Data[api.EnvMongoDBUserAdminUser])
	password := string(secret.Data[api.EnvMongoDBUserAdminPassword])

	// Get MongoDB connection details
	var mongoClient mongo.Client
	if cluster.Spec.Sharding.Enabled {
		// For sharded clusters, connect to mongos
		mongoClient, err = r.getMongosClient(ctx, cluster, username, password)
	} else {
		// For replica sets, connect to the primary
		mongoClient, err = r.getReplsetClient(ctx, cluster, username, password)
	}

	if err != nil {
		return nil, errors.Wrap(err, "failed to create MongoDB client")
	}

	return mongoClient, nil
}

// getMongosClient creates a client for sharded clusters
func (r *MongoDBUserReconciler) getMongosClient(ctx context.Context, cluster *api.PerconaServerMongoDB, username, password string) (mongo.Client, error) {
	// This is a simplified implementation. In a real scenario, you would need to
	// get the mongos service endpoints and create a proper MongoDB client
	// For now, we'll use a placeholder
	return nil, errors.New("mongos client not implemented yet")
}

// getReplsetClient creates a client for replica sets
func (r *MongoDBUserReconciler) getReplsetClient(ctx context.Context, cluster *api.PerconaServerMongoDB, username, password string) (mongo.Client, error) {
	// This is a simplified implementation. In a real scenario, you would need to
	// get the replica set endpoints and create a proper MongoDB client
	// For now, we'll use a placeholder
	return nil, errors.New("replset client not implemented yet")
}

// reconcileUser handles the user creation/update logic
func (r *MongoDBUserReconciler) reconcileUser(ctx context.Context, user *api.MongoDBUser, mongoClient mongo.Client) error {
	log := log.FromContext(ctx)

	// Determine the database for the user
	db := user.Spec.Database
	if db == "" {
		db = "admin"
	}

	// Handle external authentication
	if user.Spec.IsExternal {
		db = "$external"
	}

	// Get existing user info
	userInfo, err := mongoClient.GetUserInfo(ctx, user.Spec.Username, db)
	if err != nil {
		log.Error(err, "Failed to get user info", "username", user.Spec.Username, "database", db)
	}

	// Convert global roles to MongoDB format
	globalRoles := make([]mongo.Role, 0)
	for _, role := range user.Spec.Roles {
		globalRoles = append(globalRoles, mongo.Role{
			DB:   role.DB,
			Role: role.Name,
		})
	}

	// Convert database-specific roles to MongoDB format
	databaseRoles := make([]mongo.Role, 0)
	for _, dbAccess := range user.Spec.DatabaseAccess {
		for _, role := range dbAccess.Roles {
			databaseRoles = append(databaseRoles, mongo.Role{
				DB:   dbAccess.DatabaseName,
				Role: role,
			})
		}
	}

	// Combine all roles
	roles := append(globalRoles, databaseRoles...)

	if userInfo == nil {
		// Create new user
		if user.Spec.IsExternal {
			// External authentication user
			err = mongoClient.CreateUser(ctx, db, user.Spec.Username, "", roles...)
		} else {
			// Get password from secret
			password, err := r.getUserPassword(ctx, user)
			if err != nil {
				return errors.Wrap(err, "failed to get user password")
			}
			err = mongoClient.CreateUser(ctx, db, user.Spec.Username, password, roles...)
		}
		if err != nil {
			return errors.Wrapf(err, "failed to create user %s", user.Spec.Username)
		}
		log.Info("Created user", "username", user.Spec.Username, "database", db)
	} else {
		// Update existing user
		// Check if roles have changed
		if !reflect.DeepEqual(userInfo.Roles, roles) {
			err = mongoClient.UpdateUserRoles(ctx, db, user.Spec.Username, roles)
			if err != nil {
				return errors.Wrapf(err, "failed to update user roles for %s", user.Spec.Username)
			}
			log.Info("Updated user roles", "username", user.Spec.Username)
		}

		// Update password if not external
		if !user.Spec.IsExternal {
			password, err := r.getUserPassword(ctx, user)
			if err != nil {
				return errors.Wrap(err, "failed to get user password")
			}
			err = mongoClient.UpdateUserPass(ctx, db, user.Spec.Username, password)
			if err != nil {
				return errors.Wrapf(err, "failed to update user password for %s", user.Spec.Username)
			}
			log.Info("Updated user password", "username", user.Spec.Username)
		}
	}

	return nil
}

// getUserPassword retrieves the user password from the referenced secret
func (r *MongoDBUserReconciler) getUserPassword(ctx context.Context, user *api.MongoDBUser) (string, error) {
	if user.Spec.PasswordSecretRef == nil {
		return "", errors.New("password secret reference is required for non-external users")
	}

	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      user.Spec.PasswordSecretRef.Name,
		Namespace: user.Namespace,
	}, secret)
	if err != nil {
		return "", errors.Wrapf(err, "failed to get password secret %s", user.Spec.PasswordSecretRef.Name)
	}

	key := user.Spec.PasswordSecretRef.Key
	if key == "" {
		key = "password"
	}

	password, exists := secret.Data[key]
	if !exists {
		return "", errors.Errorf("password key %s not found in secret %s", key, user.Spec.PasswordSecretRef.Name)
	}

	return string(password), nil
}

// updateStatus updates the status of the MongoDBUser
func (r *MongoDBUserReconciler) updateStatus(ctx context.Context, user *api.MongoDBUser, state api.AppState, message string) {
	log := log.FromContext(ctx)

	user.Status.State = state
	user.Status.Message = message
	user.Status.ObservedGeneration = user.Generation

	now := metav1.Now()
	user.Status.LastSyncTime = &now

	// Update conditions
	r.updateConditions(user, state, message)

	err := r.Status().Update(ctx, user)
	if err != nil {
		log.Error(err, "Failed to update MongoDBUser status")
	}
}

// updateConditions updates the conditions of the MongoDBUser
func (r *MongoDBUserReconciler) updateConditions(user *api.MongoDBUser, state api.AppState, message string) {
	now := metav1.Now()

	// Find existing condition or create new one
	var readyCondition *api.UserCondition
	for i := range user.Status.Conditions {
		if user.Status.Conditions[i].Type == api.UserConditionReady {
			readyCondition = &user.Status.Conditions[i]
			break
		}
	}

	if readyCondition == nil {
		readyCondition = &api.UserCondition{
			Type:               api.UserConditionReady,
			LastTransitionTime: now,
		}
		user.Status.Conditions = append(user.Status.Conditions, *readyCondition)
	}

	// Determine condition status
	var status api.ConditionStatus
	switch state {
	case api.AppStateReady:
		status = api.ConditionTrue
	case api.AppStateError:
		status = api.ConditionFalse
	default:
		status = api.ConditionUnknown
	}

	// Update condition if status changed
	if readyCondition.Status != status {
		readyCondition.Status = status
		readyCondition.LastTransitionTime = now
		readyCondition.Message = message
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *MongoDBUserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&api.MongoDBUser{}).
		Complete(r)
}
