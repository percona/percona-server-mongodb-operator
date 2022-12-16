# Building and testing the Operator

## Requirements

You need to install a number of software packages on your system to satisfy the build dependencies for building the Operator and/or to run its automated tests:

* [kubectl](https://kubernetes.io/docs/tasks/tools/) - Kubernetes command-line tool
* [docker](https://www.docker.com/) - platform for developing, shipping, and running applications in containers
* [sed](https://www.gnu.org/software/sed/manual/sed.html) - CLI stream editor
* [helm](https://helm.sh/) - the package manager for Kubernetes
* [jq](https://stedolan.github.io/jq/) - command-line JSON processor
* [yq](https://github.com/mikefarah/yq) - command-line YAML processor
* [oc](https://docs.openshift.com/container-platform/4.7/cli_reference/openshift_cli/getting-started-cli.html) - Openshift command-line tool
* [gcloud](https://cloud.google.com/sdk/gcloud) - Google Cloud command-line tool

### CentOS

```
sudo yum -y install epel-release
sudo yum -y install coreutils sed jq curl docker
sudo curl -s -L https://github.com/mikefarah/yq/releases/download/v4.27.2/yq_linux_amd64 -o /usr/bin/yq
sudo chmod a+x /usr/bin/yq
curl -s -L https://github.com/openshift/origin/releases/download/v3.11.0/openshift-origin-client-tools-v3.11.0-0cbc58b-linux-64bit.tar.gz \
    | tar -C /usr/bin --strip-components 1 --wildcards -zxvpf - '*/oc' '*/kubectl'
curl -s https://get.helm.sh/helm-v3.2.4-linux-amd64.tar.gz \
    | tar -C /usr/bin --strip-components 1 -zxvpf - '*/helm'
curl https://sdk.cloud.google.com | bash
```

### Ubuntu

```
echo "deb [signed-by=/usr/share/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main" | sudo tee -a /etc/apt/sources.list.d/google-cloud-sdk.list
curl https://packages.cloud.google.com/apt/doc/apt-key.gpg | sudo apt-key --keyring /usr/share/keyrings/cloud.google.gpg add -
sudo apt-get update
sudo apt-get install -y google-cloud-sdk docker.io kubectl jq
sudo snap install helm --classic
sudo snap install yq
curl -s -L https://github.com/openshift/origin/releases/download/v3.11.0/openshift-origin-client-tools-v3.11.0-0cbc58b-linux-64bit.tar.gz | sudo tar -C /usr/bin --strip-components 1 --wildcards -zxvpf - '*/oc'
```

### MacOS

Install [Docker](https://docs.docker.com/docker-for-mac/install/), and run the following commands for the other required components:

```
brew install coreutils gnu-sed jq yq kubernetes-cli openshift-cli kubernetes-helm
curl https://sdk.cloud.google.com | bash
```

### Runtime requirements

Also, you need a Kubernetes platform of [supported version](https://www.percona.com/doc/kubernetes-operator-for-psmongodb/System-Requirements.html#officially-supported-platforms) to run the Operator in available clouds (install instructions [EKS](https://www.percona.com/doc/kubernetes-operator-for-psmongodb/eks.html), [GKE](https://www.percona.com/doc/kubernetes-operator-for-psmongodb/gke.html), [OpenShift](https://www.percona.com/doc/kubernetes-operator-for-psmongodb/openshift.html) or [minikube](https://www.percona.com/doc/kubernetes-operator-for-psmongodb/minikube.html) ).  Other Kubernetes flavors and versions depend on the backward compatibility offered by Kubernetes.

**Note:** there is no need to build an image if you are going to test some already-released version.

## Building and testing the Operator

There are scripts which build the image and run tests. Both building and testing
needs some repository for the newly created docker images. If nothing is
specified, scripts use Percona's experimental repository `perconalab/percona-server-mongodb-operator`, which
requires decent access rights to make a push.

To specify your own repository for the Percona Operator for MongoDB docker image, you can use IMAGE environment variable:

```
export IMAGE=bob/my_repository_for_test_images:K8SPSMDB-372-fix-feature-X
```
We use linux/amd64 platform by default. To specify another platform, you can use DOCKER_DEFAULT_PLATFORM environment variable

```
export DOCKER_DEFAULT_PLATFORM=linux/amd64
```
Use the following script to build the image:

```
./e2e-tests/build
```
Please note the following:

* You might need to add your user to the docker group or run the build as root to access the docker UNIX socket. 
* Make sure you have enabled the experimental features for docker by adding the following into /etc/docker/daemon.json:
```
{
    "experimental": true
}
```
* You need to be authorized to push the image to the registry of your choice

You can also build the image and run your cluster in one command:

```
./e2e-tests/build-and-run
```

Running all tests at once can be done with the following command:

```
./e2e-tests/run
```

(see how to configure the testing infrastructure [here](#using-environment-variables-to-customize-the-testing-process)).

Tests can also be run one-by-one using the appropriate scripts (their names should be self-explanatory):

```
./e2e-tests/init-deploy/run
./e2e-tests/arbiter/run
./e2e-tests/limits/run
./e2e-tests/scaling/run
./e2e-tests/demand-backup/run
./e2e-tests/scheduled-backup/run
./e2e-tests/storage/run
./e2e-tests/self-healing/run
./e2e-tests/operator-self-healing/run
....
```

Test execution produces excessive output. It is recommended to redirect the output to the file and analyze it later:
```
./e2e-tests/run >> /tmp/tests-run.out 2>&1
```

## Using environment variables to customize the testing process

### Re-declaring default image names

You can use environment variables to re-declare all default images used for testing. The
full list of variables is the following one:

* `IMAGE` - Percona Server for MongoDB Operator, `perconalab/percona-server-mongodb-operator:main` by default,
* `IMAGE_MONGOD` - mongod, `perconalab/percona-server-mongodb-operator:main-mongod4.4` by default,
* `IMAGE_PMM` - Percona Monitoring and Management (PMM) client, `perconalab/pmm-client:dev-latest` by default,
* `IMAGE_BACKUP` - backup, `perconalab/percona-server-mongodb-operator:main-backup` by default,

### Using automatic clean-up after testing

By default, each test creates its own namespace and does not clean up objects in case of failure.

To avoid manual deletion of such leftovers, you can run tests on a separate cluster and use the following environment variable to make the ultimate clean-up:

```
export CLEAN_NAMESPACE=1
```

**Note:** this will cause **deleting all namespaces** except default and system ones!

### Skipping backup tests on S3-compatible storage

Making backups [on S3-compatible storage](https://www.percona.com/doc/kubernetes-operator-for-psmongodb/backups.html#making-scheduled-backups) needs creating Secrets to have the access to the S3 buckets. There is an environment variable enabled by default, which skips all tests requiring such Secrets:

```
SKIP_BACKUPS_TO_AWS_GCP_AZURE=1
```

The backups tests will use only [MinIO](https://min.io/) if this variable is declared,
which is enough for local testing.
