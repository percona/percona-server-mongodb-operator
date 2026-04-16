ARG MONGODB_VERSION=8.0
ARG MONGODB_IMAGE=perconalab/percona-server-mongodb

FROM ${MONGODB_IMAGE}:${MONGODB_VERSION} AS mongo_image

FROM oraclelinux:9 AS base-build
WORKDIR /build

RUN mkdir -p /data/db

COPY --from=mongo_image /bin/mongod /bin/
RUN dnf install epel-release && dnf update && dnf install make gcc krb5-devel iproute-tc libfaketime bash-completion

RUN arch=$(arch | sed s/aarch64/arm64/ | sed s/x86_64/amd64/) && \
curl -sL -o /tmp/golang.tar.gz https://go.dev/dl/go1.25.1.linux-${arch}.tar.gz && \
rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/golang.tar.gz && rm /tmp/golang.tar.gz
ENV PATH=$PATH:/usr/local/go/bin


FROM base-build
COPY . .

RUN make build-tests && cp /build/bin/* /bin/
RUN pbm completion bash > /etc/bash_completion.d/pbm
RUN useradd -u 1001 user
