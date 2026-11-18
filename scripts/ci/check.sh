#!/bin/sh
set -eu

sh scripts/proto/generate.sh --check
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
go test -run="^$" -fuzz="^FuzzSwapCounters$" -fuzztime=10s -parallel=2 ./internal/apps/system/host/memory/infra/sys
go test -run="^$" -fuzz="^FuzzCPUInfo$" -fuzztime=10s -parallel=2 ./internal/apps/system/host/cpu/infra/sys
go test -run="^$" -fuzz="^FuzzActivity$" -fuzztime=10s -parallel=2 ./internal/apps/system/host/cpu/infra/sys
govulncheck ./...
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/node ./cmd/node
dist/node -help
revision=$(git rev-parse HEAD)
sh scripts/ci/package.sh dist/node "$revision" dist
go test -race -tags=integration,distribution ./internal/bootstrap/distribution \
    -run="^TestDistribution" -count=1 -args -bundle="$PWD/dist/node-linux-amd64-$revision.tar.gz"
go test -race -tags=integration,distribution ./internal/apps/system/host/identity/integration \
    -run="^TestPackagedHostIdentity$" -count=1 -args -image="papilio-node-build:$revision"
go test -race -tags=integration,distribution ./internal/apps/system/host/uptime/integration \
    -run="^TestPackagedHostUptime$" -count=1 -args -image="papilio-node-build:$revision"

go test -race -tags=integration,distribution ./internal/apps/system/host/cpu/integration \
    -run="^TestPackagedHostCPU$" -count=1 -args -image="papilio-node-build:$revision"

go test -race -tags=integration,distribution ./internal/apps/system/host/memory/integration \
    -run="^TestPackagedHostMemory$" -count=1 -args -image="papilio-node-build:$revision"
