"""Unit tests of the credential scan; run with `make py-unit` (no cluster, no conftest)."""

import base64

from lib.secret_scan import (
    ALLOWED_VALUES,
    MIN_VALUE_LENGTH,
    Finding,
    data_values,
    mask,
    scan_text,
)

VALUES = {"s3-secret/AWS_SECRET_ACCESS_KEY": "some-secret-key"}


def test_exact_value_is_reported_masked() -> None:
    findings = scan_text("pod/op/manager", "line one\nkey=some-secret-key here\n", VALUES)
    assert findings == [
        Finding("pod/op/manager", 2, "secret value s3-secret/AWS_SECRET_ACCESS_KEY", "some***")
    ]
    assert "some-secret-key" not in str(findings[0])


def test_base64_value_is_reported() -> None:
    encoded = base64.b64encode(b"some-secret-key").decode()
    values = {"s3-secret/AWS_SECRET_ACCESS_KEY (base64)": encoded}
    findings = scan_text("secret-dump", f"  secret-key: {encoded}", values)
    assert [f.rule for f in findings] == ["secret value s3-secret/AWS_SECRET_ACCESS_KEY (base64)"]


def test_clean_text_has_no_findings() -> None:
    text = "mongodb://user:***@host/admin\nmongodb+srv://user:<password>@host\nbucket=operator-testing"
    assert scan_text("pod/db/mongod", text, VALUES) == []


def test_patterns() -> None:
    text = (
        "AKIAABCDEFGHIJKLMNOP\n"
        "aws_secret_access_key = x\n"
        "DefaultEndpointsProtocol=https;AccountKey=abc\n"
        '{"private_key": "-----BEGIN PRIVATE KEY-----"}\n'
        "uri mongodb://admin:hunter22@db-rs0-0:27017/admin"
    )
    rules = [f.rule for f in scan_text("src", text, {})]
    assert rules == [
        "aws-access-key-id",
        "aws-secret-access-key",
        "azure-account-key",
        "gcp-private-key",
        "private-key-block",
        "mongodb-uri-password",
    ]
    uri = next(f for f in scan_text("src", text, {}) if f.rule == "mongodb-uri-password")
    assert uri.masked == "mong***"
    assert "hunter22" not in uri.masked


def test_mask_keeps_four_characters() -> None:
    assert mask("abcdefgh") == "abcd***"
    assert mask("ab") == "ab***"


def test_limits() -> None:
    assert MIN_VALUE_LENGTH == 8
    assert "S3-BACKUP-BUCKET-NAME-HERE" in ALLOWED_VALUES


def test_every_line_of_a_multiline_value_is_searched() -> None:
    key = '{\n  "client_email": "e2e@project.iam",\n  "private_key_id": "0123456789abcdef"\n}'
    values = data_values("gcp-cs-sa-key-secret/key.json", _b64(key))
    text = 'email "client_email": "e2e@project.iam",\nid "private_key_id": "0123456789abcdef"'
    rules = [f.rule for f in scan_text("pod/op/manager", text, values)]
    assert rules == [
        "secret value gcp-cs-sa-key-secret/key.json#2",
        "secret value gcp-cs-sa-key-secret/key.json#3",
    ]


def test_original_base64_of_a_value_with_a_trailing_newline_is_searched() -> None:
    encoded = _b64("some-secret-key\n")
    values = data_values("s3-secret/AWS_SECRET_ACCESS_KEY", encoded)
    assert values["s3-secret/AWS_SECRET_ACCESS_KEY (base64)"] == encoded
    findings = scan_text("events", f"  secret-key: {encoded}", values)
    assert [f.rule for f in findings] == ["secret value s3-secret/AWS_SECRET_ACCESS_KEY (base64)"]


def test_short_and_allowlisted_values_are_skipped() -> None:
    assert data_values("s3-secret/region", _b64("us-east-1\nshort\n")) == {
        "s3-secret/region (base64)": _b64("us-east-1\nshort\n"),
        "s3-secret/region#1": "us-east-1",
    }
    assert data_values("s3-secret/bucket", _b64("S3-BACKUP-BUCKET-NAME-HERE")) == {}


def _b64(value: str) -> str:
    return base64.b64encode(value.encode()).decode()
