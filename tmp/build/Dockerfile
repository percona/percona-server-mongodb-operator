FROM alpine:3.6

RUN adduser -D percona-server-mongodb-operator
USER percona-server-mongodb-operator

ADD tmp/_output/bin/percona-server-mongodb-operator /usr/local/bin/percona-server-mongodb-operator
