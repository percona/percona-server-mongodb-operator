FROM scratch AS builder

COPY manifests/ /build/manifests/
COPY metadata/ /build/metadata/

FROM scratch

# ANNOTATIONS is replaced with bundle.annotations.yaml
LABEL \
operators.operatorframework.io.bundle.mediatype.v1="registry+v1"
operators.operatorframework.io.bundle.manifests.v1="manifests/"
operators.operatorframework.io.bundle.metadata.v1="metadata/"
operators.operatorframework.io.bundle.package.v1="percona-server-mongodb-operator"
operators.operatorframework.io.bundle.channels.v1="stable"
operators.operatorframework.io.bundle.channel.default.v1="stable"
com.redhat.openshift.versions="v4.13-v4.17"
org.opencontainers.image.authors="info@percona.com"
org.opencontainers.image.url="https://percona.com"
org.opencontainers.image.vendor="Percona"
com.redhat.delivery.backport=true
com.redhat.delivery.operator.bundle=true

COPY --from=builder /build/ /
