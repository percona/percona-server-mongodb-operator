# Percona Operator for MongoDB

![Percona Kubernetes Operators](kubernetes.svg)

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
![Docker Pulls](https://img.shields.io/docker/pulls/percona/percona-server-mongodb-operator)
![Docker Image Size (latest by date)](https://img.shields.io/docker/image-size/percona/percona-server-mongodb-operator)
![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/percona/percona-server-mongodb-operator)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/percona/percona-server-mongodb-operator)
[![Go Report Card](https://goreportcard.com/badge/github.com/percona/percona-server-mongodb-operator)](https://goreportcard.com/report/github.com/percona/percona-server-mongodb-operator)

[Percona Operator for MongoDB](https://github.com/percona/percona-server-mongodb-operator) deploys and manages Percona Server for MongoDB on Kubernetes with ease. Automate deployments, scaling, and day-to-day operations for both replica sets and sharded clusters. Deploy with confidence and focus on your applications, not your database.

- Automated workflows for simplified management
- High availability with no single point of failure
- Easy sharding and scaling
- Integrated backups and monitoring
- Automated updates and password rotation
- Support for private container registries

While the Percona Operator is primarily managed through the command line, you can also use **[Percona Everest](https://docs.percona.com/everest/index.html)** for a web-based user interface. This open-source tool provides a streamlined experience for provisioning and managing your databases, simplifying day-to-day tasks and reducing administrative overhead. Learn more about Percona Everest in the [documentation](https://docs.percona.com/everest/index.html) or jump right in with the [quickstart guide](https://docs.percona.com/everest/quickstart-guide/quick-install.html).

# Architecture

Percona Operators are based on the [Operator SDK](https://github.com/operator-framework/operator-sdk) and leverage Kubernetes primitives to follow best [CNCF](https://www.cncf.io/) practices.

Learn more about [architecture and design decisions](https://www.percona.com/doc/kubernetes-operator-for-psmongodb/architecture.html).

## Documentation

To learn more about the Operator, check the [Percona Operator for MongoDB documentation](https://docs.percona.com/percona-operator-for-mongodb/index.html). 

# Quickstart installation

Ready to try out the Operator? Check the [Quickstart tutorial](https://docs.percona.com/percona-operator-for-mongodb/quickstart.html) for easy-to follow steps. 

Below is one of the ways to deploy the Operator using `kubectl`.

## kubectl

1. Deploy the operator from `deploy/bundle.yaml`:

```sh
kubectl apply --server-side -f https://raw.githubusercontent.com/percona/percona-server-mongodb-operator/main/deploy/bundle.yaml
```

2. Deploy the database cluster itself from `deploy/cr.yaml`

```sh
kubectl apply -f https://raw.githubusercontent.com/percona/percona-server-mongodb-operator/main/deploy/cr-minimal.yaml
```

# Need help?


**Commercial Support**  | **Community Support** |
:-: | :-: |
| <br/>Enterprise-grade assistance for your mission-critical MongoDB deployments with the Percona Operator for MongoDB. Get expert guidance for complex tasks like multi-cloud replication, database migration and building platforms.<br/><br/>  | <br/>Connect with our engineers and fellow users for general questions, troubleshooting, and sharing feedback and ideas.<br/><br/>  | 
| **[Get Percona Support](https://hubs.ly/Q02ZTH830)** | **[Visit our Forum](https://forums.percona.com/c/mongodb/percona-kubernetes-operator-for-mongodb/29)** |

# Contributing

Percona welcomes and encourages community contributions to help improve Percona Kubernetes Operator for Percona Server for MongoDB.

See the [Contribution Guide](CONTRIBUTING.md) and [Building and Testing Guide](e2e-tests/README.md) for more information on how you can contribute.

## Roadmap

We have a public roadmap which can be found [here](https://github.com/orgs/percona/projects/10). Please feel free to contribute and propose new features by following the roadmap [guidelines](https://github.com/percona/roadmap).

## Submitting Bug Reports

If you find a bug in Percona Docker Images or in one of the related projects, please submit a report to that project's [JIRA](https://jira.percona.com/projects/K8SPSMDB/issues/K8SPSMDB-555?filter=allopenissues) issue tracker or [create a GitHub issue](https://docs.github.com/en/issues/tracking-your-work-with-issues/creating-an-issue#creating-an-issue-from-a-repository) in this repository.

Learn more about submitting bugs, new features ideas and improvements in the [Contribution Guide](CONTRIBUTING.md).
