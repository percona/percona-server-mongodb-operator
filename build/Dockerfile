FROM alpine:3.8

RUN apk upgrade --update --no-cache

USER nobody

ADD build/_output/bin/percona-server-mongodb-operator /usr/local/bin/percona-server-mongodb-operator
