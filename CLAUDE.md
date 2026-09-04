# CLAUDE.md

For project conventions, build commands, architecture, and the hard-won
gotchas, see [AGENTS.md](AGENTS.md). Everything below is specific to the
Claude Code operator experience on this repo.

## CI Gate

**MANDATORY: `just ci` must pass locally before every `git push`. No
exceptions.** Do not rely on GitHub Actions to catch it — run the full
gate locally and fix what it finds. This is a hard requirement.

## Verify on hardware, not in your head

This repo's whole subject is a machine you are not running on. The rules
that turned out to matter — `ProcSubset=pid` hiding `/proc/meminfo`,
`DeviceAllow` needing `rw`, `StartLimitIntervalSec` being ignored in the
wrong section — were all invisible locally and obvious within one command
on a real node.

Two habits follow.

**Check metric and field names against a live server**, never against
recall. `vllm:request_num_preemptions_total` is a name that reads as
correct, does not exist, and returns a convincing zero. `just
refresh-fixture <url>` re-records the scrape the name contract is
asserted against; read the resulting diff rather than committing it
blind, because a metric that vanished is precisely what the fixture
exists to catch.

**Exercise the sandbox, do not reason about it.** `systemd-run` with the
unit's properties runs a command under the real restrictions:

```bash
sudo systemd-run --quiet --wait --pipe --property=ProcSubset=pid cat /proc/meminfo
```

That one line is what found the meminfo defect after it had already been
committed.

## Testing changes on a node

Copy to `/tmp`, install, and clean up after. Do not leave a half-
installed service on a machine that is serving production inference.

```bash
just build-node
scp dist/sparkwrangler-linux-arm64 <node>:/tmp/sparkwrangler
scp deploy/sparkwrangler.service <node>:/tmp/
ssh <node> 'sudo cp /tmp/sparkwrangler.service /run/systemd/system/ && sudo systemctl daemon-reload'
```

`/run/systemd/system/` rather than `/etc/`: it is tmpfs, so a reboot
discards the experiment and nothing outlives the test.

Remote shells on the nodes are **fish**, not bash. `VAR=value` fails
there; inline the value or use `set`.

## Publishing to a live broker

Publishing discovery creates real devices and entities in someone's Home
Assistant, and retained messages persist until explicitly cleared —
including under topic prefixes that no longer match anything, which read
as a ghost device nobody can delete from the UI. Confirm with the
operator before first publishing to a broker that is not a scratch one,
and prefer a distinct `-topic-prefix` when testing.

## Mutation-test the tests that matter

Several tests here exist to catch silent failures — the metric-name
contract, the discovery/state agreement, the absent-is-not-zero
behaviour. A test guarding a silent failure is itself easy to write
silently wrong. Break the thing on purpose, confirm the test fails, then
invert the edit. Never `git checkout` the file to undo it; make the
opposite edit, so an unrelated change in the same file is not lost.

## GitHub Collaboration

Be a good collaborator. Review threads left open signal unfinished work.

1. Fix the issue in a commit
2. Reply to the thread with the fixing commit hash and a one-line
   explanation
3. Resolve the conversation
4. If deferring, say so explicitly before resolving

Note that the agent account has `push` but not `admin` on this
repository, so repo settings, renames, and branch protection are the
owner's to change. A 404 from `gh` on an admin operation means
permission, not absence.
