# Go style

Last verified: 2026-11-03

What CI enforces and the conventions the code base follows. New code follows
the dominant convention; old code is changed only where it is touched anyway.
The review skill `review-go-style` applies this file.

## Tooling

- Go version: `go.mod` (`go 1.27.1`); the image builds with `golang:1.27`.
- golangci-lint runs in CI with its default linter set and
  `only-new-issues: true` (`.github/workflows/reviewdog.yml`). There is no
  `.golangci.yml`.
- Formatting in CI: `gofmt -s`, and `goimports-reviser` with
  `-imports-order std,general,company,project -company-prefixes github.com/percona`.
  That is four import groups: standard library, third party,
  `github.com/percona/*`, this module. There is no make target for them;
  run both before pushing.
- `make generate` also runs `go fix` and `deadcode -test ./...`
  (`check-deadcode`), which prints functions unreachable from `main` and
  from tests. Remove the ones your change caused.
- CI does not run `go vet`, `go mod tidy` or `-race`. Run `go mod tidy`
  after changing dependencies.
- Other CI jobs on Go changes: `make test`, the manifests drift check
  (`make generate manifests VERSION=main && git diff --exit-code`), misspell
  with US spelling.

## Errors

- Use `github.com/pkg/errors`: `errors.Wrap`, `errors.Wrapf`, `errors.New`,
  `errors.Errorf`, `errors.Is`, `errors.As`. Alias the standard library as
  `stderrors` when you need `errors.Join`.
- Do not use `fmt.Errorf` with `%w`; wrap with `errors.Wrap` so the cause
  and the stack are kept.
- Wrap messages are lowercase verb phrases naming the operation, with no
  trailing punctuation: `"get server version"`, `"create statefulset"`,
  `"reconcile users"`. The older `"failed to ..."` form exists; do not add
  more of it.
- Sentinel errors are `var ErrX = errors.New("...")`, exported when another
  package checks them, and compared with `errors.Is`. There are no custom
  error types; add one only when a caller needs structured data.
- Kubernetes API errors: alias the package `k8serrors` and check with
  `k8serrors.IsNotFound(err)` and friends.

## Logging

- `log := logf.FromContext(ctx)` with
  `logf "sigs.k8s.io/controller-runtime/pkg/log"`. The variable is `log`.
- `log.Info` for actions the operator takes and state changes; `log.V(1).Info`
  for detail. `V(1)` is the only verbosity level in use.
- `log.Error(err, "...")` only where the error is handled in place. A
  returned error is logged once, by the code that handles it.
- Key/value pairs, never formatted messages:
  `log.Info("deleting outdated backup job", "job", item.Name)`. Keys in use:
  `pod`, `replset`, `name`, `user`, `pvc`, `error`.
- Messages are short, without a trailing period. Capitalization is mixed in
  the code base; keep the style of the file you edit.
- No `fmt.Print*` and no standard library `log` in operator code.

## Context

- Every function that talks to the API server, MongoDB, PBM or a pod takes
  `ctx context.Context` as its first parameter and passes it on. Only
  constructors are exempt.
- `context.TODO()` and `context.Background()` do not appear in operator
  code. Tests use `t.Context()`.

## Naming

- `cr` is the CR a function reconciles (`*api.PerconaServerMongoDB`). Backup
  and restore CRs passed into cluster code are `bcp` and `restore`.
- Import aliases: `api` for `pkg/apis/psmdb/v1`, `corev1`, `metav1`,
  `k8serrors`, `logf`. The controller-runtime `client` package keeps its
  name; a client variable is `cl`.
- Receivers: `r` for reconcilers. On API types, use the receiver name the
  type already uses.
- Packages are lowercase single words. File names may use underscores
  (`psmdb_controller.go`, `custom_users.go`); one topic per file.
- Helpers stay unexported unless another package needs them. Constants go
  into grouped `const (...)` blocks.
- Label, annotation and finalizer strings live in `pkg/naming`.

## Tests

- Standard `testing` with testify (`assert`, `require`), table-driven with
  `t.Run`, and `t.Parallel()` when the cases share no state.
- Ginkgo, Gomega and envtest are used only by the two controller suites
  (`perconaservermongodb/suite_test.go`,
  `perconaservermongodbrestore/suite_test.go`). Reconcile behaviour goes
  there; everything else is a plain test.
- Fake clients come from `fake.NewClientBuilder().WithScheme(...)
  .WithObjects(...).WithStatusSubresource(...)`. Packages have a helper for
  it (`buildFakeClient`, `fakeReconciler`, `newFakeClient`); reuse the one in
  the package you are in.
- Load the example CR with `readDefaultCR`, which reads `deploy/cr.yaml`, so
  tests run on the documented defaults.
- PBM mocks (`mock_pbm.go`) come from `go generate` through mockgen; do not
  edit them by hand.
- Expected objects live in `testdata/` next to the test
  (`reconcile-statefulset/`, `rs-svc/`). They are static YAML with no update
  flag; change them only when the rendering changes on purpose.
- `make test` runs `go test ./... -shuffle=on` with envtest assets for
  Kubernetes 1.31. Tests must not depend on order.

## Comments and markers

- Exported functions get a doc comment that starts with the name. Lint does
  not enforce it; reviewers do.
- `//nolint:<linter>` names the linter and carries a reason. Bare `// nolint`
  exists only on legacy fake-client imports.
- `TODO` comments say what and why, with a ticket when there is one.
- CR fields use the kubebuilder markers already in use (`default`,
  `validation:Enum`, `validation:Minimum`, `validation:Maximum`,
  `validation:XValidation`, `printcolumn`) and `json:"...,omitempty"` tags.
