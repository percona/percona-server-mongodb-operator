# Percona Server for MongoDB Operator OLM bundles

This directory contains the automation used to generate, validate, build, publish,
and deploy OLM bundle content for the Percona Server for MongoDB Operator.

Two bundle types are supported:

- `community`
- `certified`

Bundles are generated for namespace-scoped installation. Certified bundles keep
`MultiNamespace` and `AllNamespaces` unsupported, matching the current certified
OperatorHub bundle style.

## Requirements

Install the host tools checked by `make`:

```bash
gawk
gcsplit
yq
yamllint
envsubst
kubectl
docker
```

The Makefile downloads the OLM helper tools into:

```text
installers/olm/tools/<os>-<arch>
```

```bash
make tools
```

Downloaded tools:

- `jq`
- `operator-sdk`
- `opm`

## Variables

Most workflows only require `VERSION`. It defaults to the branch name on a
release branch (for example `release-1.25.0`), and otherwise to the operator
version in `../../pkg/version/version.txt`.

```bash
export VERSION=1.23.0
```

Useful optional variables:

| Variable | Description | Example |
| --- | --- | --- |
| `CSV_VERSION` | CSV version. Defaults to the `x.y.z` parsed from `VERSION`. | `1.23.0` |
| `REGISTRY` | Registry used for operator, bundle, and catalog images. | `docker.io` |
| `PROD_REPOSITORY` | Repository namespace used for production bundle images. | `percona` |
| `DEV_REPOSITORY` | Repository namespace used for development bundle images and all catalog images. | `perconalab` |
| `BUNDLE_DEV_REPO` | Repository used by development bundle images. | `docker.io/perconalab/percona-server-mongodb-operator` |
| `BUNDLE_PROD_REPO` | Repository used by production bundle images. | `docker.io/percona/percona-server-mongodb-operator` |
| `BUNDLE_PLATFORM` | Platform used when building current bundle images locally. | `linux/amd64` |
| `BUNDLE_PACKAGE_CHANNEL` | Package channel used for the generated current bundle. | `stable` |
| `CONFIRM_PUSH` | Ask for confirmation before pushing bundle and catalog images. Set to `0` for non-interactive runs. | `1` |
| `CATALOG_BUNDLE_LIMIT` | Number of previous OperatorHub bundle versions to include in rendered catalogs, in addition to the current release bundle. Ignored when `CATALOG_PREVIOUS_MINORS` is set. | `2` |
| `CATALOG_PREVIOUS_MINORS` | Include every previous OperatorHub version of the N newest minor lines older than the current one, all patches included, plus older patches of the current minor line, instead of a fixed number of versions. Versions newer than the current one are ignored. Empty by default. | `2` |
| `CATALOG_PLATFORM` | Platform used when building previous OperatorHub bundle images and catalog images. | `linux/amd64` |
| `CATALOG_BUILD_PUSH` | Build and push catalog images during deploy targets when enabled. | `1` |
| `BUNDLE_SKIP_DIGEST_FAILURE` | Continue certified bundle generation when a non-required digest cannot be resolved. Missing digests are rendered as `<DIGEST>`. | `1` |
| `BUNDLE_DEV_IMAGES` | Set by the development targets (`bundle/<type>`, `deploy/<type>`): a certified image whose Red Hat digest cannot be resolved is replaced by the development image from `../../e2e-tests/release_versions`, so the example CR of an unreleased version is deployable. Production targets leave it unset. | `1` |

OpenShift versions are resolved from `../../e2e-tests/release_versions` and used
to render `com.redhat.openshift.versions`.

Override only when necessary:

```bash
export OPENSHIFT_VERSIONS="v4.18-v4.22"
```

---

# Development

Development bundle targets generate bundle directories only. Build bundle
images explicitly with `make build/<type>` and push existing local images with
`make push/<type>`.

Development catalog sources use `CATALOG_ENV=dev` by default, while package
names keep the production names. Release targets override `CATALOG_ENV=prod`.

Packages:

- `percona-server-mongodb-operator`
- `percona-server-mongodb-operator-certified`

Catalog sources:

- `community-dev`
- `certified-dev`

## Generate bundles

```bash
make bundles VERSION=1.23.0
make bundle/community VERSION=1.23.0
make bundle/certified VERSION=1.23.0
```

## Build bundle images

Build a generated bundle image locally:

```bash
make build/community VERSION=1.23.0
make build/certified VERSION=1.23.0
```

## Push bundle images

Push an existing generated bundle image:

```bash
make push/community VERSION=1.23.0
make push/certified VERSION=1.23.0
```

The manual development bundle workflow is:

```text
make bundle/community
    ↓
make build/community
    ↓
make push/community
```

## Build catalogs

Catalog build targets render and push the catalog image to `BUNDLE_DEV_REPO`
with the `-dev-catalog` suffix. The current release bundle image must already
exist in `BUNDLE_DEV_REPO` with the normal bundle tag.

For the latest `CATALOG_BUNDLE_LIMIT` previous versions already published in
OperatorHub, `build-catalog.sh` downloads the bundle manifests from the GitHub
community or certified OperatorHub repositories, builds bundle images with the
`-dev-catalog` suffix, and pushes them before rendering the catalog.

With the default `CATALOG_BUNDLE_LIMIT=2`, each catalog contains:

```text
current release bundle
latest previous GitHub OperatorHub bundle
second latest previous GitHub OperatorHub bundle
```

For example, with `VERSION=1.23.0` and previous GitHub versions `1.22.0` and
`1.21.2`, the community catalog uses:

```text
docker.io/perconalab/percona-server-mongodb-operator:1.23.0-community-bundle
docker.io/perconalab/percona-server-mongodb-operator:1.22.0-community-bundle-dev-catalog
docker.io/perconalab/percona-server-mongodb-operator:1.21.2-community-bundle-dev-catalog
```

To include whole minor lines instead, set `CATALOG_PREVIOUS_MINORS`. With
`VERSION=1.24.0`, `CATALOG_PREVIOUS_MINORS=2` and previous GitHub versions
`1.21.2`, `1.22.0`, `1.23.0` and `1.23.1`, the catalog gets `1.22.0`, `1.23.0`
and `1.23.1`. The build stops when OperatorHub has fewer minor lines than
requested.

The certified catalog follows the same pattern:

```text
docker.io/perconalab/percona-server-mongodb-operator:1.23.0-certified-bundle
docker.io/perconalab/percona-server-mongodb-operator:1.22.0-certified-bundle-dev-catalog
docker.io/perconalab/percona-server-mongodb-operator:1.21.2-certified-bundle-dev-catalog
```

All bundles are rendered into one channel, as in the published catalogs.
`BUNDLE_PACKAGE_CHANNEL` sets its name (`stable` by default). Each version
skips every older one (`olm.skipRange` in community catalogs, `spec.skips` in
certified ones), so OLM upgrades any older version straight to the newest.

```bash
make catalog-build-push/community VERSION=1.23.0
make catalog-build-push/certified VERSION=1.23.0
```

## Personal catalog testing

Override `DEV_REPOSITORY` to build and push development bundles and catalog
images to a personal repository namespace. Use `CATALOG_NAMESPACE=olm` on
clusters where the OpenShift console reads the software catalog from `olm`.

```bash
make deploy/community \
  VERSION=1.23.0 \
  CSV_VERSION=1.23.0 \
  DEV_REPOSITORY=my-repository \
  CATALOG_NAMESPACE=olm
```

This publishes images such as:

```text
docker.io/my-repository/percona-server-mongodb-operator:1.23.0-community-bundle
docker.io/my-repository/percona-server-mongodb-operator:1.22.0-community-bundle-dev-catalog
docker.io/my-repository/percona-server-mongodb-operator:community-dev-catalog
```

## Deploy catalogs

Deploy generates the bundle, builds and pushes the current bundle image, builds
and pushes the catalog image, and applies the CatalogSource.

```bash
make deploy/community VERSION=1.23.0
make deploy/certified VERSION=1.23.0
make deploy VERSION=1.23.0
```

After deployment, verify the packages:

```bash
kubectl get packagemanifest percona-server-mongodb-operator -n openshift-marketplace
kubectl get packagemanifest percona-server-mongodb-operator-certified -n openshift-marketplace
```

or search in the OpenShift console for:

```text
Percona Distribution for MongoDB Operator
```

---

# Release (`*-prod`)

Release targets use the `-prod` suffix.

## Generate release bundles

```bash
make bundles-prod VERSION=1.23.0
make bundle-prod/community VERSION=1.23.0
make bundle-prod/certified VERSION=1.23.0
```

## Deploy release catalogs

Release deploy targets generate production bundles, build and push production
bundle images, and build and push catalog images through
`build-catalog.sh build` when `CATALOG_BUILD_PUSH=1`. Catalog images use
`BUNDLE_DEV_REPO` with the `-prod-catalog` suffix; production deploys switch the
bundle image repository to `BUNDLE_PROD_REPO`.

```bash
make deploy-prod/community VERSION=1.23.0
make deploy-prod/certified VERSION=1.23.0
make deploy-prod VERSION=1.23.0
```

Build a production bundle image manually:

```bash
make build-prod BUNDLE_TYPE=community VERSION=1.23.0
make build-prod BUNDLE_TYPE=certified VERSION=1.23.0
```

Push a production bundle image manually:

```bash
make push-prod BUNDLE_TYPE=community VERSION=1.23.0
make push-prod BUNDLE_TYPE=certified VERSION=1.23.0
```

Each deploy performs the complete release workflow:

```text
generate production bundle
    ↓
build production bundle image
    ↓
push production bundle image
    ↓
build and push catalog image pointing to the production bundle
    ↓
apply CatalogSource
```

Unlike the development targets, release bundles use the production package names:

- `percona-server-mongodb-operator`
- `percona-server-mongodb-operator-certified`

---

# Validation

Validate every generated bundle:

```bash
make validate VERSION=1.23.0
```

Or validate a single bundle:

```bash
make validate/community VERSION=1.23.0
make validate/certified VERSION=1.23.0
```

Validation uses:

- `validate-image.sh`
- `validate-directory.sh`

---

# OLM tests

`make tests` runs the pytest tests in `e2e-tests/` (in this directory) against
the deployed development catalogs. Deploy them first with `make deploy/<type>`,
and point `kubectl` and `oc` at the OpenShift cluster (the shared harness
creates the test project with `oc`; `helm`, `jq` and `uv` are also required).
Build the catalog with `CATALOG_PREVIOUS_MINORS=2` to test upgrades from the
two previous minor versions and all their patch releases.

```bash
make deploy CATALOG_PREVIOUS_MINORS=2
make tests
make tests/community
make tests/certified
```

`make tests` runs `tests/community` and then `tests/certified`, one at a time
(also under `make -j`), because each run deletes the cluster-wide operator CRDs.
Each one first waits for the CatalogSource to be `READY` and to serve the
package (`OLM_CATALOG_TIMEOUT`, 300 seconds), so it can run right after
`make deploy/<type>`. The target is the head of the default channel, or of
`OLM_TARGET_CHANNEL`. `OLM_TESTS` (default
`olm-default-install olm-backup-restore olm-upgrade`) selects the tests.
Reports are written to `e2e-tests/reports/<test>-<csv>.*` in the repository
root. The runner copies the current kubectl context into a temporary
kubeconfig and uses it for every run, so a context switch in
`~/.kube/config` during the runs (a cluster deploy, another test run) does not
move them to another cluster. A run whose steps were all skipped counts as
failed.

For a dev catalog, every test checks that the head of the default channel is
`OPERATOR_VERSION`, the `crVersion` in `deploy/cr.yaml`, and fails early when
the catalog is stale. Set `OLM_TARGET_VERSION` to expect another version. The
check is skipped when `OLM_TARGET_CHANNEL` picks another channel.

## Tests

| Test | What it checks | Run only this test |
| ---- | -------------- | ------------------ |
| `olm-default-install` | Installs the operator and creates the cluster from the CSV's default example, the one the OpenShift console prefills on Create. Writes data, then checks that the cluster stays stable and the operator logs no errors. | `make tests OLM_TESTS=olm-default-install` |
| `olm-backup-restore` | Takes a logical and a physical backup and restores each one: every member must return the data as of the backup. Scans the logs and objects for leaked storage credentials. | `make tests OLM_TESTS=olm-backup-restore` |
| `olm-upgrade` | Upgrades every older bundle of the catalog straight to the target, then checks the operator, the cluster, the data and a restore of a backup taken before the upgrade. | `make tests OLM_TESTS=olm-upgrade` |

`make tests` runs each test against both catalogs; use `make tests/community`
or `make tests/certified` for one. `OLM_TESTS` takes several names separated by
spaces. The sections below describe each test in detail.

### olm-default-install

Runs once per bundle type. It installs the target CSV with a Manual-approval
Subscription and applies the first `alm-examples` entry of the CSV unchanged:
the PerconaServerMongoDB that the OpenShift console prefills when a user clicks
Create. Today that is a sharded cluster (`rs0`, `cfg` and `mongos`, three pods
each, with hostname anti-affinity) with backups enabled and no storage. The
test fails at the start when the cluster has fewer than three schedulable
worker nodes. It waits up to 15 minutes for `ready` with every component ready,
and fails earlier when the cluster stays in `error` for 2 minutes (the operator
retries an error) or cannot be read 6 times in a row. It then writes a document
through the mongos Service and reads it back. It changes
`spec.upgradeOptions.schedule` and waits for `status.observedGeneration`, so the
operator has reconciled the cluster before the final checks: once the cluster
is ready, no DB pod may be replaced or restarted and no Warning event may come
up, and the operator pod must not restart or log errors. The one exception is
`no ready pods to get pbm-agent version`, which the operator logs at ERROR
level while no backup agent is ready yet: it is allowed from applying the
example until the cluster is ready.

### olm-backup-restore

Runs once per bundle type. It installs the target CSV with a Manual-approval
Subscription, deploys SeaweedFS (the S3 storage of the shared harness) in the
test project and a replica set with backups from the bundle's example CR, and
writes data. It then takes a logical and a physical backup, built from the
bundle's example objects. Before each restore it writes one more document;
after the restore every member (direct connection to each pod) must return only
the data as of the backup, and the cluster must accept writes again. For the
physical restore it also waits for the `percona.com/resync-pbm` annotation, the
StatefulSet rebuild and the PBM resync. The operator pod must not restart or
log errors during the run, with two exceptions. The `FULL CLUSTER CRASH` line,
which the operator logs while the physical restore stops every mongod, is
allowed at most ten times, and only between the start and the end of the
physical restore. `no ready pods to get pbm-agent version` is allowed while the
cluster starts and during the physical restore (see `olm-default-install`).

The logs of the operator and database pods, the backup and restore objects,
the events and the cluster object are scanned for the storage credentials and
for credential-shaped strings (`e2e-tests/lib/secret_scan.py`) before the
physical restore and at the end of the run. A finding fails the test with the
source, line and rule, with the match masked. The operator replaces the
database pods when the physical restore completes, so their logs are read
every 15 seconds during the restore, and the logs of the replaced pods are
scanned at the end too. The final scan also runs when a backup or restore step
fails.

### olm-upgrade

Runs once for every bundle in the catalog older than the target. Each run
installs that bundle with a Manual-approval Subscription (`startingCSV`) on the
target's channel, deploys a replica set with backups from the bundle's example
CR, writes data and takes a logical backup. It then approves only the
InstallPlan straight to the target and deletes any other pending one, such as
one for a newer patch, so each starting version is upgraded directly, without
changing the channel, as users do. In a catalog with a channel per minor
version, the test moves the Subscription to the target's channel first. After
the upgrade it checks that:

- only the target CSV is left and the operator runs its image;
- the operator pod stays Ready and is not replaced or restarted, and its logs
  and the namespace's Warning events have no new errors, both right after the
  upgrade and after a 3-minute soak;
- the operator reconciled the cluster (`status.observedGeneration`), the cluster
  stayed `ready`, and no DB pod was recreated or restarted;
- the data written before the upgrade is readable on every member (direct
  connection to each pod), and new writes succeed;
- the target operator restores the backup taken by the starting one: every
  member returns only the pre-upgrade data, and new writes succeed.

`make tests` fails when the catalog holds fewer than two previous minor
versions, not counting the target's own minor line. Set `OLM_MIN_MINORS` to
change that number. Set `OLM_FROM_CSVS` to a space-separated list of CSV names
to choose the starting bundles.

## Running by hand and in a pipeline

OLM tests live in `installers/olm/e2e-tests/` and are not part of the regular
e2e suite: they need an OpenShift cluster with the catalogs deployed, and
changes here do not trigger the PR e2e run (`installers/olm/**` is in
`.e2eignore`). They reuse the shared pytest harness from the repository's
`e2e-tests/` (`conftest.py` and `lib/`) unchanged, loaded with `-p conftest`;
each test overrides `test_paths` for its location, and `olm.py` holds the OLM
helpers. `make py-check` in the repository root resolves their fixtures without
a cluster (`make py-setup-plan`) and type-checks them, so a fixture or helper
renamed in the shared harness fails CI. To run one test by hand from the
repository root:

```bash
SKIP_DELETE=0 PYTHONPATH=e2e-tests:installers/olm/e2e-tests \
  uv run pytest -p conftest installers/olm/e2e-tests/olm-backup-restore/
```

The shared `conftest.py` defaults to `SKIP_DELETE=1`, so a manual run without
`SKIP_DELETE=0` keeps its namespace, operator and CRDs. `make tests` cleans up
after every run unless you set `SKIP_DELETE=1`.

Before each run, the test deletes the operator CRDs, together with every `psmdb`
resource in the cluster, and with `SKIP_DELETE=0` it deletes them again after
the run. OLM leaves CRDs behind, and a newer CRD blocks installing an older
bundle. Use a dedicated cluster. Run `make tests SKIP_DELETE=1` to keep the
last run's resources for debugging; earlier runs are always cleaned up, since
the next run deletes the CRDs anyway.

A scheduled job (an OpenShift cluster in the current `kubectl` and `oc`
context, push rights to `DEV_REPOSITORY`) builds the catalogs and runs every
test with one command from the repository root:

```bash
make -C installers/olm deploy tests CATALOG_PREVIOUS_MINORS=2 CONFIRM_PUSH=0
```

It exits non-zero when any run fails and leaves one JUnit report per run in
`e2e-tests/reports/`.

---

# Certified Metadata

Certified bundles resolve image metadata and related image digests through
`distributions/certified.sh`.

Generated certified bundle files are written under:

```text
installers/olm/bundles/certified
```

The bundle image tag still uses the public `certified` name, for example:

```text
<BUNDLE_PROD_REPO>:1.23.0-certified-bundle
```

Bundle generation fails when:

- a required image is missing;
- a certified image tag does not match the expected pattern;
- the required `clustersync` tag is not found in the Red Hat repository.

Development targets set `BUNDLE_DEV_IMAGES=1`: an image whose digest cannot be
resolved, which is every image of an unreleased version, is replaced by the
development image from `../../e2e-tests/release_versions`, so the dev catalog's
example CR deploys and the OLM tests can run against it. Production targets
fail instead.

When `BUNDLE_SKIP_DIGEST_FAILURE=1`, missing non-`clustersync` digests are rendered as
`<DIGEST>` and reported in the build output. The `clustersync` tag is always
checked strictly, so generation stops immediately when
`registry.connect.redhat.com/...:<VERSION>-clustersync` is not found in the
Red Hat repository.

---

# Cleanup

Remove generated bundles, catalogs, temporary SDK projects, and downloaded
tools:

```bash
make clean
```

Display all available targets:

```bash
make help
```
