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

The Makefile downloads the OLM helper tools into
`installers/olm/tools/<os>-<arch>`:

```bash
make tools
```

Downloaded tools:

- `jq`
- `operator-sdk`
- `opm`

## Variables

Most workflows only need `VERSION`.

```bash
export VERSION=1.23.0
```

Useful optional variables:

| Variable | Description | Example |
| --- | --- | --- |
| `CSV_VERSION` | CSV version. Defaults to the first `x.y.z` parsed from `VERSION`. | `1.23.0` |
| `IMAGE` | Community operator image used in generated manifests. | `docker.io/percona/percona-server-mongodb-operator:1.23.0` |
| `REDHAT_OPERATOR_IMAGE` | Certified operator image used in generated manifests. | `registry.connect.redhat.com/percona/percona-server-mongodb-operator:1.23.0` |
| `DEV_BUNDLE_REPO` | Development bundle and catalog image repository. | `docker.io/perconalab/percona-server-mongodb-operator` |
| `CONFIRM_PUSH` | Ask before pushing images. Set to `0` in CI. | `0` |
| `SKIP_DIGEST_FAILURE` | Continue certified generation when a digest cannot be resolved. Missing digests are rendered as `<DIGEST>`. | `1` |

OpenShift versions are resolved from `../../e2e-tests/release_versions` and used
to render `com.redhat.openshift.versions`. Override only when needed:

```bash
export OPENSHIFT_VERSIONS="v4.18-v4.22"
```

## Development

Development targets publish bundle and catalog images to `perconalab`.

The development package names are suffixed with `-dev` to avoid colliding with
public OperatorHub packages already present in OpenShift default catalogs:

- `percona-server-mongodb-operator-dev`
- `percona-server-mongodb-operator-certified-dev`

The display name is suffixed with `(Dev)` in the OpenShift console.

Build and push development bundle images:

```bash
make bundle-dev VERSION=1.23.0
make bundle-dev/community VERSION=1.23.0
make bundle-dev/certified VERSION=1.23.0
```

Deploy development catalogs to OpenShift:

```bash
make deploy-dev VERSION=1.23.0
make deploy-dev/community VERSION=1.23.0
make deploy-dev/certified VERSION=1.23.0
```

After deploying, search the OpenShift console for:

```bash
Percona Distribution for MongoDB Operator (Dev)
```

Or check with:

```bash
kubectl get packagemanifest percona-server-mongodb-operator-dev -n openshift-marketplace
kubectl get packagemanifest percona-server-mongodb-operator-certified-dev -n openshift-marketplace
```

## Release Example

Generate release bundles locally:

```bash
make bundles VERSION=1.23.0
make bundle/community VERSION=1.23.0
make bundle/certified VERSION=1.23.0
```

Build, push, and deploy release catalogs:

```bash
make deploy VERSION=1.23.0
make deploy/community VERSION=1.23.0
make deploy/certified VERSION=1.23.0
```

Release deploy runs the full flow for each bundle type:

```bash
make bundle/<bundle-type>
./build-image.sh ${CONTAINER} bundles/<bundle-type> <bundle-type> ${BUNDLE_IMAGE_VERSION}
make catalog/<bundle-type> BUNDLE_REPO=${DEPLOY_BUNDLE_REPO}
make apply-catalog/<bundle-type>
```

## Validation

Validate all generated bundles:

```bash
make validate VERSION=1.23.0
```

Validate one bundle:

```bash
make validate/community VERSION=1.23.0
make validate/certified VERSION=1.23.0
```

Validation uses:

- `validate-image.sh`
- `validate-directory.sh`

## Certified Metadata

Certified bundles resolve image metadata and related image digests through
`distributions/redhat.sh`.

Bundle generation fails when:

- a required image is missing
- a certified image tag does not match the expected pattern
- a required digest cannot be resolved and `SKIP_DIGEST_FAILURE` is not enabled

With `SKIP_DIGEST_FAILURE=1`, missing digests are rendered as `<DIGEST>` and
reported in the build output.

## Cleanup

Remove generated bundles, catalogs, temporary SDK projects, and downloaded tools:

```bash
make clean
```

Show available targets:

```bash
make help
```
