# percona-server-mongodb-operator

[![Build Status](https://travis-ci.org/Percona-Lab/percona-server-mongodb-operator.svg?branch=master)](https://travis-ci.org/Percona-Lab/percona-server-mongodb-operator)
[![Go Report Card](https://goreportcard.com/badge/github.com/Percona-Lab/percona-server-mongodb-operator)](https://goreportcard.com/report/github.com/Percona-Lab/percona-server-mongodb-operator)
[![codecov](https://codecov.io/gh/Percona-Lab/percona-server-mongodb-operator/branch/master/graph/badge.svg)](https://codecov.io/gh/Percona-Lab/percona-server-mongodb-operator)

# Run

## Run the Operator
1. Add the 'psmdb' Namespace to Kubernetes:
    ```
    $ kubectl create namespace psmdb
    ```
1. Add the MongoDB Users secrets to Kubernetes. **Update mongodb-users.yaml with new passwords!!!**
    ```
    $ kubectl create -f deploy/mongodb-users.yaml
    ```
1. Start the percona-server-mongodb-operator within Kubernetes:
    ```
    $ kubectl create -f deploy/rbac.yaml
    $ kubectl create -f deploy/crd.yaml
    $ kubectl create -f deploy/operator.yaml
    ```
1. Create the Percona Server for MongoDB CR:
    ```
    $ kubectl apply -f deploy/cr.yaml
    ```
1. Wait for the operator and replica set pod reach Running state:
    ```
    $ kubectl --namespace=psmdb get pods
    NAME                                               READY   STATUS    RESTARTS   AGE
    percona-server-mongodb-operator-754846f95d-z4vh9   1/1     Running   0          17m
    rs0-0                                              1/1     Running   0          17m
    ``` 
1. Add a readWrite user for an application *(requires 'mongo' shell)*:
    ```
    $ mongo -u userAdmin -p admin123456 --host=rs0-0.percona-server-mongodb.psmdb.svc.cluster.local admin
    rs0:PRIMARY> db.createUser({user: "app", pwd: "myAppPassword", roles: [ { db: "myApp", role: "readWrite" } ] })
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
1. Insert a test document in the 'myApp' database as the new application user:
    ```
    $ mongo -u myApp -p myAppPassword --host=rs0-0.percona-server-mongodb.psmdb.svc.cluster.local admin
    rs0:PRIMARY> use myApp
    switched to db myApp
    rs0:PRIMARY> db.test.insert({x:1})
    WriteResult({ "nInserted" : 1 })
    ```
