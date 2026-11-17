#!/bin/sh
set -eu

unformatted=$(gofmt -l cmd internal)
if [ -n "$unformatted" ]; then
    printf '%s\n' "$unformatted"
    exit 1
fi

go mod tidy
git diff --exit-code -- go.mod go.sum
go mod verify
go vet ./...
go test -race -tags=integration ./...
go test -run="^$" -fuzz="^FuzzLoad$" -fuzztime=10s -parallel=2 ./internal/settings
govulncheck ./...
mkdir -p dist
go build -trimpath -o dist/node ./cmd/node
dist/node -help
