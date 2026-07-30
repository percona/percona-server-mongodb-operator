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
| `BUNDLE_DEV_REPO` | Repository used by development bundle images. | `docker.io/perconalab/percona-server-mongodb-operator` |
| `BUNDLE_PROD_REPO` | Repository used by production bundle images. | `docker.io/percona/percona-server-mongodb-operator` |
| `BUNDLE_PLATFORM` | Platform used when building current bundle images locally. | `linux/amd64` |
| `BUNDLE_PACKAGE_CHANNEL` | Package channel used for the generated current bundle. | `stable` |
| `CONFIRM_PUSH` | Ask for confirmation before pushing bundle and catalog images. Set to `0` for non-interactive runs. | `1` |
| `CATALOG_BUNDLE_LIMIT` | Number of previous OperatorHub bundle versions to include in rendered catalogs, in addition to the current release bundle. | `2` |
| `CATALOG_PLATFORM` | Platform used when building previous OperatorHub bundle images and catalog images. | `linux/amd64` |
| `CATALOG_BUILD_PUSH` | Build and push catalog images during deploy targets when enabled. | `1` |
| `BUNDLE_SKIP_DIGEST_FAILURE` | Continue certified bundle generation when a non-required digest cannot be resolved. Missing digests are rendered as `<DIGEST>`. | `1` |

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

The certified catalog follows the same pattern:

```text
docker.io/perconalab/percona-server-mongodb-operator:1.23.0-certified-bundle
docker.io/perconalab/percona-server-mongodb-operator:1.22.0-certified-bundle-dev-catalog
docker.io/perconalab/percona-server-mongodb-operator:1.21.2-certified-bundle-dev-catalog
```

All bundles are rendered into their own versioned channel. `BUNDLE_PACKAGE_CHANNEL`
sets the channel prefix (`stable` by default), so versions are rendered into
channels such as `stable-v1.23`, `stable-v1.22`, and `stable-v1.21`.

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
<BUNDLE_PROD_REPO>:1.23.0-certified-bundle
```

Bundle generation fails when:

- a required image is missing;
- a certified image tag does not match the expected pattern;
- the required `clustersync` tag is not found in the Red Hat repository.

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
