FROM registry.ci.openshift.org/openshift/release:golang-1.19 AS builder
WORKDIR /go/src/github.com/vr4manta/capi-webhook
COPY . .
RUN NO_DOCKER=1 make build

FROM registry.ci.openshift.org/openshift/origin-v4.0:base
COPY --from=builder /go/src/github.com/vr4manta/capi-webhook/bin/capi-webhook .

LABEL io.openshift.release.operator true
