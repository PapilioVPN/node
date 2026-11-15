.PHONY: check build generate package image smoke integration

VULN_DB ?= https://vuln.go.dev
VERSION ?= $(shell git describe --always --dirty)
IMAGE ?= papilio-node:$(VERSION)

check:
	./scripts/ci/check.sh "$(VULN_DB)"

integration:
	./scripts/ci/integration.sh

build:
	go build ./...
	./scripts/ci/package.sh "$(VERSION)"

generate:
	./scripts/ci/generate.sh

package: build

image: package
	docker build --platform linux/amd64 --tag "$(IMAGE)" --file deploy/Dockerfile .local/artifacts/context

smoke:
	go test -race -tags=container ./internal/bootstrap -run '^TestContainer$$' -args -image "$(IMAGE)"
