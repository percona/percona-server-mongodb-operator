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
