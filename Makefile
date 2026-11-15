.PHONY: check build generate

VULN_DB ?= https://vuln.go.dev

check:
	./scripts/ci/check.sh "$(VULN_DB)"

build:
	go build ./...

generate:
	./scripts/ci/generate.sh
