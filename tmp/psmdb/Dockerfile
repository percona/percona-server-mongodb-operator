ARG PSMDB_VERSION
FROM percona/percona-server-mongodb:$PSMDB_VERSION

USER root
ADD https://github.com/percona/mongodb-orchestration-tools/releases/download/0.4.1/k8s-mongodb-initiator /usr/local/bin
ADD https://github.com/percona/mongodb-orchestration-tools/releases/download/0.4.1/mongodb-healthcheck /usr/local/bin
ADD entrypoint.sh /
RUN chmod +x /usr/local/bin/*-* /entrypoint.sh

USER mongodb
