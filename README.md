# sparkrustler

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
| GPU adapter (nvidia-smi) | done, tolerates `[N/A]` fields |
| Host memory adapter | done |
| Config, daemon, systemd unit | done |
| Run against a real broker | not yet |

## Install

```sh
just build-node                       # linux/arm64, which is what a Spark is
scp dist/sparkrustler-linux-arm64 <node>:/usr/local/bin/sparkrustler
scp deploy/sparkrustler.service <node>:/etc/systemd/system/
```

Settings come from flags or `SPARKRUSTLER_`-prefixed environment
variables; the unit reads `/etc/sparkrustler.env` so the broker password
stays out of a world-readable unit file.

```sh
sparkrustler \
  -node-id spark-a23e \
  -vllm-url http://localhost:8000 \
  -broker tcp://mqtt.example.net:1883 \
  -device-model "DGX Spark (GB10)"
```

Run one per node. Each publishes its own device, and Home Assistant
assembles them.

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

**Available host memory** is the leading indicator of a wedge. On
unified-memory hardware the accelerator and the page cache draw on one
pool, so a large download competes with a resident model, and the
failure is not a clean OOM — it is userspace starvation, where sshd
accepts a connection and then cannot fork.

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
| `cmd/sparkrustler` | the daemon: poll, observe, publish |
| `internal/vllm` | metrics parsing, HTTP client, the reading model |
| `internal/hadiscovery` | HA device discovery types and the sensor catalog |
| `internal/publisher` | state payload, derived rates, MQTT transport |
| `internal/gpu` | accelerator adapters; `nvidia-smi` today |
| `internal/host` | host memory, which on unified memory is the number that matters |
| `internal/config` | flags and environment |
| `deploy/` | systemd unit |

## Tests

```sh
just ci
```

`internal/vllm/testdata/live-scrape.txt` is a recorded scrape from a real
two-node vLLM server. Refresh it against your own server if upstream
metric names change:

```sh
curl -s http://<node>:8000/metrics | grep -E '^(#|vllm:)' > internal/vllm/testdata/live-scrape.txt
```

## License

MIT
