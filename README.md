# sparkwrangler

Publishes the state of a vLLM node to Home Assistant over MQTT, as a
device with sensors rather than a wall of text in a terminal.

One agent runs per node. It reads vLLM's own HTTP surface and the host's
accelerator, publishes a single Home Assistant device-discovery message,
and then publishes state on an interval. Home Assistant does the rest:
history, graphs, statistics, alerting, and a view a human and an agent
can both read.

Nothing here is specific to one deployment. The GB10 quirks live behind a
platform adapter; the vLLM half is whatever vLLM exports.

## Status

Working end to end and not yet run in anger. One dependency,
`paho.mqtt.golang`, because MQTT has no standard-library equivalent;
everything else is stdlib, including the Prometheus parser.

| | |
|---|---|
| vLLM metrics parsing | done, tested against a recorded live scrape |
| vLLM client and reading model | done, verified against a live 2-node server |
| HA device discovery payloads | done, full entity metadata |
| State payload and derived rates | done |
| Discovery/state contract tests | done |
| MQTT transport, LWT, reconnect | done |
| sd_notify: readiness, status, watchdog | done, stdlib |
| Hardened unit | done, 1.6 OK, verified on DGX OS |
| GPU adapter (nvidia-smi) | done, tolerates `[N/A]` fields |
| Host memory adapter | done |
| Config, daemon, systemd unit | done |
| Run against a real broker | not yet |

## Install

```sh
just build-node                       # linux/arm64, which is what a Spark is
scp dist/sparkwrangler-linux-arm64 <node>:/tmp/sparkwrangler
scp deploy/sparkwrangler.service deploy/sparkwrangler.env.example <node>:/tmp/
```

On the node:

```sh
sudo install -m0755 /tmp/sparkwrangler /usr/local/bin/sparkwrangler
sudo install -m0644 /tmp/sparkwrangler.service /etc/systemd/system/
sudo install -m0600 /tmp/sparkwrangler.env.example /etc/sparkwrangler.env
sudo systemctl daemon-reload && sudo systemctl enable --now sparkwrangler
```

Run one per node. Each publishes its own device, and Home Assistant
assembles them.

## Worker nodes

On a tensor-parallel cluster only the head node serves the API. The
others hold half the weights and are doing exactly the same work, but
there is no engine on them to ask.

Leave `-vllm-url` empty on those nodes. The daemon then publishes
accelerator and host readings and declares no serving entities at all,
rather than a dozen sensors that can only ever read unknown next to a
vLLM indicator stuck off — which looks like a broken node instead of a
correctly configured one.

The state payload follows the same rule: a worker omits `vllm_up`
entirely rather than publishing `false`. False means the engine should be
here and is not; a worker has no engine to be missing.

## The unit

`Type=notify`, not `Type=simple`. The daemon signals readiness once the
broker connection exists, reports what it is doing through `STATUS`, and
pings the watchdog **from inside its work loop** — a ping from a separate
goroutine would prove only that the goroutine lives, which is exactly the
state a wedged poll loop would be in. `systemctl status` therefore reads
like this rather than "active (running)":

```
Status: "serving Qwen/Qwen3.5-122B-A10B-FP8, 4 running, 2 waiting, KV 88.5%"
```

The protocol is implemented in `internal/sdnotify` against the documented
wire format — a datagram of key=value pairs — rather than taken as a
dependency, and degrades to a no-op outside systemd.

Hardening scores **1.6 OK** on `systemd-analyze security`, systemd's best
band. Verify after any edit:

```sh
just unit-check
```

Two settings in it are load-bearing and counterintuitive, both found by
testing on real hardware rather than by reading documentation:

**`DeviceAllow=` entries are `rw`, not `r`.** NVML opens the device nodes
read-write even to answer a read-only query. With read-only entries
`nvidia-smi` fails with `Failed to initialize NVML: Unknown Error`, and
every accelerator sensor goes blank.

**`ProcSubset=pid` is deliberately absent.** It improves the hardening
score and hides `/proc/meminfo` — the single most important reading on
unified-memory hardware. Because a missing meminfo is indistinguishable
from a platform that does not publish one, the sensor would go quietly
blank forever rather than failing.

## Why these sensors

The entity set is drawn from what an actual incident needed, not from
what the exposition happens to contain.

**Queue depth and preemptions** separate *busy* from *oversubscribed*.
`num_requests_waiting` above zero on a single-user deployment already
means something is wrong; `waiting_for_capacity` and a rising preemption
counter say the KV pool is genuinely too small for the offered load.

**Prefix cache hit rate** explains a latency change that nothing else
accounts for. On a conversational workload it is most of the reason
repeat turns are cheap, and a collapse in it is invisible everywhere
else.

**GPU rail power** is named for what it measures, which is narrower than
"the machine". On GB10 the driver populates GPU Power Readings and marks
Module Power Readings, GPU Memory Power Readings and every power limit
`N/A`, so the figure excludes the Grace cores and the LPDDR5X subsystem
— a large share of draw on unified memory. It reads roughly 9 W idle and
21 W at 95% utilisation on a node with a 240 W supply. There is no
broader number to publish: the host exposes no `hwmon` power rails and
no `tegrastats`, so a wall meter is the only route to true system draw.
If you have one, its reading and this one will differ by a lot, and both
are correct.

**Available host memory** is the leading indicator of a wedge. On
unified-memory hardware the accelerator and the page cache draw on one
pool, so a large download competes with a resident model, and the
failure is not a clean OOM — it is userspace starvation, where sshd
accepts a connection and then cannot fork.

**Memory used**, as a percentage, is that same reading in the form a
gauge card can draw — derived from it rather than measured separately,
so the two can never disagree. It is the share of memory a new
allocation could not get, which is not what `free` calls used:
reclaimable page cache counts as available, so a node that has just
pulled a 60 GB model reads calmer here than its resident-set accounting
would suggest. That is the intended reading. The question is how close
the next allocation is to failing, not where the bytes went.

**The MAC address** identifies this node as a machine Home Assistant may
know by other means. It is published twice: as a `connections` entry in
the device record, which is the registry's field for a hardware address,
and as a diagnostic sensor.

Be clear about what that does and does not buy, because it changed
recently. Until Home Assistant 2026.8 a connection shared with another
integration merged both records into one device. 2026.8 scoped
identifiers and connections to a single config entry and split the prior
composite devices, so **an MQTT device no longer merges with the one a
DHCP or router integration has for the same host, and nothing in a
discovery payload can make it.** Home Assistant offers no replacement —
`via_device` is deprecated and child devices are unimplemented future
work. The connection entry stays because it is still the correct field
for the address and still what any future correlation would need; the
sensor is the half that is useful today, since a template or automation
can read it and correlate the two itself. `-area` suggests where the
device lands, which is what actually puts it beside the other records
for the same machine.

The address is read from `/sys/class/net`, and the interface chosen is
the one carrying the default route — a Spark has two QSFP fabric ports
whose kernel names sort ahead of the RJ45 the house network sees, so
"the first one" would publish an address nothing else has ever heard of.
The route is followed even when it leaves over a bridge, bond or VLAN,
which is filed as a virtual device and is nonetheless exactly the
address the router sees. `-net-interface` overrides the choice, and a
name that cannot be read stops startup rather than being absorbed.

## Design notes

**Absent is not zero.** Every optional reading is a pointer, omitted from
the payload when the metric is missing, and rendered by the discovery
templates as unavailable. This is not fastidiousness: a metric that is
renamed upstream is not found, reads as zero, and zero is also the
healthy value for a queue depth or a preemption counter. A sensor that
is silently always-calm is worse than one that is missing. The metric
names are constants, asserted in tests against a recorded scrape from a
real server, so a rename fails loudly.

**The discovery payload and the state payload are checked against each
other.** Discovery declares what each entity reads; state decides what is
published. A disagreement produces an entity that is permanently unknown
and logs nothing. `TestDiscoveryTemplatesMatchStateKeys` fails in both
directions — a template with no field behind it, and a field no entity
reads.

**Device-based discovery.** One retained message declares the node and
every sensor on it, so a node appears and disappears as a unit rather
than as a drift of orphaned entities.

**Entity metadata is filled deliberately.** Device classes decide unit
conversion and long-term statistics; state classes decide whether a value
is graphable and how it is summed — `total_increasing` for cumulative
counters, `measurement` for gauges, and neither for launch parameters,
which are not measurements and should not put flat lines in statistics.
Entity categories keep configuration facts out of the way of live
readings.

**A down engine publishes nothing it cannot know.** Carrying the last
good values forward would leave a calm dashboard over a dead server.

## Layout

| path | |
|---|---|
| `cmd/sparkwrangler` | the daemon: poll, observe, publish |
| `internal/vllm` | metrics parsing, HTTP client, the reading model |
| `internal/hadiscovery` | HA device discovery types and the sensor catalog |
| `internal/publisher` | state payload, derived rates, MQTT transport |
| `internal/gpu` | accelerator adapters; `nvidia-smi` today |
| `internal/host` | host memory, which on unified memory is the number that matters, and the network identity |
| `internal/config` | flags and environment |
| `internal/sdnotify` | systemd readiness, status and watchdog protocol |
| `deploy/` | systemd unit |

## Contributing

[AGENTS.md](AGENTS.md) carries the conventions, the architecture, and a
Gotchas section listing the things that looked obviously correct and were
not. Read it before changing code.

```sh
just ci        # fmt-check, vet, lint, race tests — the whole gate
just unit-check  # systemd-analyze verify + security; needs a systemd host
```

`just ci` must pass before pushing. Linting is hermetic: golangci-lint is
pinned in `tools/go.mod` and run through `go run`, so CI installs nothing
and lints with the same rules you do. `godoclint` runs with
`require-pkg-doc`.

`internal/vllm/testdata/live-scrape.txt` is a recorded scrape from a real
two-node vLLM server. Refresh it against your own server if upstream
metric names change:

```sh
curl -s http://<node>:8000/metrics | grep -E '^(#|vllm:)' > internal/vllm/testdata/live-scrape.txt
```

## License

MIT
