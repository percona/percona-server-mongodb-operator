FROM oraclelinux:9 AS base-build
WORKDIR /build
RUN dnf update && dnf install make gcc krb5-devel

RUN arch=$(arch | sed s/aarch64/arm64/ | sed s/x86_64/amd64/) && \
curl -sL -o /tmp/golang.tar.gz https://go.dev/dl/go1.25.1.linux-${arch}.tar.gz && \
rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/golang.tar.gz && rm /tmp/golang.tar.gz
ENV PATH=$PATH:/usr/local/go/bin

ARG TESTS_BCP_TYPE
ENV TESTS_BCP_TYPE=${TESTS_BCP_TYPE}

COPY . .
RUN go build -o /bin/pbm-test ./e2e-tests/cmd/pbm-test

CMD ["pbm-test"]
