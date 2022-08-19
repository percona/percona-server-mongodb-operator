![Percona Distribution for MongoDB Operator](operator.png)
<div align="center">

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
![Docker Pulls](https://img.shields.io/docker/pulls/percona/percona-server-mongodb-operator)
![Docker Image Size (latest by date)](https://img.shields.io/docker/image-size/percona/percona-server-mongodb-operator)
![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/percona/percona-server-mongodb-operator)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/percona/percona-server-mongodb-operator)
[![Go Report Card](https://goreportcard.com/badge/github.com/percona/percona-server-mongodb-operator)](https://goreportcard.com/report/github.com/percona/percona-server-mongodb-operator)
</div>

[Percona Server for MongoDB](https://www.percona.com/software/mongodb/percona-server-for-mongodb) (PSMDB) is an open-source enterprise MongoDB solution that helps you to ensure data availability for your applications while improving security and simplifying the development of new applications in the most demanding public, private, and hybrid cloud environments.

Based on our best practices for deployment and configuration, [Percona Operator for MongoDB](https://www.percona.com/doc/kubernetes-operator-for-psmongodb/index.html) contains everything you need to quickly and consistently deploy and scale Percona Server for MongoDB instances into a Kubernetes cluster on-premises or in the cloud. It provides the following capabilities:

* Easy deployment with no single point of failure
* Sharding support
* Scheduled and manual backups
* Integrated monitoring with [Percona Monitoring and Management](https://www.percona.com/software/database-tools/percona-monitoring-and-management)
* Smart Update to keep your database software up to date automatically
* Automated Password Rotation – use the standard Kubernetes API to enforce password rotation policies for system user
* Private container image registries

# Architecture

Percona Operators are based on the [Operator SDK](https://github.com/operator-framework/operator-sdk) and leverage Kubernetes primitives to follow best CNCF practices. 

Please read more about architecture and design decisions [here](https://www.percona.com/doc/kubernetes-operator-for-psmongodb/architecture.html).

# Quickstart installation

## Helm

Install the Operator:

```bash
helm install my-op percona/psmdb-operator
```

Install Percona Server for MongoDB:

```bash
helm install my-db percona/psmdb-db
```

See more details in:
- [Helm installation documentation](https://www.percona.com/doc/kubernetes-operator-for-psmongodb/helm.html)
- [Operator helm chart parameter reference](https://github.com/percona/percona-helm-charts/blob/main/charts/psmdb-operator)
- [Percona Server for MongoDB helm chart parameters reference](https://github.com/percona/percona-helm-charts/blob/main/charts/psmdb-db)

## kubectl

It usually takes two steps to deploy Percona Server for MongoDB on Kubernetes:

Deploy the operator from `deploy/bundle.yaml`:

```sh
kubectl apply --server-side -f https://raw.githubusercontent.com/percona/percona-server-mongodb-operator/main/deploy/bundle.yaml
```

Deploy the database cluster itself from `deploy/cr.yaml

```sh
kubectl apply -f https://raw.githubusercontent.com/percona/percona-server-mongodb-operator/main/deploy/cr-minimal.yaml
```

See full documentation with examples and various advanced cases on [percona.com](https://www.percona.com/doc/kubernetes-operator-for-psmongodb/index.html).

# Contributing

Percona welcomes and encourages community contributions to help improve Percona Kubernetes Operator for Percona Server for MongoDB.

See the [Contribution Guide](CONTRIBUTING.md) and [Building and Testing Guide](e2e-tests/README.md) for more information.

# Roadmap

We have an experimental public roadmap which can be found [here](https://github.com/percona/roadmap/projects/1). Please feel free to contribute and propose new features by following the roadmap [guidelines](https://github.com/percona/roadmap).
 
# Submitting Bug Reports

If you find a bug in Percona Docker Images or in one of the related projects, please submit a report to that project's [JIRA](https://jira.percona.com) issue tracker. Learn more about submitting bugs, new features ideas and improvements in the [Contribution Guide](CONTRIBUTING.md).

