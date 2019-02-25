FROM busybox:latest

COPY bin/dcos-mongodb-controller /usr/local/bin/
COPY bin/dcos-mongodb-watchdog /usr/local/bin/

RUN adduser -D mongodb-orchestration-tools
USER mongodb-orchestration-tools
