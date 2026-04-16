# PBM tests

## Run tests
Run all tests
```
$ MONGODB_VERSION=8.0 ./run-all
```
`MONGODB_VERSION` is a PSMDB version (e.g. 6.0/7.0/8.0). Default is `8.0`

`./run-all` would run all tests both on a sharded cluster and a non-sharded replica set.

### You might want to run some tests separately:

* `./run-new-cluster` - restore on a blank new cluster
* `./run-rs` - general and consistency tests on a non-sharded replicaset
* `./run-sharded` - general and consistency tests on a sharded clusters

*`MONGODB_VERSION` applies as for the `./run-all`

## Start test cluster
To start tests with a running pbm-agent and minio storage:
```
$ MONGODB_VERSION=8.0 ./start-cluster
```
`MONGODB_VERSION` is a PSMDB version (e.g. 6.0/7.0/8.0). Default is `8.0`

`./start-replset` - to start a non-sharded replica set.

You need to set up PBM though:
```
$ docker compose -f ./docker/docker-compose.yaml exec agent-rs101 pbm config --file=/etc/pbm/minio.yaml
```
Run pbm commands:
```
$ docker compose -f ./docker/docker-compose.yaml exec agent-rs101 pbm list
```
