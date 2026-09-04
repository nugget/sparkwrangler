# sparkwrangler — vLLM node telemetry to Home Assistant over MQTT

# Recipes emit their own output; use `just --verbose <recipe>` when
# debugging what actually ran.
set quiet

# One pinned toolchain for every recipe, rather than whatever Homebrew or
# apt last installed, so the gate's verdict is identical on every machine
# and matches CI's GO_VERSION. The lesson behind pinning: a toolchain
# bump once changed gofmt's comment alignment and failed fmt-check on
# files that main had always considered formatted. Bump this together
# with GO_VERSION in .github/workflows/ci.yml.
export GOTOOLCHAIN := "go1.27.0"

golangci := "go run -modfile=tools/go.mod github.com/golangci/golangci-lint/v2/cmd/golangci-lint"

default: ci

# The full gate. Must pass before pushing; CI is the safety net, not the
# first line of defence.
ci: fmt-check vet lint test

[doc("Build for this machine")]
build:
    go build -o sparkwrangler ./cmd/sparkwrangler

[doc("Cross-compile for the nodes, which are arm64 Linux")]
build-node:
    GOOS=linux GOARCH=arm64 go build -o dist/sparkwrangler-linux-arm64 ./cmd/sparkwrangler

[doc("Race tests, which is the only way they are run")]
test:
    go test -race ./...

[doc("Race tests with coverage in one pass; writes coverage.out for CI")]
cover:
    go test -race -coverprofile=coverage.out ./...

vet:
    go vet ./...

lint:
    {{golangci}} run ./...

fmt:
    "$(go env GOROOT)/bin/gofmt" -w cmd internal

# gofmt comes from the pinned toolchain's GOROOT rather than the PATH:
# the bare binary ignores GOTOOLCHAIN, so calling it directly would
# reintroduce the per-machine skew the pin exists to end.
fmt-check:
    @test -z "$("$(go env GOROOT)/bin/gofmt" -l cmd internal)" || (echo "Files need formatting:" && "$(go env GOROOT)/bin/gofmt" -l cmd internal && exit 1)

[doc("Lint the systemd unit. Run on a systemd host; needs no root")]
unit-check:
    systemd-analyze verify deploy/sparkwrangler.service
    systemd-analyze security deploy/sparkwrangler.service

# Refresh the recorded scrape the metric-name contract is asserted
# against. Run when upstream vLLM changes, then read the diff: a
# disappearing metric is exactly what this fixture exists to catch.
[doc("Re-record the vLLM metrics fixture from a live server")]
refresh-fixture url:
    curl -s {{url}}/metrics | grep -E '^(#|vllm:)' > internal/vllm/testdata/live-scrape.txt
