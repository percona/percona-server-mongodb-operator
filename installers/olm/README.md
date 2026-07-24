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
| `IMAGE` | Community operator image used in generated manifests. | `docker.io/percona/percona-server-mongodb-operator:1.23.0` |
| `REDHAT_OPERATOR_IMAGE` | Certified operator image used in generated manifests. | `registry.connect.redhat.com/percona/percona-server-mongodb-operator:1.23.0` |
| `DEV_BUNDLE_REPO` | Repository used by development bundle and catalog images. | `docker.io/perconalab/percona-server-mongodb-operator` |
| `DEPLOY_BUNDLE_REPO` | Repository used by release bundle and catalog images. | `docker.io/percona/percona-server-mongodb-operator` |
| `CONFIRM_PUSH` | Ask before pushing images. Development targets default to local builds; deploy targets enable push automatically. Set to `0` in CI. | `0` |
| `SKIP_DIGEST_FAILURE` | Continue certified generation when a digest cannot be resolved. Missing digests are rendered as `<DIGEST>`. | `1` |

OpenShift versions are resolved from `../../e2e-tests/release_versions` and used
to render `com.redhat.openshift.versions`.

Override only when necessary:

```bash
export OPENSHIFT_VERSIONS="v4.18-v4.22"
```

---

# Development

Development targets build local bundles and catalogs by default. Images are only
pushed when running a deploy target (or when explicitly enabling push).

Development package names are suffixed with `-dev` to avoid colliding with the
public OperatorHub packages shipped with OpenShift.

Packages:

- `percona-server-mongodb-operator-dev`
- `percona-server-mongodb-operator-certified-dev`

Display names are suffixed with **(Dev)**.

## Generate bundles

```bash
make bundles VERSION=1.23.0
make bundle/community VERSION=1.23.0
make bundle/certified VERSION=1.23.0
```

## Build catalogs

```bash
make catalog/community VERSION=1.23.0
make catalog/certified VERSION=1.23.0
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
kubectl get packagemanifest percona-server-mongodb-operator-dev -n openshift-marketplace
kubectl get packagemanifest percona-server-mongodb-operator-certified-dev -n openshift-marketplace
```

or search in the OpenShift console for:

```text
Percona Distribution for MongoDB Operator (Dev)
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

## Build release catalogs

```bash
make catalog-prod/community VERSION=1.23.0
make catalog-prod/certified VERSION=1.23.0
```

## Deploy release catalogs

```bash
make deploy-prod/community VERSION=1.23.0
make deploy-prod/certified VERSION=1.23.0
make deploy-prod VERSION=1.23.0
```

Each deploy performs the complete release workflow:

```text
bundle-prod/*
    ↓
build-image.sh
    ↓
catalog-prod/*
    ↓
apply-catalog/*
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

Bundle generation fails when:

- a required image is missing;
- a certified image tag does not match the expected pattern;
- a required digest cannot be resolved and `SKIP_DIGEST_FAILURE` is disabled.

When `SKIP_DIGEST_FAILURE=1`, missing digests are rendered as `<DIGEST>` and
reported in the build output.

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
