ARG MONGODB_VERSION=8.0
ARG MONGODB_IMAGE=perconalab/percona-server-mongodb
FROM ${MONGODB_IMAGE}:${MONGODB_VERSION}
USER root
COPY e2e-tests/docker/keyFile /opt/keyFile
RUN chown mongodb /opt/keyFile && chmod 400 /opt/keyFile && mkdir -p /home/mongodb/ && chown mongodb /home/mongodb
USER mongodb
