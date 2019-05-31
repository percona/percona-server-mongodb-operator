FROM registry.access.redhat.com/ubi7/ubi-minimal
RUN microdnf update && microdnf clean all

LABEL name="Percona Server for MongoDB Operator" \
      vendor="Percona" \
      summary="Percona Server for MongoDB Operator simplifies the deployment and management of Percona Server for MongoDB in a Kubernetes or OpenShift environment" \
      description="Percona Server for MongoDB is our free and open-source drop-in replacement for MongoDB Community Edition. It offers all the features and benefits of MongoDB Community Edition, plus additional enterprise-grade functionality." \
      maintainer="Percona Development <info@percona.com>"

COPY LICENSE /licenses/
COPY build/_output/bin/percona-server-mongodb-operator /usr/local/bin/percona-server-mongodb-operator

USER nobody
