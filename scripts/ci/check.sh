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
XRAY_TEST_BINARY=$(sh scripts/ci/prepare-xray.sh)
export XRAY_TEST_BINARY
go test -race -tags=integration ./...
go test -run="^$" -fuzz="^FuzzLoad$" -fuzztime=10s -parallel=2 ./internal/settings
go test -run="^$" -fuzz="^FuzzSwapCounters$" -fuzztime=10s -parallel=2 ./internal/apps/system/host/memory/infra/sys
go test -run="^$" -fuzz="^FuzzCPUInfo$" -fuzztime=10s -parallel=2 ./internal/apps/system/host/cpu/infra/sys
go test -run="^$" -fuzz="^FuzzActivity$" -fuzztime=10s -parallel=2 ./internal/apps/system/host/cpu/infra/sys
go test -run="^$" -fuzz="^FuzzLoginRecords$" -fuzztime=10s -parallel=2 ./internal/apps/system/host/sessions/infra/utmp
go test -run="^$" -fuzz="^FuzzPowerSupply$" -fuzztime=10s -parallel=2 ./internal/apps/system/host/sensors/infra/sys
go test -run="^$" -fuzz="^FuzzCompilationEnvelope$" -fuzztime=10s -parallel=2 ./internal/apps/xray/runtime/configs/infra/jsonwire
go test -run="^$" -fuzz="^FuzzSelection$" -fuzztime=10s -parallel=2 ./internal/apps/xray/users/accounts/infra/jsonwire
go test -run="^$" -fuzz="^FuzzStateEnvelope$" -fuzztime=10s -parallel=2 ./internal/apps/xray/runtime/configs/infra/jsonwire
go test -run="^$" -fuzz="^FuzzAccountControlEnvelope$" -fuzztime=10s -parallel=2 ./internal/apps/xray/users/accounts/infra/jsonwire
go test -run="^$" -fuzz="^FuzzAccountBatchEnvelope$" -fuzztime=10s -parallel=2 ./internal/apps/xray/users/accounts/infra/jsonwire
go test -run="^$" -fuzz="^FuzzAccountConfigurationEnvelope$" -fuzztime=10s -parallel=2 ./internal/apps/xray/users/accounts/infra/jsonwire
go test -run="^$" -fuzz="^FuzzCompiledAccountSelection$" -fuzztime=10s -parallel=2 ./internal/apps/xray/users/accounts/infra/configuration
go test -run="^$" -fuzz="^FuzzCompiledMembershipReplacement$" -fuzztime=10s -parallel=2 ./internal/apps/xray/users/accounts/infra/configuration
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

go test -race -tags=integration,distribution ./internal/apps/system/network/interfaces/integration \
    -run="^TestPackagedNetworkInterfaces$" -count=1 -args -image="papilio-node-build:$revision"

go test -race -tags=integration,distribution ./internal/apps/system/host/processes/integration \
    -run="^TestPackagedHostProcesses$" -count=1 -args -image="papilio-node-build:$revision"

go test -race -tags=integration,distribution ./internal/apps/system/storage/filesystems/integration \
    -run="^TestPackagedFilesystems$" -count=1 -args -image="papilio-node-build:$revision"

go test -race -tags=integration,distribution ./internal/apps/system/storage/devices/integration \
    -run="^TestPackagedDeviceIO$" -count=1 -args -image="papilio-node-build:$revision"

go test -race -tags=integration,distribution ./internal/apps/system/host/sessions/integration \
    -run="^TestPackagedSessions$" -count=1 -args -image="papilio-node-build:$revision"

go test -race -tags=integration,distribution ./internal/apps/system/host/sensors/integration \
    -run="^TestPackagedSensors$" -count=1 -args -image="papilio-node-build:$revision"

go test -race -tags=integration,distribution ./internal/apps/system/node/runtime/integration \
    -run="^TestPackagedNodeRuntime$" -count=1 -args -image="papilio-node-build:$revision"

go test -race -tags=integration,distribution ./internal/apps/xray/runtime/instances/integration \
    -run="^TestPackagedInstances$" -count=1 -args -image="papilio-node-build:$revision"

go test -race -tags=integration,distribution ./internal/apps/xray/runtime/configs/integration \
    -run="^TestPackagedConfigurationValidation$" -count=1 -args -image="papilio-node-build:$revision"

go test -race -tags=integration,distribution ./internal/apps/xray/runtime/statistics/integration \
    -run="^TestPackagedRuntimeStatistics$" -count=1 -args -image="papilio-node-build:$revision"

go test -race -tags=integration,distribution ./internal/apps/xray/observability/loggers/integration \
    -run="^TestPackagedLoggerReopening$" -count=1 -args -image="papilio-node-build:$revision"


go test -race -tags=integration,distribution ./internal/apps/xray/proxies/inbounds/integration \
    -run="^TestPackagedInboundManagement$" -count=1 -args -image="papilio-node-build:$revision"

go test -race -tags=integration,distribution ./internal/apps/xray/proxies/outbounds/integration \
    -run="^TestPackagedOutboundReadback$" -count=1 -args -image="papilio-node-build:$revision"

go test -race -tags=integration,distribution ./internal/apps/xray/users/accounts/integration \
    -run="^TestPackagedAccountReads$" -count=1 -args -image="papilio-node-build:$revision"
