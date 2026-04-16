# Contributing guide

Welcome to Percona Backup for MongoDB!

1. [Prerequisites](#prerequisites)
2. [Submitting a pull request](#submitting-a-pull-request)
3. [Contributing to documentation](#contributing-to-documentation)

We're glad that you would like to become a Percona community member and participate in keeping open source open.

Percona Backup for MongoDB (PBM) is a distributed, low-impact solution for achieving consistent backups of MongoDB sharded clusters and replica sets.

You can contribute in one of the following ways:

1. Reach us on our [Forums](https://forums.percona.com/c/mongodb/percona-backup-for-mongodb).
2. [Submit a bug report or a feature request](https://jira.percona.com/projects/PBM)
3. Submit a pull request (PR) with the code patch
4. Contribute to documentation

## Prerequisites

Before submitting code contributions, we ask you to complete the following prerequisites.

### 1. Sign the CLA

Before you can contribute, we kindly ask you to sign our [Contributor License Agreement](https://cla-assistant.percona.com/percona/percona-backup-mongodb) (CLA). You can do this in on click using your GitHub account.

**Note**:  You can sign it later, when submitting your first pull request. The CLA assistant validates the PR and asks you to sign the CLA to proceed.

### 2. Code of Conduct

Please make sure to read and agree to our [Code of Conduct](https://github.com/percona/community/blob/main/content/contribute/coc.md).

## Submitting a pull request

All bug reports, enhancements and feature requests are tracked in [Jira issue tracker](https://jira.percona.com/projects/PBM). Though not mandatory, we encourage you to first check for a bug report among Jira issues and in the PR list: perhaps the bug has already been addressed.

For feature requests and enhancements, we do ask you to create a Jira issue, describe your idea and discuss the design with us. This way we align your ideas with our vision for the product development.

If the bug hasn’t been reported / addressed, or we’ve agreed on the enhancement implementation with you, do the following:

1. [Fork](https://docs.github.com/en/github/getting-started-with-github/fork-a-repo) this repository
2. Clone this repository on your machine.
3. Create a separate branch for your changes. If you work on a Jira issue, please  include the issue number in the branch name so it reads as ``<JIRAISSUE>-my_branch``. This makes it easier to track your contribution.
4. Make your changes. Please follow the guidelines outlined in the [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments) to improve code readability.
5. Test your changes locally. See the [Running tests locally](#running-tests-locally) section for more information
6. Commit the changes. Add the Jira issue number at the beginning of your  message subject so that is reads as `<JIRAISSUE> -  My subject`. The [commit message guidelines](https://gist.github.com/robertpainsi/b632364184e70900af4ab688decf6f53) will help you with writing great commit messages
7. Open a PR to Percona
8. Our team will review your code and if everything is correct, will merge it.
Otherwise, we will contact you for additional information or with the request to make changes.

### Building Percona Backup for MongoDB

To build Percona Backup for MongoDB from source code, you require the following:

* Go 1.25 or above. See [Installing and setting up Go tools](
https://golang.org/doc/install) for more information
* make
* ``krb5-devel`` for Red Hat Enterprise Linux / CentOS or ``libkrb5-dev`` for Debian / Ubuntu. This package is required for Kerberos authentication in Percona Server for MongoDB.

To build the project, run the following commands:

```sh
$ git clone https://github.com/<your_name>/percona-backup-mongodb
$ cd percona-backup-mongodb
$ make build
```

After ``make`` completes, you can find ``pbm`` and ``pbm-agent`` binaries in the ``./bin`` directory:

```sh
$ cd bin
$ ./pbm version
```

By running ``pbm version``, you can verify if Percona Backup for MongoDB has been built correctly and is ready for use.

```
Output

Version:   [pbm version number]
Platform:  linux/amd64
GitCommit: [commit hash]
GitBranch: master
BuildTime: [time when this version was produced in UTC format]
GoVersion: [Go version number]
```

**TIP**: instead of specifying the path to pbm binaries, you can add it to the PATH environment variable:

```sh
export PATH=/percona-backup-mongodb/bin:$PATH
```

### Running tests locally

When you work, you should periodically run tests to check that your changes don’t break existing code.

You can find the tests in the ``e2e-tests`` directory.

To save time on tests execution during development, we recommend running  general and consistency tests for a sharded cluster:

```sh
$ MONGODB_VERSION=8.0 ./run-sharded
```

``$ MONGODB_VERSION`` stands for the Percona Server for MongoDB version Percona Backup for MongoDB is running with. Default is 8.0.

After the development is complete and you are ready to submit a pull request, run all tests using the following command:

```sh
$ MONGODB_VERSION=8.0 ./run-all
```

You can run tests on your local machine with whatever operating system you have. After you submit the pull request, we will check your patch on multiple operating systems.

## Contributing to documentation

We welcome contributions to our [documentation](https://docs.percona.com/percona-backup-mongodb).

Documentation source files are in the [dedicated docs repository](https://github.com/percona/pbm-docs). The contents of the `doc` folder is outdated and will be removed.

Please follow the [Docs contributing guidelines](https://github.com/percona/pbm-docs/blob/main/CONTRIBUTING.md) for how to contribute to documentation.

## After your pull request is merged

Once your pull request is merged, you are an official Percona Community Contributor. Welcome to the community!
