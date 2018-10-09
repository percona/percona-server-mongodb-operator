# percona-server-mongodb-operator

[![Build Status](https://travis-ci.org/Percona-Lab/percona-server-mongodb-operator.svg?branch=master)](https://travis-ci.org/Percona-Lab/percona-server-mongodb-operator)
[![Go Report Card](https://goreportcard.com/badge/github.com/Percona-Lab/percona-server-mongodb-operator)](https://goreportcard.com/report/github.com/Percona-Lab/percona-server-mongodb-operator)
[![codecov](https://codecov.io/gh/Percona-Lab/percona-server-mongodb-operator/branch/master/graph/badge.svg)](https://codecov.io/gh/Percona-Lab/percona-server-mongodb-operator)

# Run

1. Deploy the CRD:
```
$ kubectl create -f deploy/crd.yaml
```
1. Start the Operator *(external to kubernetes)*
```
$ OPERATOR_NAME=<name> operator-sdk up local
```
1. Create the Percona Server for MongoDB CR:
```
$ kubectl apply -f deploy/cr.yaml
```
