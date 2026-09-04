# sparkwrangler — vLLM node telemetry to Home Assistant over MQTT

default: ci

# Everything CI runs. Must pass before pushing.
ci: fmt-check vet test

build:
    go build -o sparkwrangler ./cmd/sparkwrangler

# Cross-compile for the nodes, which are arm64 Linux.
build-node:
    GOOS=linux GOARCH=arm64 go build -o dist/sparkwrangler-linux-arm64 ./cmd/sparkwrangler

test:
    go test ./...

vet:
    go vet ./...

fmt:
    gofmt -w cmd internal

fmt-check:
    @test -z "$(gofmt -l cmd internal)" || (echo "Files need formatting:" && gofmt -l cmd internal && exit 1)

# Refresh the recorded scrape the metric-name contract is asserted
# against. Run when upstream vLLM changes, then read the diff: a
# disappearing metric is the thing this fixture exists to catch.
refresh-fixture url:
    curl -s {{url}}/metrics | grep -E '^(#|vllm:)' > internal/vllm/testdata/live-scrape.txt

# Lint the unit file. Run on a systemd host; needs no root.
unit-check:
    systemd-analyze verify deploy/sparkwrangler.service
    systemd-analyze security deploy/sparkwrangler.service
