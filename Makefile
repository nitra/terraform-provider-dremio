HOSTNAME=registry.opentofu.org
NAMESPACE=nitra
NAME=dremio
BINARY=terraform-provider-${NAME}
# Placeholder version for local `make install` testing via the filesystem
# plugin cache. Pin required_providers to this version in a throwaway .tf
# file when exercising an unreleased change; real releases are cut by
# goreleaser off a git tag instead.
VERSION=99.0.0
OS_ARCH=$(shell go env GOOS)_$(shell go env GOARCH)

.PHONY: default build install test testacc docs

default: install

build:
	go build -o ${BINARY}

install: build
	mkdir -p ~/.terraform.d/plugins/${HOSTNAME}/${NAMESPACE}/${NAME}/${VERSION}/${OS_ARCH}
	mv ${BINARY} ~/.terraform.d/plugins/${HOSTNAME}/${NAMESPACE}/${NAME}/${VERSION}/${OS_ARCH}

test:
	go test ./... $(TESTARGS) -timeout=30s

testacc:
	TF_ACC=1 go test ./... -v $(TESTARGS) -timeout 120m

docs:
	go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@v0.25.0 generate
