# percona-server-mongodb-operator

[![Build Status](https://travis-ci.org/Percona-Lab/percona-server-mongodb-operator.svg?branch=master)](https://travis-ci.org/Percona-Lab/percona-server-mongodb-operator)
[![Go Report Card](https://goreportcard.com/badge/github.com/Percona-Lab/percona-server-mongodb-operator)](https://goreportcard.com/report/github.com/Percona-Lab/percona-server-mongodb-operator)
[![codecov](https://codecov.io/gh/Percona-Lab/percona-server-mongodb-operator/branch/master/graph/badge.svg)](https://codecov.io/gh/Percona-Lab/percona-server-mongodb-operator)

A Kubernetes operator for [Percona Server for MongoDB](https://www.percona.com/software/mongo-database/percona-server-for-mongodb) based on the [Operator SDK](https://github.com/operator-framework/operator-sdk).

<!-- ToC start -->
# Table of Contents

1. [DISCLAIMER](#disclaimer)
1. [Requirements](#requirements)
1. [Run the Operator](#run-the-operator)
1. [Connect to the MongoDB Replica Set](#connect-to-the-mongodb-replica-set)
   1. [Static Endpoints List](#static-endpoints-list)
1. [Required Secrets](#required-secrets)
   1. [MongoDB System Users](#mongodb-system-users)
      1. [Development Mode](#development-mode)
   1. [MongoDB Internal Authentication Key (optional)](#mongodb-internal-authentication-key-optional)
<!-- ToC end -->

# DISCLAIMER

**This code is incomplete, expect major issues and changes until this repo has stabilised!**

# Requirements

The operator was developed/tested for only:
1. Percona Server for MongoDB 3.6 or greater
1. Kubernetes version 1.10 to 1.11
1. Go 1.11

# Run the Operator
1. Add the 'psmdb' Namespace

    on Kubernetes:
    ```
    kubectl create namespace psmdb
    kubectl config set-context $(kubectl config current-context) --namespace=psmdb
    ```
    on OpenShift:
    ```
    oc new-project psmdb
    ```
1. Add the MongoDB Users secrets to Kubernetes. **Update mongodb-users.yaml with new passwords!**
    
    on Kubernetes:
    ```
    kubectl create -f deploy/mongodb-users.yaml
    ```
    on OpenShift:
    ```
    oc create -f deploy/mongodb-users.yaml
    ```
1. Extra step **for Google Kubernetes Engine**
    ```
    kubectl create clusterrolebinding cluster-admin-binding1 --clusterrole=cluster-admin --user=<myname@example.org>
    ```
1. Create RBAC and CustomResourceDefinition

    This step require that your user needs to have **cluster-admin role privileges**. 
    
    Detailed about users and roles:
    
    Kubernetes: [documentation](https://kubernetes.io/docs/reference/access-authn-authz/rbac/#default-roles-and-role-bindings)
    
    OpenShift: [documentation](https://docs.openshift.com/enterprise/3.0/architecture/additional_concepts/authorization.html)
    
    on Kubernetes:
    ```
    kubectl create -f deploy/crd.yaml
    kubectl create -f deploy/rbac.yaml
    ```
    on OpenShift:
    ```
    oc project mykola-psmdb
    oc create -f deploy/crd.yaml
    oc create -f deploy/rbac.yaml
    ```
1. Extra step **for OpenShift**, if you want to manage PSMDB cluster from non-privilegied user, you can grant permissions by applying next clusterrole
    ```
    oc create clusterrole psmdb-admin --verb="*" --resource=perconaservermongodbs.psmdb.percona.com
    oc adm policy add-cluster-role-to-user psmdb-admin <some-user>
    ```
1. Start the percona-server-mongodb-operator within Kubernetes:
    ```
    kubectl create -f deploy/operator.yaml
    ```
1. Create the Percona Server for MongoDB cluster:

    on Kubernetes:
    ```
    kubectl apply -f deploy/cr.yaml
    ```

    on OpenShift:
    1. Uncomment the deploy/cr.yaml field *'#platform:'* and set it to *'platform: openshift'*. Example:
    ```
    apiVersion: psmdb.percona.com/v1alpha1
    kind: PerconaServerMongoDB
    metadata:
      name: my-cluster-name
    spec:
      platform: openshift
    ...
    ...
    ```
    2. Create/apply the CR: 
    ```
    oc apply -f deploy/cr.yaml
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

# Connect to the MongoDB Replica Set

1. From a *'mongo'* shell add a [readWrite](https://docs.mongodb.com/manual/reference/built-in-roles/#readWrite) user for use with an application *(hostname/replicaSet in mongo uri may vary for your situation)*:
    ```
    $ kubectl run -i --rm --tty percona-client --image=percona/percona-server-mongodb:3.6 --restart=Never -- bash -il
    mongodb@percona-client:/$ mongo mongodb+srv://userAdmin:userAdmin123456@my-cluster-name-rs0.psmdb.svc.cluster.local/admin?replicaSet=rs0
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

## Static Endpoints List

If you prefer to use a static server list *(instead of using mongodb+srv:// to detect servers)* use *'kubectl describe service'* to gather the list of endpoints.

Example *(see 'Endpoints:' below)*:
    ```
    $ kubectl describe service my-cluster-name-rs0
    Name:              my-cluster-name-rs0
    Namespace:         myproject
    Labels:            <none>
    Annotations:       <none>
    Selector:          app=percona-server-mongodb,percona-server-mongodb_cr=my-cluster-name,replset=rs0
    Type:              ClusterIP
    IP:                None
    Port:              mongodb  27017/TCP
    TargetPort:        27017/TCP
    Endpoints:         172.17.0.10:27017,172.17.0.12:27017,172.17.0.9:27017
    Session Affinity:  None
    Events:            <none>
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

**Note: Do not use the default MongoDB Users in Production!**

To make development/testing easier a secrets file with default MongoDB System User/Passwords is located at *'deploy/mongodb-users.yaml'*.

The development-mode credentials from *deploy/mongodb-users.yaml* are:

| Secret Key                       | Secret Value         |
|----------------------------------|----------------------|
| MONGODB_BACKUP_USER              | backup               |
| MONGODB_BACKUP_PASSWORD          | backup123456         |
| MONGODB_CLUSTER_ADMIN_USER       | clusterAdmin         |
| MONGODB_CLUSTER_ADMIN_PASSWORD   | clusterAdmin123456   |
| MONGODB_CLUSTER_MONITOR_USER     | clusterMonitor       |
| MONGODB_CLUSTER_MONITOR_PASSWORD | clusterMonitor123456 |
| MONGODB_USER_ADMIN_USER          | userAdmin            |
| MONGODB_USER_ADMIN_PASSWORD      | userAdmin123456      |

## MongoDB Internal Authentication Key (optional)

**Default Secret name**: *my-cluster-name-mongodb-key*
**Secret name field**: *spec.secrets.key*

By default, the operator will create a random, 1024-byte key for [MongoDB Internal Authentication](https://docs.mongodb.com/manual/core/security-internal-authentication/) if it does not already exist.

If you would like to deploy a different key, create the secret manually before starting the operator.

# Configuration

The operator is configured via the *spec* section of the [deploy/cr.yaml](https://github.com/Percona-Lab/percona-server-mongodb-operator/blob/master/deploy/cr.yaml) file.

## Spec (top-level)
YAML Path: *spec*

| Key                   | Value Type  | Default | Description                                                                                                          |
|-----------------------|-------------|---------|----------------------------------------------------------------------------------------------------------------------|
| version               | string      | 3.6     | The Dockerhub tag of [percona/percona-server-mongodb](https://hub.docker.com/r/perconalab/percona-server-mongodb-operator/tags/) to deploy |
| [secrets](#secrets)   | subdoc      |         |                                                                                                                      |
| [replsets](#replsets) | array       |         |                                                                                                                      |
| [mongod](#mongod)     | subdoc      |         |                                                                                                                      |

## Secrets
YAML Path: *spec.secrets*

| Key      | Value Type  | Default                       | Description                                                                                                          |
|----------|-------------|-------------------------------|----------------------------------------------------------------------------------------------------------------------|
| key      | string      | my-cluster-name-mongodb-key   | The secret name for the [MongoDB Internal Auth Key](https://docs.mongodb.com/manual/core/security-internal-authentication/). This secret is auto-created by the operator if it doesn't exist   |
| users    | string      | my-cluster-name-mongodb-users | The secret name for the MongoDB users required to run the operator. **This secret is required to run the operator!** |

## Replsets
YAML Path: *spec.replsets*

| Key                       | Value Type  | Default | Description                                                                                                          |
|---------------------------|-------------|---------|----------------------------------------------------------------------------------------------------------------------|
| name                      | string      | rs0     | The name of the [MongoDB Replica Set](https://docs.mongodb.com/manual/replication/)                                  |
| size                      | int         | 3       | The size of the MongoDB Replica Set, must be >= 3 for [High-Availability](https://docs.mongodb.com/manual/replication/#redundancy-and-data-availability) |
| storageClass              | string      |         | Set the [Kubernetes Storage Class](https://kubernetes.io/docs/concepts/storage/storage-classes/) to use with the MongoDB [Persistent Volume Claim](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#persistentvolumeclaims) |
| resources.limits.cpu      | string      |         |                                                                                                                      |
| resources.limits.memory   | string      |         |                                                                                                                      |
| resources.limits.storage  | string      |         |                                                                                                                      |
| resources.requests.cpu    | string      |         |                                                                                                                      |
| resources.requests.memory | string      |         |                                                                                                                      |

## Mongod
YAML Path: *spec.mongod*

| Key                                                 | Value Type | Default    | Description                                                                                                             |
|-----------------------------------------------------|------------|------------|-------------------------------------------------------------------------------------------------------------------------|
| net.port                                            | int        | 27017      | Sets the MongoDB ['net.port' config option](https://docs.mongodb.com/manual/reference/configuration-options/#net.port)  |
| net.hostPort                                        | int        | 0          | Sets the Kubernetes ['hostPort' option](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/network-plugins/#support-hostport) for MongoDB containers                                                  |
| security.redactClientLogData                        | bool       | false      | Enables/disables the Percona Server for MongoDB [Log Redaction feature](https://www.percona.com/doc/percona-server-for-mongodb/LATEST/log-redaction.html) |
| setParameter.ttlMonitorSleepSecs                    | int        | 60         | Sets the Percona Server for MongoDB 'ttlMonitorSecs' server parameter                                                   |
| setParameter.wiredTigerConcurrentReadTransactions   | int        | 128        | Sets the MongoDB ['wiredTigerConcurrentReadTransactions'](https://docs.mongodb.com/manual/reference/parameters/#param.wiredTigerConcurrentReadTransactions) server parameter |
| setParameter.wiredTigerConcurrentWriteTransactions  | int        | 128        | Sets the MongoDB ['wiredTigerConcurrentWriteTransactions'](https://docs.mongodb.com/manual/reference/parameters/#param.wiredTigerConcurrentWriteTransactions) server parameter |
| storage.engine                                      | string     | wiredTiger | Sets the Mongodb ['storage.engine' config option](https://docs.mongodb.com/manual/reference/configuration-options/#storage.engine) |
| storage.inMemory.inMemorySizeRatio                  | float      | 0.9        |                                                                                                                      |
| storage.mmapv1.nsSize                               | int        | 16         |                                                                                                                      |
| storage.mmapv1.smallfiles                           | bool       | false      |                                                                                                                      |
| storage.wiredTiger.engineConfig.cacheSizeRatio      | float      | 0.5        |                                                                                                                      |
| storage.wiredTiger.engineConfig.directoryForIndexes | bool       | false      |                                                                                                                      |
| storage.wiredTiger.engineConfig.journalCompressor   | string     | snappy     |                                                                                                                      |
| storage.wiredTiger.collectionConfig.blockCompressor | string     | snappy     |                                                                                                                      |
| storage.wiredTiger.indexConfig.prefixCompression    | bool       | true       |                                                                                                                      |
| operationProfiling.mode                             | string     | slowOp     |                                                                                                                      |
| operationProfiling.slowOpThresholdMs                | int        | 100        |                                                                                                                      |
| operationProfiling.rateLimit                        | int        | 1          |                                                                                                                      |
| auditLog.destination                                | string     |            |                                                                                                                      |
| auditLog.format                                     | string     | BSON       |                                                                                                                      |
| auditLog.filter                                     | string     | {}         |                                                                                                                      |
