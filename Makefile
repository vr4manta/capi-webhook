DBG         ?= 0
#REGISTRY    ?= quay.io/openshift/
VERSION     ?= $(shell git describe --always --abbrev=7)
MUTABLE_TAG ?= latest
IMAGE        = $(REGISTRY)capi-webhook
BUILD_IMAGE ?= registry.ci.openshift.org/openshift/release:golang-1.19
GOLANGCI_LINT = go run ./vendor/github.com/golangci/golangci-lint/cmd/golangci-lint

# Enable go modules and vendoring
# https://github.com/golang/go/wiki/Modules#how-to-install-and-activate-module-support
# https://github.com/golang/go/wiki/Modules#how-do-i-use-vendoring-with-modules-is-vendoring-going-away
GO111MODULE = on
export GO111MODULE
GOFLAGS ?= -mod=vendor
export GOFLAGS

ifeq ($(DBG),1)
GOGCFLAGS ?= -gcflags=all="-N -l"
endif

.PHONY: all
all: check build test

NO_DOCKER ?= 0

ifeq ($(shell command -v podman > /dev/null 2>&1 ; echo $$? ), 0)
	ENGINE=podman
else ifeq ($(shell command -v docker > /dev/null 2>&1 ; echo $$? ), 0)
	ENGINE=docker
else
	NO_DOCKER=1
endif

USE_DOCKER ?= 0
ifeq ($(USE_DOCKER), 1)
	ENGINE=docker	
endif

ifeq ($(NO_DOCKER), 1)
  DOCKER_CMD =
  IMAGE_BUILD_CMD = imagebuilder
else
  DOCKER_CMD := $(ENGINE) run --env GO111MODULE=$(GO111MODULE) --env GOFLAGS=$(GOFLAGS) --rm -v "$(PWD)":/go/src/github.com/vr4manta/capi-webhook:Z  -w /go/src/github.com/vr4manta/capi-webhook $(BUILD_IMAGE)
  # The command below is for building/testing with the actual image that Openshift uses. Uncomment/comment out to use instead of above command. CI registry pull secret is required to use this image.
  # DOCKER_CMD := $(ENGINE) run --env GO111MODULE=$(GO111MODULE) --env GOFLAGS=$(GOFLAGS) --rm -v "$(PWD)":/go/src/github.com/openshift/machine-api-operator:Z -w /go/src/github.com/openshift/machine-api-operator registry.ci.openshift.org/ocp/builder:rhel-8-golang-1.19-openshift-4.11
  IMAGE_BUILD_CMD = $(ENGINE) build
endif

PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
ENVTEST = go run ${PROJECT_DIR}/vendor/sigs.k8s.io/controller-runtime/tools/setup-envtest
# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.26

.PHONY: vendor
vendor:
	$(DOCKER_CMD) ./hack/go-mod.sh

.PHONY: check
check: verify-crds-sync lint fmt vet test ## Run code validations

.PHONY: build
build: capi-webhook ## Build binaries

.PHONY: capi-webhook
capi-webhook:
	$(DOCKER_CMD) ./hack/go-build.sh capi-webhook

.PHONY: test
test: unit

unit:
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path --bin-dir $(PROJECT_DIR)/bin)" ./hack/ci-test.sh

.PHONY: image
image: ## Build docker image
ifeq ($(NO_DOCKER), 1)
	./hack/imagebuilder.sh
endif
	@echo -e "\033[32mBuilding image $(IMAGE):$(VERSION) and tagging also as $(IMAGE):$(MUTABLE_TAG)...\033[0m"
	$(IMAGE_BUILD_CMD) -t "$(IMAGE):$(VERSION)" -t "$(IMAGE):$(MUTABLE_TAG)" ./

.PHONY: push
push: ## Push image to docker registry
	@echo -e "\033[32mPushing images...\033[0m"
	$(ENGINE) push "$(IMAGE):$(VERSION)"
	$(ENGINE) push "$(IMAGE):$(MUTABLE_TAG)"

.PHONY: lint
lint: ## Run golangci-lint over the codebase.
	 $(call ensure-home, ${GOLANGCI_LINT} run ./pkg/... ./cmd/... --timeout=10m)

.PHONY: fmt
fmt: ## Update and show diff for import lines
	$(DOCKER_CMD) hack/go-fmt.sh .

.PHONY: goimports
goimports: ## Go fmt your code
	$(DOCKER_CMD) hack/goimports.sh .

.PHONY: vet
vet: ## Apply go vet to all go files
	$(DOCKER_CMD) hack/go-vet.sh ./pkg/... ./cmd/...

.PHONY: help
help:
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

define ensure-home
	@ export HOME=$${HOME:=/tmp/kubebuilder-testing}; \
	if [ $${HOME} == "/" ]; then \
	  export HOME=/tmp/kubebuilder-testing; \
	fi; \
	$(1)
endef
