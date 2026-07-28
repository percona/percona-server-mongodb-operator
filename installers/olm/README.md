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

Most workflows only require `VERSION`.

```bash
export VERSION=1.23.0
```

Useful optional variables:

| Variable | Description | Example |
| --- | --- | --- |
| `CSV_VERSION` | CSV version. Defaults to the first `x.y.z` parsed from `VERSION`. | `1.23.0` |
| `REGISTRY` | Registry used for operator, bundle, and catalog images. | `docker.io` |
| `PROD_REPOSITORY` | Repository namespace used for production bundle images. | `percona` |
| `DEV_REPOSITORY` | Repository namespace used for development bundle images and all catalog images. | `perconalab` |
| `DEV_BUNDLE_REPO` | Repository used by development bundle images. | `docker.io/perconalab/percona-server-mongodb-operator` |
| `PROD_BUNDLE_REPO` | Repository used by production bundle images. | `docker.io/percona/percona-server-mongodb-operator` |
| `BUNDLE_BUILD_PUSH` | Build and push bundle images when enabled. | `1` |
| `CONFIRM_BUILD_PUSH` | Ask for confirmation before pushing bundle and catalog images. Set to `0` for non-interactive runs. | `1` |
| `DEV_CATALOG_BUNDLE_TAG_SUFFIX` | Tag suffix used for previous OperatorHub bundle images built as development catalog inputs. The current release bundle keeps the normal bundle tag. | `dev-catalog` |
| `DEV_CATALOG_TAG_SUFFIX` | Tag suffix used for catalog images. Catalog images are always development images. | `dev-catalog` |
| `CATALOG_BUNDLE_LIMIT` | Number of previous OperatorHub bundle versions to include in rendered catalogs, in addition to the current release bundle. | `2` |
| `CATALOG_BUILD_PUSH` | Build and push catalog images during deploy targets when enabled. | `1` |
| `GITHUB_TOKEN` / `GH_TOKEN` | Optional token for GitHub API requests when building catalogs. | `<token>` |
| `SKIP_DIGEST_FAILURE` | Continue certified generation when a non-required digest cannot be resolved. Missing digests are rendered as `<DIGEST>`. | `1` |

OpenShift versions are resolved from `../../e2e-tests/release_versions` and used
to render `com.redhat.openshift.versions`.

Override only when necessary:

```bash
export OPENSHIFT_VERSIONS="v4.18-v4.22"
```

---

# Development

Development targets generate bundle directories and build bundle images. Bundle
image push is controlled by `BUNDLE_BUILD_PUSH` and defaults to enabled.

Development catalog sources are suffixed with `-dev`, but the package names keep
the production names.

Packages:

- `percona-server-mongodb-operator`
- `percona-server-mongodb-operator-certified`

Catalog sources:

- `community-dev`
- `certified-dev`

## Build bundles

```bash
make bundles VERSION=1.23.0
make bundle/community VERSION=1.23.0
make bundle/certified VERSION=1.23.0
```

Build locally without pushing:

```bash
make bundle/community VERSION=1.23.0 BUNDLE_BUILD_PUSH=0
```

## Build catalogs

Catalog build targets first generate, build, and push the current release bundle
image to `DEV_BUNDLE_REPO` with the normal bundle tag, then render and push the
catalog image to `DEV_BUNDLE_REPO` with the `DEV_CATALOG_TAG_SUFFIX` suffix.

For the latest `CATALOG_BUNDLE_LIMIT` previous versions already published in
OperatorHub, `build-catalog.sh` downloads the bundle manifests from GitHub,
builds bundle images with the `DEV_CATALOG_BUNDLE_TAG_SUFFIX`, and pushes them
before rendering the catalog.

The current bundle is rendered into the default package channel from
`PACKAGE_CHANNEL` (`stable` by default). Previous OperatorHub versions are
rendered into versioned channels such as `stable-v1.22` so the OpenShift
Console can show version-specific catalog metadata.

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
  DEV_REPOSITORY=valmiranogueira \
  CATALOG_NAMESPACE=olm
```

This publishes images such as:

```text
docker.io/valmiranogueira/percona-server-mongodb-operator:1.23.0-community-bundle
docker.io/valmiranogueira/percona-server-mongodb-operator:1.22.0-community-bundle-dev-catalog
docker.io/valmiranogueira/percona-server-mongodb-operator:community-dev-catalog
```

## Deploy catalogs

Deploy automatically enables image push (with confirmation when enabled).

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
`build-catalog.sh build-push` when `CATALOG_BUILD_PUSH=1`. Catalog images always
use `DEV_BUNDLE_REPO` with the `DEV_CATALOG_TAG_SUFFIX` suffix; production
deploys only switch the bundle image repository to `PROD_BUNDLE_REPO`.

```bash
make deploy-prod/community VERSION=1.23.0
make deploy-prod/certified VERSION=1.23.0
make deploy-prod VERSION=1.23.0
```

Each deploy performs the complete release workflow:

```text
generate production bundle
    ↓
build and push production bundle image
    ↓
build and push development catalog image pointing to the production bundle
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

# Certified Metadata

Certified bundles resolve image metadata and related image digests through
`distributions/redhat.sh`.

The public certified bundle type maps to the internal `redhat` distribution.
Generated certified bundle files are written under:

```text
installers/olm/bundles/redhat
```

The bundle image tag still uses the public `certified` name, for example:

```text
<PROD_BUNDLE_REPO>:1.23.0-certified-bundle
```

Bundle generation fails when:

- a required image is missing;
- a certified image tag does not match the expected pattern;
- the required `clustersync` Red Hat tag digest cannot be resolved.

When `SKIP_DIGEST_FAILURE=1`, missing non-`clustersync` digests are rendered as
`<DIGEST>` and reported in the build output. The `clustersync` digest is always
resolved strictly so a missing `registry.connect.redhat.com/...:<VERSION>-clustersync`
tag stops generation immediately.

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
