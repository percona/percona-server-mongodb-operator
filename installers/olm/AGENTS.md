# OLM bundles and catalogs: notes for coding agents

Read the root [`AGENTS.md`](../../AGENTS.md) first. Usage, targets and
variables are in [`README.md`](README.md); keep it the source of truth and
update it with any change here.

## What lives here

- `generate.sh` renders a bundle (CSV, CRDs, metadata) into
  `bundles/<community|certified>`; `distributions/<type>.sh` holds what
  differs per bundle type (images, `olm.skipRange` or `spec.skips`).
- `build-bundle.sh` builds and pushes bundle images.
- `build-catalog.sh` downloads previous bundles from the OperatorHub GitHub
  repositories, builds catalog images and applies the CatalogSource.
- `Makefile` wraps these; `make help` lists the targets.

Vocabulary: a **bundle** is one operator version as an image, its main
manifest is the **CSV**, a **catalog** indexes bundles, a **channel** is an
upgrade stream inside it (`stable-vX.Y`), and a cluster installs from a
catalog through a **Subscription** and **InstallPlans**.

## Rules

- Versions come from `VERSION`/`CSV_VERSION`; `CSV_VERSION` must be a plain
  `x.y.z`. Do not hardcode a release number in scripts.
- Dev targets push to `DEV_REPOSITORY` (default `perconalab`). Use a personal
  namespace for experiments, `make deploy/community DEV_REPOSITORY=<you>`, and
  never point dev targets at `percona`. Keep `CONFIRM_PUSH=1` for interactive
  runs.
- Do not change what existing catalogs contain by default: add a variable and
  leave the old behavior when it is unset.
- Scripts run under `set -euo pipefail` on Linux and on macOS (bash 3.2, BSD
  `awk`/`sed`, GNU make 3.81). An empty array is "unbound" under `set -u` in
  bash 3.2: check its length before expanding it. Test version-selection logic
  with both BSD awk and gawk.
- On macOS, `opm` needs `~/.config/containers/policy.json` to pull bundles;
  Linux ships it in `/etc/containers/policy.json`.
- Run `make validate` after changing bundle generation.
