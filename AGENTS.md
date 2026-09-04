# AGENTS.md

Conventions for working on sparkwrangler. Read this before changing
code; the [Gotchas](#gotchas) section in particular is the accumulated
cost of things that looked obviously correct and were not.

## What this is

A daemon that runs on one vLLM node, reads what that node is doing, and
publishes it to Home Assistant as an MQTT device. It observes and
publishes. It cannot start, stop, or reconfigure anything, and that is a
deliberate boundary rather than an unimplemented feature — see
[Scope](#scope).

## Build & Test

**`just ci` must pass before every push. No exceptions.** GitHub Actions
is a safety net, not the first line of defence.

```sh
just ci           # fmt-check, vet, lint, race tests — the whole gate
just test         # race tests alone
just build-node   # linux/arm64, which is what a Spark is
just unit-check   # systemd-analyze verify + security; needs a systemd host
```

The Go toolchain is pinned in the justfile and mirrored by `GO_VERSION`
in `.github/workflows/ci.yml`; bump them together. `gofmt` is invoked
from the pinned toolchain's `GOROOT` rather than from `PATH`, because
the bare binary ignores `GOTOOLCHAIN` and would reintroduce exactly the
per-machine formatting skew the pin exists to end.

Linting is hermetic: golangci-lint is pinned in `tools/go.mod` and run
via `go run`, so CI installs nothing and lints with the same rules a
developer does.

## Scope

The daemon is read-only against the node and write-only against the
broker. It never subscribes to a topic and has no command surface.

This matters for review: a change that gives it authority over vLLM's
lifecycle is not a feature addition, it is a change of category. It
would inherit every hazard in the operational envelope — teardown
ordering, the unified-memory budget, the RoCE GID pin — and those belong
in a design discussion before any code.

## Code Conventions

- **Absent is not zero.** This is the project's central rule. Every
  optional reading is a pointer, omitted from the payload when missing,
  and rendered by the discovery templates as unavailable. A metric that
  is renamed upstream is simply not found, reads as zero, and zero is
  also the healthy value for a queue depth or a preemption counter. **A
  permanently calm sensor is worse than a missing one**, because nobody
  investigates a dashboard that looks fine.
- **Prefer the standard library.** There is exactly one third-party
  dependency, `paho.mqtt.golang`, because MQTT has no stdlib equivalent.
  The Prometheus parser and the systemd notification protocol are both
  hand-written against documented formats in less code than the
  dependency would have cost. Adding a second dependency needs an
  argument.
- **Metric and topic names are constants**, asserted in tests against a
  recorded scrape. Never inline a `vllm:` string at a call site.
- **Tests are table-driven and always run with `-race`.**
- **Structured logging via `slog`.** INFO is the operator story, DEBUG is
  troubleshooting, WARN is degraded, ERROR is broken. A failed
  observation from one source is WARN and leaves that reading absent; it
  never fails the cycle, because a node whose `nvidia-smi` is missing
  should still report its vLLM state.
- **Errors are wrapped with context** and never swallowed. The one
  deliberate exception is a source being unavailable on this platform —
  no `/proc/meminfo`, no `nvidia-smi` — which is a state to publish, not
  an error to propagate to a loop that would log it every interval
  forever.
- **A configuration that looks secure and is not must fail, not warn
  quietly.** TLS options against a plaintext broker URL are an error;
  a password against a plaintext URL is a startup warning naming the
  fix. The rule is the same one as absent-is-not-zero: the dangerous
  state is the one that looks fine.
- **No operator's hostnames in the repo.** Examples use `spark-01` and
  `mqtt.example.net`. This is a general tool, and someone's network
  topology is not documentation.
- **The systemd unit is part of the interface.** It is gated in CI and
  reviewed like code.

## Documentation

GoDoc is a product surface, not commentary on the source. `godoclint`
runs with `require-pkg-doc`, so every package explains what it is for
before it explains itself.

**Exported symbols** document the contract, not the Go shape. Every one
gets a comment starting with its name that reads as a complete sentence.
Where several functions are closely related, each still gets its own —
one comment spanning three functions is how the reader loses the
distinction between them.

**Unexported symbols** answer a different question: *why is this here in
its current form?* The test:

> If a contributor deleted this symbol in a PR, would the surrounding
> code make clear why that's wrong?

If no, document the why. If yes, no comment is needed. This bar exists
to prevent two failure modes: decisions that look like accidents, and
reflexive comments that restate the signature.

**Rules discovered by testing carry their evidence.** A comment saying
"read-write, not read-only" is worth little; one saying NVML opens the
nodes `O_RDWR` even for a read-only query, and that narrowing to `r`
fails with `Failed to initialize NVML: Unknown Error`, prevents someone
from making the change back. Several comments in this repo exist purely
to stop a plausible-looking edit.

## Architecture at a Glance

```
cmd/sparkwrangler   poll loop: observe every source, publish, ping the watchdog
internal/vllm       metrics parsing, HTTP client, the reading model
internal/gpu        accelerator adapters behind an interface; nvidia-smi today
internal/host       host memory, which on unified memory is the number that matters
internal/hadiscovery  HA device-discovery types and the sensor catalog
internal/publisher  state payload, derived rates, MQTT transport
internal/sdnotify   systemd readiness, status and watchdog protocol
internal/config     flags and environment
deploy/             the systemd unit and an environment example
```

One binary runs per node and publishes one HA device. There is no
central collector; Home Assistant is the aggregator.

The discovery catalog and the state payload are two halves of one
contract — discovery declares what each entity reads, state decides what
is published — and they are checked against each other in both
directions by `TestDiscoveryTemplatesMatchStateKeys`. A mismatch produces
an entity that is permanently unknown and logs nothing anywhere, so that
test is load-bearing. Adding a sensor means touching both files.

## Gotchas

Each of these was found by running against real hardware, and each was
invisible to tests and to reading documentation.

- **`ProcSubset=pid` hides `/proc/meminfo`.** It improves the systemd
  hardening score and silently removes the most important reading on
  unified-memory hardware. Because a missing meminfo is treated as a
  platform that does not publish one, the sensor goes quietly blank
  forever. Do not add it back.
- **`DeviceAllow=` entries must be `rw`, not `r`.** NVML opens the
  device nodes read-write even to answer a read-only query. Narrowing
  them looks obviously correct and breaks every accelerator sensor.
- **`StartLimitIntervalSec` and `StartLimitBurst` belong in `[Unit]`.**
  In `[Service]` systemd parses and ignores them, so the crash-loop
  guard silently does nothing. `systemd-analyze verify` is the only
  thing that tells you.
- **`strconv.ParseFloat("NaN")` succeeds.** vLLM emits `NaN` for
  histograms with no observations. Reading it as a value publishes a real
  latency of zero, and `encoding/json` refuses to marshal it, so a single
  unobserved histogram fails the entire state payload.
- **Metric names are guessable and wrong.** `vllm:num_preemptions_total`
  is the real name; the plausible `vllm:request_num_preemptions_total`
  does not exist and read a convincing zero against a live server. The
  name constants and their fixture test exist because of this.
- **The MQTT will must be registered before connecting.** A will set
  afterwards is not part of the session the broker recorded, so a node
  that loses power publishes nothing and its entities keep their last
  healthy values. A clean shutdown never fires a will at all, which is
  why `SIGTERM` publishes an explicit offline.
- **The watchdog is pinged from inside the work loop, after the work.** A
  ping on its own goroutine attests only that the goroutine lives, which
  is precisely the state a wedged poll loop would be in.

## Contributing

Conventional commits: `feat:`, `fix:`, `docs:`, `refactor:`, `test:`,
`chore:`. Never push to `main` — branch and open a PR.

Commit messages carry the reasoning. A message that says what changed
duplicates the diff; one that says what was believed, what turned out to
be true, and what it cost is the only durable record of why the code
looks like this.

Pull requests keep the test plan honest: check items off as they are
actually verified, and say plainly when something was not.
