SHELL := /bin/bash

GO ?= go
GOWORK ?= off
IMAGE_REGISTRY ?= quay.io/open-cluster-management
IMAGE_TAG ?= latest
IMAGE_REPOSITORY ?= $(or $(IMAGE),$(IMAGE_REGISTRY)/karpenter-provider-aws-addon)
IMAGE_NAME ?= $(IMAGE_REPOSITORY):$(IMAGE_TAG)
BINARY ?= karpenter-provider-aws-addon-controller
CHART := charts/karpenter-provider-aws-addon
KARPENTER_VERSION ?= 1.12.1
UPSTREAM_GENERATED_PATHS := \
	pkg/addon/karpenter_generated.go \
	pkg/addon/manifests/templates/crds \
	pkg/addon/manifests/templates/01-hosting-serviceaccount.yaml \
	pkg/addon/manifests/templates/02-hosting-service.yaml \
	pkg/addon/manifests/templates/03-hosting-pdb.yaml \
	pkg/addon/manifests/templates/04-hosting-deployment.yaml

.PHONY: all
all: build

.PHONY: update update-manifests
update: update-manifests
update-manifests:
	GOWORK=$(GOWORK) $(GO) run ./hack/karpenter-sync --version $(KARPENTER_VERSION) --repo-root .

.PHONY: verify-upstream-manifests
verify-upstream-manifests:
	@tmpdir=$$(mktemp -d); \
	trap 'rm -rf "$$tmpdir"' EXIT; \
	GOWORK=$(GOWORK) $(GO) run ./hack/karpenter-sync --version $(KARPENTER_VERSION) --repo-root "$$tmpdir"; \
	status=0; \
	for path in $(UPSTREAM_GENERATED_PATHS); do \
		diff -ru "$$path" "$$tmpdir/$$path" || status=1; \
	done; \
	exit $$status

.PHONY: diff-upstream-manifests
diff-upstream-manifests:
	@tmpdir=$$(mktemp -d); \
	trap 'rm -rf "$$tmpdir"' EXIT; \
	GOWORK=$(GOWORK) $(GO) run ./hack/karpenter-sync --version $(KARPENTER_VERSION) --repo-root "$$tmpdir"; \
	for path in $(UPSTREAM_GENERATED_PATHS); do \
		diff -ru "$$path" "$$tmpdir/$$path" || true; \
	done

.PHONY: verify
verify: vendor verify-fmt verify-upstream-manifests vet test-unit

.PHONY: verify-fmt
verify-fmt:
	@echo "Verifying Go formatting..."
	@test -z "$$(gofmt -l cmd hack pkg)"

.PHONY: vet
vet:
	GOWORK=$(GOWORK) $(GO) vet ./...

.PHONY: vendor
vendor:
	GOWORK=$(GOWORK) $(GO) mod tidy

.PHONY: build
build: vendor
	CGO_ENABLED=0 GOWORK=$(GOWORK) $(GO) build -ldflags="-s -w" -o $(BINARY) ./cmd/karpenter-provider-aws-addon

.PHONY: test-unit
test-unit:
	GOWORK=$(GOWORK) $(GO) test -race -coverprofile=coverage.out ./...

.PHONY: test-integration
test-integration:
	@echo "No integration suite yet; hosted render and Secret sync contracts are covered by unit tests."

.PHONY: test-e2e
test-e2e:
	@echo "No e2e suite yet. Use fake MSA Secret smoke testing before AWS provisioning acceptance."

.PHONY: test-chart
test-chart:
	helm lint $(CHART)
	helm template karpenter-provider-aws-addon $(CHART) \
		--namespace open-cluster-management-addon \
		--set image.repository=$(IMAGE_REPOSITORY) \
		--set image.tag=$(IMAGE_TAG) >/dev/null

.PHONY: image
image:
	docker build -f Dockerfile -t $(IMAGE_NAME) .

.PHONY: image-push
image-push:
	docker push $(IMAGE_NAME)

.PHONY: image-manifest
image-manifest:
	docker manifest create $(IMAGE_NAME) \
		$(IMAGE_REGISTRY)/karpenter-provider-aws-addon:$(IMAGE_TAG)-amd64 \
		$(IMAGE_REGISTRY)/karpenter-provider-aws-addon:$(IMAGE_TAG)-arm64
	docker manifest annotate $(IMAGE_NAME) \
		$(IMAGE_REGISTRY)/karpenter-provider-aws-addon:$(IMAGE_TAG)-amd64 --arch amd64
	docker manifest annotate $(IMAGE_NAME) \
		$(IMAGE_REGISTRY)/karpenter-provider-aws-addon:$(IMAGE_TAG)-arm64 --arch arm64
	docker manifest push $(IMAGE_NAME)
