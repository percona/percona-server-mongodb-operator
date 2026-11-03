"""Generate the review set: one git diff per case from exact edits to tracked files.

Usage: python3 -I .agents/evals/review-set/make_cases.py <repo> <out_dir> [case ...]

Each case applies `replace` or `append` edits to tracked files, captures `git diff`,
writes it to <out_dir>/<case>/diff.patch and restores the files. The script refuses to
run when a file it edits has local changes, and it stops when a `replace` anchor is not
found exactly once, so a stale case fails instead of producing a different diff.
"""

import pathlib
import subprocess
import sys

ARBITER = "e2e-tests/arbiter/test_arbiter.py"
TYPES = "pkg/apis/psmdb/v1/psmdb_types.go"
CONTAINER = "pkg/psmdb/container.go"
BACKUP = "pkg/psmdb/backup/backup.go"
TLS = "pkg/psmdb/tls/tls.go"
NAMING_TEST = "pkg/naming/naming_test.go"
TESTS_YAML = "e2e-tests/tests.yaml"
TLS_TEST = "e2e-tests/tls-issue-cert-manager/test_tls_issue_cert_manager.py"

CRED = 'f"clusterAdmin:clusterAdmin123456@{config.cluster}.{config.namespace}"'

Edit = tuple[str, ...]

CASES: dict[str, list[Edit]] = {
    # Defects. expected.md names the planted defect of each case.
    "D1-unbounded-wait": [
        (
            "append",
            ARBITER,
            '''
    @pytest.mark.dependency(depends=["TestArbiter::test_primary_failover"])
    def test_cluster_back_to_ready(self, config: ArbiterConfig) -> None:
        """Wait until the operator reports the cluster ready again after the failover"""
        while True:
            state = kubectl_bin(
                "get", "psmdb", "arbiter", "-o", "jsonpath={.status.state}", check=False
            ).strip()
            if state == "ready":
                break
            logger.info(f"cluster state is {state!r}, waiting")
            time.sleep(5)
        logger.info("cluster is ready again")
''',
        ),
    ],
    "D2-readiness-sleep": [
        (
            "append",
            ARBITER,
            f'''
    @pytest.mark.dependency(depends=["TestArbiter::test_primary_failover"])
    def test_scale_up_data_nodes(self, config: ArbiterConfig, psmdb_client: MongoManager) -> None:
        """Scale rs0 to six data nodes and check the replset sees seven members"""
        kubectl_bin(
            "patch",
            "psmdb",
            "arbiter",
            "--type=json",
            "-p",
            '[{{"op":"replace","path":"/spec/replsets/0/size","value":6}}]',
        )
        time.sleep(300)  # give the operator time to add the new pods

        members = psmdb_client.run_mongosh(
            "rs.status().members.length",
            {CRED},
        ).strip()
        assert members == "7", f"expected 7 replset members (6 data + arbiter), got {{members}}"
''',
        ),
    ],
    "D3-assertion-cannot-fail": [
        (
            "append",
            ARBITER,
            '''
    @pytest.mark.dependency(depends=["TestArbiter::test_create_cluster"])
    def test_arbiter_has_no_data_volume(self, config: ArbiterConfig) -> None:
        """The arbiter pod must not get a data PVC"""
        pvcs = kubectl_bin(
            "get", "pvc", "-l", "app.kubernetes.io/component=arbiter", "-o", "name", check=False
        )
        logger.info(f"arbiter PVCs: {pvcs.strip()!r}")
''',
        ),
    ],
    "D4-no-crversion-gate": [
        (
            "replace",
            TYPES,
            "\tEnvFrom".ljust(26)
            + "[]corev1.EnvFromSource".ljust(29)
            + '`json:"envFrom,omitempty"`\n',
            "\tEnvFrom".ljust(26)
            + "[]corev1.EnvFromSource".ljust(29)
            + '`json:"envFrom,omitempty"`\n'
            + "\tTimezone".ljust(26)
            + "string".ljust(29)
            + '`json:"timezone,omitempty"`\n',
        ),
        (
            "replace",
            CONTAINER,
            "\trsName := replset.Name\n",
            '\ttz := replset.Timezone\n\tif tz == "" {\n\t\ttz = "UTC"\n\t}\n\n\trsName := replset.Name\n',
        ),
        (
            "replace",
            CONTAINER,
            "\t\t\t\tValue: cr.Namespace,\n\t\t\t},\n",
            '\t\t\t\tValue: cr.Namespace,\n\t\t\t},\n\t\t\t{\n\t\t\t\tName:  "TZ",\n\t\t\t\tValue: tz,\n\t\t\t},\n',
        ),
    ],
    "D5-leaked-resource": [
        (
            "append",
            ARBITER,
            '''
    @pytest.mark.dependency(depends=["TestArbiter::test_create_cluster"])
    def test_pods_visible_to_view_role(self, config: ArbiterConfig) -> None:
        """A service account with the built-in view role can list the cluster pods"""
        kubectl_bin(
            "create",
            "clusterrolebinding",
            f"{config.namespace}-view",
            "--clusterrole=view",
            f"--serviceaccount={config.namespace}:default",
        )
        pods = kubectl_bin(
            "get",
            "pods",
            "-n",
            config.namespace,
            "-o",
            "name",
            f"--as=system:serviceaccount:{config.namespace}:default",
        )
        assert f"pod/{config.cluster}-0" in pods, (
            f"cluster pods not visible to the view role: {pods}"
        )
''',
        ),
    ],
    "D6-go-style": [
        ("replace", BACKUP, 'import (\n\t"context"\n', 'import (\n\t"context"\n\t"fmt"\n'),
        (
            "replace",
            BACKUP,
            "\terr := cl.List(ctx,\n\t\trstrs,",
            "\terr := cl.List(context.Background(),\n\t\trstrs,",
        ),
        (
            "replace",
            BACKUP,
            '\t\treturn false, errors.Wrap(err, "get restore list")\n',
            '\t\tl.Error(err, "list restores")\n\t\treturn false, fmt.Errorf("get restore list: %w", err)\n',
        ),
    ],
    # Clean diffs. Nothing above Minor is expected.
    "C1-behaviour-test": [
        (
            "append",
            ARBITER,
            f'''
    @pytest.mark.dependency(depends=["TestArbiter::test_primary_failover"])
    def test_arbiter_pod_restart(
        self, config: ArbiterConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        """Delete the arbiter pod and check it rejoins the replset without data loss"""
        arbiter_pod = f"{{config.cluster}}-arbiter-0"
        kubectl_bin(
            "delete", "pod", arbiter_pod, "--grace-period=0", "--force", "-n", config.namespace
        )
        wait_pod(arbiter_pod)
        wait_for_running(config.cluster, 2, False)

        deadline = time.time() + 120
        states = ""
        while time.time() < deadline:
            states = psmdb_client.run_mongosh(
                "rs.status().members.map(m => m.name + ' ' + m.stateStr).join('\\\\n')",
                {CRED},
            )
            arbiter_lines = [s for s in states.splitlines() if s.startswith(arbiter_pod)]
            if arbiter_lines and arbiter_lines[0].endswith("ARBITER"):
                break
            time.sleep(5)
        else:
            pytest.fail(f"arbiter did not rejoin as ARBITER within 120s, last seen: {{states}}")

        for i in range(2):
            psmdb_client.compare_mongo_cmd(
                "find({{}}, {{ _id: 0 }}).toArray()",
                f"myApp:myPass@{{config.cluster}}-{{i}}.{{config.cluster}}.{{config.namespace}}",
                test_file=f"{{test_paths['test_dir']}}/compare/find-1.json",
            )
''',
        ),
    ],
    "C2-error-wrapping": [
        (
            "replace",
            TLS,
            'fmt.Errorf("generate CA key: %v", err)',
            'errors.Wrap(err, "generate CA key")',
        ),
        (
            "replace",
            TLS,
            'fmt.Errorf("generate CA certificate: %v", err)',
            'errors.Wrap(err, "generate CA certificate")',
        ),
        (
            "replace",
            TLS,
            'fmt.Errorf("encode CA certificate: %v", err)',
            'errors.Wrap(err, "encode CA certificate")',
        ),
        (
            "replace",
            TLS,
            'fmt.Errorf("encode CA private key: %v", err)',
            'errors.Wrap(err, "encode CA private key")',
        ),
        (
            "replace",
            TLS,
            'fmt.Errorf("encode TLS certificate: %v", err)',
            'errors.Wrap(err, "encode TLS certificate")',
        ),
        (
            "replace",
            TLS,
            'fmt.Errorf("encode TLS private key: %v", err)',
            'errors.Wrap(err, "encode TLS private key")',
        ),
    ],
    "C3-unit-test": [
        (
            "replace",
            NAMING_TEST,
            'import "testing"\n',
            (
                "import (\n"
                '\t"testing"\n'
                "\n"
                '\tpsmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"\n'
                ")\n"
            ),
        ),
        (
            "append",
            NAMING_TEST,
            """
func TestMongodStatefulSetName(t *testing.T) {
	t.Parallel()

	cr := &psmdbv1.PerconaServerMongoDB{}
	cr.Name = "my-cluster"
	rs := &psmdbv1.ReplsetSpec{Name: "rs0"}

	tests := map[string]struct {
		fn       func(*psmdbv1.PerconaServerMongoDB, *psmdbv1.ReplsetSpec) string
		expected string
	}{
		"statefulset":   {fn: MongodStatefulSetName, expected: "my-cluster-rs0"},
		"custom config": {fn: MongodCustomConfigName, expected: "my-cluster-rs0-mongod"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tt.fn(cr, rs); got != tt.expected {
				t.Errorf("%s = %q, want %q", name, got, tt.expected)
			}
		})
	}
}
""",
        ),
    ],
    "C4-tests-yaml-skip": [
        (
            "replace",
            TESTS_YAML,
            "  gke-arm64:\n    - name: monitoring-pmm3\n",
            (
                "  gke-arm64:\n"
                "    - name: demand-backup-eks-credentials\n"
                "      reason: requires EKS credentials integration\n"
                "    - name: demand-backup-eks-credentials-irsa\n"
                "      reason: requires EKS IRSA\n"
                "    - name: monitoring-pmm3\n"
            ),
        ),
    ],
    "C5-sleep-to-wait": [
        (
            "replace",
            TLS_TEST,
            '        _deploy_cmctl(test_paths["src_dir"], test_paths["conf_dir"])\n        time.sleep(60)\n',
            (
                '        _deploy_cmctl(test_paths["src_dir"], test_paths["conf_dir"])\n'
                "        kubectl_bin(\n"
                '            "wait", "--for=condition=Ready", "certificate/some-name-ca-cert", "--timeout=120s"\n'
                "        )\n"
            ),
        ),
    ],
}


def git(repo: pathlib.Path, *args: str) -> str:
    return subprocess.run(
        ["git", *args], cwd=repo, check=True, capture_output=True, text=True
    ).stdout


def apply_edit(repo: pathlib.Path, case_id: str, edit: Edit) -> str:
    kind, rel, *rest = edit
    path = repo / rel
    text = path.read_text()
    if kind == "replace":
        old, new = rest
        n = text.count(old)
        if n != 1:
            sys.exit(f"{case_id}: anchor found {n} times in {rel}: {old[:60]!r}")
        text = text.replace(old, new)
    elif kind == "append":
        (addition,) = rest
        if not text.endswith("\n"):
            text += "\n"
        text += addition
    else:
        sys.exit(f"{case_id}: unknown edit kind {kind!r}")
    path.write_text(text)
    return rel


def changed(repo: pathlib.Path, files: list[str]) -> str:
    return git(repo, "status", "--porcelain", "--", *files).strip()


def main(argv: list[str]) -> int:
    if len(argv) < 3:
        sys.exit(__doc__)
    repo = pathlib.Path(argv[1]).resolve()
    out = pathlib.Path(argv[2]).resolve()
    wanted = argv[3:] or list(CASES)
    unknown = sorted(set(wanted) - set(CASES))
    if unknown:
        sys.exit(f"unknown cases: {', '.join(unknown)}; known: {', '.join(CASES)}")
    files = sorted({edit[1] for case_id in wanted for edit in CASES[case_id]})
    if local := changed(repo, files):
        sys.exit(f"local changes in files the set edits, commit or revert them first:\n{local}")

    out.mkdir(parents=True, exist_ok=True)
    for case_id in wanted:
        touched: list[str] = []
        try:
            for edit in CASES[case_id]:
                touched.append(apply_edit(repo, case_id, edit))
            diff = git(repo, "diff", "--", *touched)
        finally:
            if touched:
                git(repo, "checkout", "--", *touched)
        case_dir = out / case_id
        case_dir.mkdir(exist_ok=True)
        (case_dir / "diff.patch").write_text(diff)
        print(f"{case_id}: {len(diff.splitlines())} diff lines, files {sorted(set(touched))}")

    if changed(repo, files):
        sys.exit("files left modified after generation")
    print("done, edited files restored")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
