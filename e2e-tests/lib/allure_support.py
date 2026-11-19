"""Allure reporting: steps for shared helpers, failure attachments, labels, environment.

Everything here is a no-op unless pytest runs with --alluredir.
"""

import functools
import inspect
import os
import re
from collections.abc import Callable
from pathlib import Path

import allure
import pytest

# user:password@host in connection strings, and pwd:'...' in createUser calls.
# Image digests (tag@sha256:...) look the same and are left alone.
_PASSWORD = re.compile(r":[^:@/\s]+@(?!sha256:)")
# The pwd value runs to the next , or } so that shell-escaped quotes in xtrace
# output ('\''secret'\'') are masked too.
_PWD_FIELD = re.compile(r"""(\bpwd['"]?[ \t]*:[ \t]*)[^,}\n]+""")
# Names of keys that hold a secret. Bounded runs keep matching linear on long
# dotted or hyphenated words.
_KEY = (
    r"[\w.-]{0,64}?(?:password|passwd|(?<![a-z])pass(?![a-z0-9_])|secret|token|access[_-]?key|account[_-]?key"
    r"|api[_-]?key|private[_-]?key|unseal[_-]?key|\.key|-key|credentials(?![a-z]))[\w.-]{0,64}"
)
# A quoted value may hold spaces; a bare value runs to the next space or delimiter.
_VALUE = r"""(?:'[^'\n]*'|"[^"\n]*"|[^\s'",;{}\[\]]+)"""
# KEY=value anywhere: env and xtrace lines, --from-literal=token=..., password=...
_SECRET_ASSIGN = re.compile(rf"""(?i)(\b{_KEY}['"]?[ \t]*=(?!=)[ \t]*)({_VALUE})""")
# key: value only where a key starts (line start, list item, xtrace "+ ", after { or ,),
# so error text like "get users secret: <cause>" keeps its cause. Never crosses lines.
_SECRET_FIELD = re.compile(
    rf"""(?im)((?:^[ \t]*(?:[-+][ \t]+)?|[{{,][ \t]*)['"]?{_KEY}['"]?[ \t]*:[ \t]*)({_VALUE})"""
)
# Keys that name a secret instead of holding one, for example secretName: my-secret,
# the CR field credentialsSecret: my-s3-secret (camelCase ending in Secret), or values
# that are not secrets (token_status_code=401, TOKEN_TTL: 24h).
_SECRET_REFERENCE = re.compile(
    r"(?i:name|ref|path|file|type|version|status|code|ttl|\.py)$|[a-z]Secrets?$|^(?i:secrets?)$"
)
# --password value, mongosh -p value, Authorization: Bearer value, vault operator unseal value.
_SECRET_ARG = re.compile(
    r"(?i)(--[\w-]{0,40}password[ \t]+|\bmongo(?:sh)?\b[^\n]{0,200}?[ \t]-p[ \t]+|\bbearer[ \t]+"
    rf"|\bunseal[ \t]+)({_VALUE})"
)
# Tokens with a known shape: AWS access key IDs, Vault tokens, Grafana service account tokens.
_KNOWN_TOKEN = re.compile(
    r"\b(?:(?:AKIA|ASIA)[0-9A-Z]{16}|hvs\.[A-Za-z0-9_-]{20,}|glsa_[A-Za-z0-9_]{20,})\b"
)
_PRIVATE_KEY = re.compile(
    r"-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|\Z)", re.DOTALL
)
# File paths under well-known roots are not secrets (--tls-key=/etc/tls/tls.key).
# Other values that start with / stay masked: base64 keys can start with /.
_FILE_PATH = re.compile(r"/(?:dev|etc|tmp|var|opt|home|data|usr|run|proc|mnt|srv|root|certs?)/")
_MAX_VALUE_LEN = 80

# Failure diagnostics from K8sCollector.capture_summary(), in report order.
_SUMMARY_TITLES = {
    "logs": "Operator pod logs",
    "resources": "Kubernetes resources",
    "events": "Kubernetes events",
    "pbm": "PBM status and logs",
    "mongod": "Mongod logs",
}


def _mask_secret_field(match: re.Match[str]) -> str:
    prefix, value = match.group(1), match.group(2)
    key = prefix.strip().lstrip("-+{,").strip().strip("'\"").rstrip("=:").strip().strip("'\"")
    # Also skip pytest log prefixes such as lib.secrets:secrets.py:96.
    if (
        _SECRET_REFERENCE.search(key)
        or value.startswith(("***", "<", "$"))
        or _FILE_PATH.match(value.strip("'\""))
        or ".py:" in value
    ):
        return match.group(0)
    quote = value[0] if value[0] in "'\"" and len(value) > 1 and value[-1] == value[0] else ""
    return f"{prefix}{quote}***{quote}"


def mask(text: str) -> str:
    """Hide passwords, keys and tokens in text that goes to the report."""
    text = _PRIVATE_KEY.sub("***", text)
    text = _KNOWN_TOKEN.sub("***", text)
    text = _SECRET_ARG.sub(_mask_secret_field, text)
    text = _SECRET_ASSIGN.sub(_mask_secret_field, text)
    text = _SECRET_FIELD.sub(_mask_secret_field, text)
    return _PWD_FIELD.sub(r"\1***", _PASSWORD.sub(":***@", text))


def _title_value(value: object) -> str:
    text = " ".join(mask(str(value)).split())
    if text.startswith("/"):
        text = Path(text).name  # file arguments: the name is enough
    return text if len(text) <= _MAX_VALUE_LEN else text[: _MAX_VALUE_LEN - 3] + "..."


def step[**P, R](title: str) -> Callable[[Callable[P, R]], Callable[P, R]]:
    """Report each call of the function as an Allure step.

    `title` is formatted with the call arguments, for example
    "Wait for pod {pod_name}". Arguments are not recorded as step parameters,
    and passwords in the title are masked, so connection strings stay out of
    the report.
    """

    def decorator(func: Callable[P, R]) -> Callable[P, R]:
        signature = inspect.signature(func)

        @functools.wraps(func)
        def wrapper(*args: P.args, **kwargs: P.kwargs) -> R:
            bound = signature.bind(*args, **kwargs)
            bound.apply_defaults()
            values = {name: _title_value(value) for name, value in bound.arguments.items()}
            with allure.step(title.format(**values)):
                return func(*args, **kwargs)

        return wrapper

    return decorator


def issue[F: Callable[..., object]](key: str) -> Callable[[F], F]:
    """Link a test to its Jira ticket, for example @issue("K8SPSMDB-1753")."""
    decorator: Callable[[F], F] = allure.issue(key, name=key)  # type: ignore[no-untyped-call]
    return decorator


def attach_summary(summary: dict[str, str]) -> None:
    """Attach the failure diagnostics to the current test."""
    for key, title in _SUMMARY_TITLES.items():
        content = summary.get(key)
        if content:
            attach_text(content, title)


def attach_text(content: str, name: str) -> None:
    """Attach text to the current test, with passwords masked."""
    allure.attach(mask(content), name=name, attachment_type=allure.attachment_type.TEXT)


def name_bash_test(test_name: str) -> None:
    """Give a bash test run by the wrapper its own name and identity in the report.

    Every bash test runs as the same pytest test (test_bash_wrapper), so Allure
    would treat them as one test and show all but the last as its retries. A
    hidden parameter with the test name makes the history id unique per test.
    """
    allure.dynamic.title(test_name)  # type: ignore[no-untyped-call]
    allure.dynamic.parameter("test", test_name, mode=allure.parameter_mode.HIDDEN)


def _results_dir(config: pytest.Config) -> str | None:
    results_dir: str | None = config.getoption("allure_report_dir", None)
    return results_dir


def add_labels(config: pytest.Config, items: list[pytest.Item]) -> None:
    """Group tests by e2e test name: the test directory, or --test-name for bash tests."""
    if not _results_dir(config):
        return

    bash_test = config.getoption("--test-name")
    for item in items:
        is_bash = item.name == "test_bash_wrapper" and bash_test
        name = bash_test if is_bash else item.path.parent.name
        item.add_marker(pytest.mark.allure_label(name, label_type="feature"))
        item.add_marker(
            pytest.mark.allure_label("bash" if is_bash else "pytest", label_type="tag")
        )
        if is_bash:
            item.add_marker(pytest.mark.allure_label(name, label_type="suite"))


def write_environment(config: pytest.Config) -> None:
    """Write the run environment for the report's Environment widget."""
    results_dir = _results_dir(config)
    if not results_dir:
        return

    from lib.report_generator import ENV_VARS_TO_REPORT

    lines = [f"{key}={os.environ[key]}" for key in ENV_VARS_TO_REPORT if os.environ.get(key)]
    path = Path(results_dir)
    path.mkdir(parents=True, exist_ok=True)
    (path / "environment.properties").write_text("\n".join(lines) + "\n")
