FROM golang:1.25.5 AS builder
ARG OS=linux
ARG ARCH=amd64
WORKDIR /go/src/open-cluster-management.io/addon-contrib/karpenter-provider-aws-addon
COPY . .
RUN GOOS=${OS} GOARCH=${ARCH} make build

FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
ENV USER_UID=10001
COPY --from=builder /go/src/open-cluster-management.io/addon-contrib/karpenter-provider-aws-addon/karpenter-provider-aws-addon-controller /
USER ${USER_UID}
ENTRYPOINT ["/karpenter-provider-aws-addon-controller"]
