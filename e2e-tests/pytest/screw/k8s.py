import sys
import base64
import logging
import logging.config
import typing
import yaml
from tempfile import NamedTemporaryFile

import google.auth
from google.cloud import container_v1
from google.auth.transport.requests import Request as GoogleAuthRequest

from kubernetes import client, config
from kubernetes.dynamic import DynamicClient


LOG_CONFIG = {
    "version": 1,
    "formatters": {"default": {"format": "%(asctime)s [%(levelname)s]: %(message)s"}},
    "handlers": {
        "console": {
            "class": "logging.StreamHandler",
            "formatter": "default",
            "level": logging.DEBUG,
        },
        "file": {
            "class": "logging.FileHandler",
            "filename": "test.log",
            "formatter": "default",
            "level": logging.INFO,
        },
    },
    "root": {"handlers": ("console", "file"), "level": "NOTSET"},
}
logging.config.dictConfig(LOG_CONFIG)
logger = logging.getLogger(__name__)

GCP_PROJECT_ID = "cloud-dev-112233"
CLUSTER_ID = "ege-5866"
REGION = "europe-west3"


def init_dynamic_client(
    project_id: str, location: str, cluster_id: str
) -> DynamicClient:
    logger.debug("initializing GKE client")

    gke_cluster = container_v1.ClusterManagerClient().get_cluster(
        request={
            "name": f"projects/{project_id}/locations/{location}/clusters/{cluster_id}"
        }
    )

    # Obtain Google authentication credentials
    creds: google.auth.credentials.Credentials
    creds, _ = google.auth.default()
    auth_req = GoogleAuthRequest()
    # Refresh the token
    creds.refresh(auth_req)

    logger.debug("token refreshed")

    # Initialize the Kubernetes client configuration object
    configuration = client.Configuration()
    # Set the cluster endpoint
    configuration.host = f"https://{gke_cluster.endpoint}"

    logger.debug("connecting to %s", configuration.host)

    # Write the cluster CA certificate to a temporary file
    with NamedTemporaryFile(delete=False) as ca_cert:
        ca_cert.write(base64.b64decode(gke_cluster.master_auth.cluster_ca_certificate))
        configuration.ssl_ca_cert = ca_cert.name

    # Set the authentication token
    configuration.api_key_prefix["authorization"] = "Bearer"
    configuration.api_key["authorization"] = creds.token

    # Create and return the Kubernetes CoreV1 API client
    return DynamicClient(client.ApiClient(configuration))


def create_custom_resource_definitions(cl: DynamicClient):
    crd_resource = cl.resources.get(
        api_version="apiextensions.k8s.io/v1", kind="CustomResourceDefinition"
    )

    with open("../../deploy/crd.yaml") as crd_yaml:
        for crd in yaml.load_all(crd_yaml, yaml.Loader):
            try:
                created_crd = crd_resource.create(body=crd)
                logger.info("created crd/%s", created_crd.metadata["name"])
            except client.exceptions.ApiException as e:
                if e.status == 409:  # HTTP 409 Conflict - Resource already exists
                    logger.info(
                        "crd/%s already exists, skipping",
                        crd.get("metadata", {}).get("name"),
                    )
                else:
                    raise


def create_custom_resource_definition(
    cl: DynamicClient,
    crd: typing.Dict,
) -> client.V1CustomResourceDefinition:
    crd_resource = cl.resources.get(
        api_version="apiextensions.k8s.io/v1", kind="CustomResourceDefinition"
    )
    return crd_resource.create(body=crd)


logger.info("authenticating to cluster")
config.load_kube_config()
dyn_client = init_dynamic_client(GCP_PROJECT_ID, REGION, CLUSTER_ID)

logger.info("creating crds")
create_custom_resource_definitions(dyn_client)
