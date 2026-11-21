#!/usr/bin/env python3

import logging
from collections.abc import Callable
from dataclasses import dataclass

import pytest
from lib.kubectl import kubectl_bin
from lib.ldap import check_replset_ldap, check_sharded_ldap, deploy_openldap
from lib.mongo import MongoManager
from lib.utils import Paths

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class LdapConfig:
    namespace: str
    ldap_server: str


class TestLdap:
    """LDAP authentication over plain LDAP (port 389) for a replset and a sharded cluster."""

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def config(cls, create_infra: Callable[[str], str]) -> LdapConfig:
        namespace = create_infra("ldap")
        return LdapConfig(
            namespace=namespace, ldap_server=f"openldap.{namespace}.svc.cluster.local:389"
        )

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def setup_tests(cls, config: LdapConfig, test_paths: Paths) -> None:
        deploy_openldap(test_paths["test_dir"], config.namespace, tls=False, timeout=220)
        kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")

    def test_replset_ldap_auth(
        self, config: LdapConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        check_replset_ldap(
            psmdb_client, test_paths["test_dir"], config.namespace, config.ldap_server
        )

    def test_sharded_ldap_auth(
        self, config: LdapConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        check_sharded_ldap(
            psmdb_client, test_paths["test_dir"], config.namespace, config.ldap_server
        )
