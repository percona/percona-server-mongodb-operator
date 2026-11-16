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
_PWD_FIELD = re.compile(r"""(\bpwd['"]?\s*:\s*)[^,}\n]+""")
_MAX_VALUE_LEN = 80

# Failure diagnostics from K8sCollector.capture_summary(), in report order.
_SUMMARY_TITLES = {
    "logs": "Operator pod logs",
    "resources": "Kubernetes resources",
    "events": "Kubernetes events",
    "pbm": "PBM status and logs",
    "mongod": "Mongod logs",
}


def mask(text: str) -> str:
    """Hide passwords in connection strings and in mongosh createUser calls."""
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


def set_title(title: str) -> None:
    """Set the display name of the current test."""
    allure.dynamic.title(title)  # type: ignore[no-untyped-call]


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
