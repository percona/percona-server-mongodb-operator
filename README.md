# percona-server-mongodb-operator

[![Build Status](https://travis-ci.org/Percona-Lab/percona-server-mongodb-operator.svg?branch=master)](https://travis-ci.org/Percona-Lab/percona-server-mongodb-operator)
[![Go Report Card](https://goreportcard.com/badge/github.com/Percona-Lab/percona-server-mongodb-operator)](https://goreportcard.com/report/github.com/Percona-Lab/percona-server-mongodb-operator)
[![codecov](https://codecov.io/gh/Percona-Lab/percona-server-mongodb-operator/branch/master/graph/badge.svg)](https://codecov.io/gh/Percona-Lab/percona-server-mongodb-operator)

A Kubernetes operator for [Percona Server for MongoDB](https://www.percona.com/software/mongo-database/percona-server-for-mongodb) based on the [Operator SDK](https://github.com/operator-framework/operator-sdk).

# DISCLAIMER

**This code is incomplete, expect major issues and changes until this repo has stabilised!**

# Requirements

The operator was developed/tested for only:
1. Percona Server for MongoDB 3.6 or greater
1. Kubernetes version 1.10 to 1.11
1. Go 1.11

# Run

## Run the Operator
1. Add the 'psmdb' Namespace to Kubernetes:
    ```
    kubectl create namespace psmdb
    kubectl config set-context $(kubectl config current-context) --namespace=psmdb
    ```
1. Add the MongoDB Users secrets to Kubernetes. **Update mongodb-users.yaml with new passwords!!!**
    ```
    kubectl create -f deploy/mongodb-users.yaml
    ```
1. Extra step **(for Google Kubernetes Engine ONLY!!!)**
    ```
    kubectl create clusterrolebinding cluster-admin-binding1 --clusterrole=cluster-admin --user=<myname@example.org>
    ```
1. Start the percona-server-mongodb-operator within Kubernetes:
    ```
    kubectl create -f deploy/rbac.yaml
    kubectl create -f deploy/crd.yaml
    kubectl create -f deploy/operator.yaml
    ```
1. Create the Percona Server for MongoDB cluster:
    ```
    kubectl apply -f deploy/cr.yaml
    ```
1. Wait for the operator and replica set pod reach Running state:
    ```
    $ kubectl get pods
    NAME                                               READY   STATUS    RESTARTS   AGE
    my-cluster-name-rs0-0                              1/1     Running   0          8m
    my-cluster-name-rs0-1                              1/1     Running   0          8m
    my-cluster-name-rs0-2                              1/1     Running   0          7m
    percona-server-mongodb-operator-754846f95d-sf6h6   1/1     Running   0          9m
    ``` 
1. From a *'mongo'* shell add a [readWrite](https://docs.mongodb.com/manual/reference/built-in-roles/#readWrite) user for use with an application *(hostname/replicaSet in mongo uri may vary for your situation)*:
    ```
    $ kubectl run -i --rm --tty percona-client --image=percona/percona-server-mongodb:3.6 --restart=Never -- bash -il
    mongodb@percona-client:/$ mongo mongodb+srv://userAdmin:admin123456@my-cluster-name-rs0.psmdb.svc.cluster.local/admin?replicaSet=rs0
    rs0:PRIMARY> db.createUser({
        user: "myApp",
        pwd: "myAppPassword",
        roles: [
          { db: "myApp", role: "readWrite" }
        ]
    })
    Successfully added user: {
    	"user" : "myApp",
    	"roles" : [
    		{
    			"db" : "myApp",
    			"role" : "readWrite"
    		}
    	]
    }
    ```
1. Again from a *'mongo'* shell, insert and retrieve a test document in the *'myApp'* database as the new application user:
    ```
    $ kubectl run -i --rm --tty percona-client --image=percona/percona-server-mongodb:3.6 --restart=Never -- bash -il
    mongodb@percona-client:/$ mongo mongodb+srv://myApp:myAppPassword@my-cluster-name-rs0.psmdb.svc.cluster.local/admin?replicaSet=rs0
    rs0:PRIMARY> use myApp
    switched to db myApp
    rs0:PRIMARY> db.test.insert({ x: 1 })
    WriteResult({ "nInserted" : 1 })
    rs0:PRIMARY> db.test.findOne()
    { "_id" : ObjectId("5bc74ef05c0ec73be760fcf9"), "x" : 1 }
    ```

# Required Secrets

The operator requires Kubernetes Secrets to be deployed before it is started.

The name of the required secrets can be set in *deploy/cr.yaml* under the section *spec.secrets*.

## MongoDB System Users

**Default Secret name**: *my-cluster-name-mongodb-users*
**Secret name field**: *spec.secrets.users*

The operator requires system-level MongoDB Users to automate the MongoDB deployment. These users should not be used to run an application!

| User Purpose    | Username Secret Key          | Password Secret Key              | MongoDB Role                                                                               |
|-----------------|------------------------------|----------------------------------|--------------------------------------------------------------------------------------------|
| Backup/Restore  | MONGODB_BACKUP_USER          | MONGODB_BACKUP_PASSWORD          | [backup](https://docs.mongodb.com/manual/reference/built-in-roles/#backup), [clusterMonitor](https://docs.mongodb.com/manual/reference/built-in-roles/#clusterMonitor), [restore](https://docs.mongodb.com/manual/reference/built-in-roles/#restore) | 
| Cluster Admin   | MONGODB_CLUSTER_ADMIN_USER   | MONGODB_CLUSTER_ADMIN_PASSWORD   | [clusterAdmin](https://docs.mongodb.com/manual/reference/built-in-roles/#clusterAdmin)     |
| Cluster Monitor | MONGODB_CLUSTER_MONITOR_USER | MONGODB_CLUSTER_MONITOR_PASSWORD | [clusterMonitor](https://docs.mongodb.com/manual/reference/built-in-roles/#clusterMonitor) | 
| User Admin      | MONGODB_USER_ADMIN_USER      | MONGODB_USER_ADMIN_PASSWORD      | [userAdmin](https://docs.mongodb.com/manual/reference/built-in-roles/#userAdmin)           |

### Development Mode

**Note: do not use in Production!**

To make development/testing easier a secrets file with default MongoDB System User/Passwords is located at *'deploy/mongodb-users.yaml'*.

The default credentials from *deploy/mongodb-users.yaml* are:

| Secret Key                       | Secret Value   |
|----------------------------------|----------------|
| MONGODB_BACKUP_USER              | backup         |
| MONGODB_BACKUP_PASSWORD          | admin123456    |
| MONGODB_CLUSTER_ADMIN_USER       | clusterAdmin   |
| MONGODB_CLUSTER_ADMIN_PASSWORD   | admin123456    |
| MONGODB_CLUSTER_MONITOR_USER     | clusterMonitor |
| MONGODB_CLUSTER_MONITOR_PASSWORD | admin123456    |
| MONGODB_USER_ADMIN_USER          | userAdmin      |
| MONGODB_USER_ADMIN_PASSWORD      | admin123456    |

## MongoDB Internal Authentication Key (optional)

**Default Secret name**: *my-cluster-name-mongodb-key*
**Secret name field**: *spec.secrets.key*

By default, the operator will create a random, 1024-byte key for [MongoDB Internal Authentication](https://docs.mongodb.com/manual/core/security-internal-authentication/) if it does not already exist.

If you would like to deploy a different key, create the secret manually before starting the operator.

# Configuration

The operator is configured via the *spec* section of the [deploy/cr.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) file.

| Key                                                        | Value Type | Default                       | Description                                                                                                          |
|------------------------------------------------------------|------------|-------------------------------|----------------------------------------------------------------------------------------------------------------------|
| version                                                    | string     | 3.6                           | The Dockerhub tag of percona/percona-server-mongodb to deploy                                                        |
| secrets.key                                                | string     | my-cluster-name-mongodb-key   | The secret name for the MongoDB Internal Auth Key. This secret is auto-created if it doesn't exist                   |
| secrets.users                                              | string     | my-cluster-name-mongodb-users | The secret name for the MongoDB users required to run the operator. **This secret is required to run the operator!** |
| replsets.name                                              | string     | rs0                           | The name of the MongoDB Replica Set                                                                                  |
| replsets.size                                              | integer    | 3                             | The size of the MongoDB Replica Set, must be >= 3 for high-availability                                              |
| replset.resources.limits.cpu                               | string     |                               |                                                                                                                      |
| replset.resources.limits.memory                            | string     |                               |                                                                                                                      |
| replset.resources.limits.storage                           | string     |                               |                                                                                                                      |
| replset.resources.requests.cpu                             | string     |                               |                                                                                                                      |
| replset.resources.requests.memory                          | string     |                               |                                                                                                                      |
| mongod.net.port                                            | integer    | 27017                         |                                                                                                                      |
| mongod.net.hostPort                                        | integer    | 0                             |                                                                                                                      |
| mongod.security.redactClientLogData                        | boolean    | false                         |                                                                                                                      |
| mongod.setParameter.ttlMonitorSleepSecs                    | integer    | 60                            |                                                                                                                      |
| mongod.setParameter.wiredTigerConcurrentReadTransactions   | integer    | 128                           |                                                                                                                      |
| mongod.setParameter.wiredTigerConcurrentWriteTransactions  | integer    | 128                           |                                                                                                                      |
| mongod.storage.engine                                      | string     | wiredTiger                    |                                                                                                                      |
| mongod.storage.inMemory.inMemorySizeRatio                  | float      | 0.9                           |                                                                                                                      |
| mongod.storage.mmapv1.nsSize                               | integer    | 16                            |                                                                                                                      |
| mongod.storage.mmapv1.smallfiles                           | boolean    | false                         |                                                                                                                      |
| mongod.storage.wiredTiger.engineConfig.cacheSizeRatio      | float      | 0.5                           |                                                                                                                      |
| mongod.storage.wiredTiger.engineConfig.directoryForIndexes | boolean    | false                         |                                                                                                                      |
| mongod.storage.wiredTiger.engineConfig.journalCompressor   | string     | snappy                        |                                                                                                                      |
| mongod.storage.wiredTiger.collectionConfig.blockCompressor | string     | snappy                        |                                                                                                                      |
| mongod.storage.wiredTiger.indexConfig.prefixCompression    | boolean    | true                          |                                                                                                                      |
| mongod.operationProfiling.mode                             | string     | slowOp                        |                                                                                                                      |
| mongod.operationProfiling.slowOpThresholdMs                | integer    | 100                           |                                                                                                                      |
| mongod.operationProfiling.rateLimit                        | integer    | 1                             |                                                                                                                      |
| mongod.auditLog.destination                                | string     |                               |                                                                                                                      |
| mongod.auditLog.format                                     | string     | BSON                          |                                                                                                                      |
| mongod.auditLog.filter                                     | string     | {}                            |                                                                                                                      |
