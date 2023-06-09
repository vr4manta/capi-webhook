#!/usr/bin/env bash

set -eu

REPO=github.com/vr4manta/capi-webhook
WHAT=${1:-capi-webhook}
GLDFLAGS=${GLDFLAGS:-}

eval $(go env | grep -e "GOHOSTOS" -e "GOHOSTARCH")

: "${GOOS:=${GOHOSTOS}}"
: "${GOARCH:=${GOHOSTARCH}}"

# Go to the root of the repo
cd "$(git rev-parse --show-cdup)"

if [ -z ${VERSION_OVERRIDE+a} ]; then
	echo "Using version from git..."
	VERSION_OVERRIDE=$(git describe --abbrev=8 --dirty --always)
fi

GLDFLAGS+="-extldflags '-static' -X ${REPO}/pkg/version.Raw=${VERSION_OVERRIDE}"

eval $(go env)

echo "Building ${REPO}/cmd/${WHAT} (${VERSION_OVERRIDE})"
GO111MODULE=${GO111MODULE} CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${GOARCH} go build ${GOFLAGS} -ldflags "${GLDFLAGS}" -o bin/${WHAT} ${REPO}/cmd/${WHAT}
