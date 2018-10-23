# percona-server-mongodb-operator

[![Build Status](https://travis-ci.org/Percona-Lab/percona-server-mongodb-operator.svg?branch=master)](https://travis-ci.org/Percona-Lab/percona-server-mongodb-operator)
[![Go Report Card](https://goreportcard.com/badge/github.com/Percona-Lab/percona-server-mongodb-operator)](https://goreportcard.com/report/github.com/Percona-Lab/percona-server-mongodb-operator)
[![codecov](https://codecov.io/gh/Percona-Lab/percona-server-mongodb-operator/branch/master/graph/badge.svg)](https://codecov.io/gh/Percona-Lab/percona-server-mongodb-operator)

A Kubernetes operator for [Percona Server for MongoDB](https://www.percona.com/software/mongo-database/percona-server-for-mongodb) based on the [Operator SDK](https://github.com/operator-framework/operator-sdk).

# DISCLAIMER

**This code is incomplete, expect major issues and changes until this repo has stabilised!**

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
    percona-server-mongodb-operator-754846f95d-z4vh9   1/1     Running   0          17m
    rs0-0                                              1/1     Running   0          17m
    ``` 
1. From a Kubernetes container with the 'mongo' shell, add a [readWrite](https://docs.mongodb.com/manual/reference/built-in-roles/#readWrite) user for use with an application *(requires 'mongo' shell, mongo --host= field may vary for your situation)*:
    ```
    $ kubectl run -i --rm --tty percona-client --image=percona/percona-server-mongodb:3.6 --restart=Never -- bash -il
    mongodb@percona-client:/$ mongo -u userAdmin -p admin123456 --host=my-cluster-name admin
    rs0:PRIMARY> db.createUser({
        user: "app",
        pwd: "myAppPassword",
        roles: [
          { db: "myApp", role: "readWrite" }
        ]
    })
    Successfully added user: {
    	"user" : "app",
    	"roles" : [
    		{
    			"db" : "myApp",
    			"role" : "readWrite"
    		}
    	]
    }
    ```
1. Again from a Kubernetes container with the 'mongo' shell, insert and retrieve a test document in the 'myApp' database as the new application user:
    ```
    $ kubectl run -i --rm --tty percona-client --image=percona/percona-server-mongodb:3.6 --restart=Never -- bash -il
    mongodb@percona-client:/$ mongo -u app -p myAppPassword --host=my-cluster-name admin
    rs0:PRIMARY> use myApp
    switched to db myApp
    rs0:PRIMARY> db.test.insert({ x: 1 })
    WriteResult({ "nInserted" : 1 })
    rs0:PRIMARY> db.test.findOne()
    { "_id" : ObjectId("5bc74ef05c0ec73be760fcf9"), "x" : 1 }
    ```
