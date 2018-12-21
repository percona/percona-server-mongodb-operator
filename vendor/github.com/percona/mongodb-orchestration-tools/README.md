# mongodb-orchestration-tools

[![](https://godoc.org/github.com/percona/mongodb-orchestration-tools?status.svg)](http://godoc.org/github.com/percona/mongodb-orchestration-tools)
[![Build Status](https://travis-ci.org/percona/mongodb-orchestration-tools.svg?branch=master)](https://travis-ci.org/percona/mongodb-orchestration-tools)
[![Go Report Card](https://goreportcard.com/badge/github.com/percona/mongodb-orchestration-tools)](https://goreportcard.com/report/github.com/percona/mongodb-orchestration-tools)
[![codecov](https://codecov.io/gh/percona/mongodb-orchestration-tools/branch/master/graph/badge.svg)](https://codecov.io/gh/percona/mongodb-orchestration-tools)

Go-based tools for MongoDB container orchestration.

*Note: This code is intended for a specific integration/use case, therefore it is unlikely Issues or Pull Requests will be accepted from the public. Please fork if this is a concern.*

**Tools**:
- **mongodb-executor**: a tool for executing tasks on the local mongod/mongos container
- **mongodb-healthcheck**: a tool for running MongoDB DC/OS health+readiness checks or Kubernetes liveness check
- **dcos-mongodb-controller**: a tool for controlling the replica set initiation and adding system MongoDB users
- **dcos-mongodb-watchdog**: a daemon to monitor dcos pod status and manage mongodb replica set membership
- **k8s-mongodb-initiator**: a tool for replica set initiation and adding system MongoDB users

## Use Case
The tools in this repository are designed to be used specifically within the [DC/OS 'percona-server-mongodb' service](https://docs.mesosphere.com/services/percona-server-mongodb/) or the [Percona Server for MongoDB Operator](https://github.com/Percona-Lab/percona-server-mongodb-operator).

## Required

### MongoDB
These tools were designed/tested for use with [Percona Server for MongoDB](https://www.percona.com/software/mongo-database/percona-server-for-mongodb) 3.6 and above with [Replication](https://docs.mongodb.com/manual/replication/) and [Authentication](https://docs.mongodb.com/manual/core/authentication/) enabled.

### DC/OS
The minimum requirements are:
1. DC/OS 1.10+ *(1.11+ recommended)*

### Kubernetes Operator
The minimum requirements are:
1. Kubernetes v1.10+

## Build
1. Install go1.10+ and 'make'
2. Run 'make' in git directory
3. Find binaries in 'bin' directory

## Contact
- Tim Vaillancourt - [Github](https://github.com/timvaillancourt) [Email](mailto:tim.vaillancourt@percona.com)
- Percona - [Twitter](https://twitter.com/Percona) [Contact Page](https://www.percona.com/about-percona/contact)
