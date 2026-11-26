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
class LdapTLSConfig:
    namespace: str
    ldap_server: str


class TestLdapTLS:
    """LDAP authentication over LDAPS (port 636) for a replset and a sharded cluster."""

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def config(cls, create_infra: Callable[[str], str]) -> LdapTLSConfig:
        namespace = create_infra("ldap-tls")
        return LdapTLSConfig(
            namespace=namespace, ldap_server=f"openldap.{namespace}.svc.cluster.local:636"
        )

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def setup_tests(
        cls,
        config: LdapTLSConfig,
        test_paths: Paths,
        deploy_cert_manager: Callable[..., None],
    ) -> None:
        deploy_cert_manager()
        deploy_openldap(test_paths["test_dir"], config.namespace, tls=True, timeout=600)
        kubectl_bin("apply", "-f", f"{test_paths['conf_dir']}/secrets.yml")

    @pytest.mark.dependency()
    def test_replset_ldap_auth(
        self, config: LdapTLSConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        check_replset_ldap(
            psmdb_client, test_paths["test_dir"], config.namespace, config.ldap_server
        )

    @pytest.mark.dependency(depends=["TestLdapTLS::test_replset_ldap_auth"])
    def test_sharded_ldap_auth(
        self, config: LdapTLSConfig, test_paths: Paths, psmdb_client: MongoManager
    ) -> None:
        check_sharded_ldap(
            psmdb_client, test_paths["test_dir"], config.namespace, config.ldap_server
        )
