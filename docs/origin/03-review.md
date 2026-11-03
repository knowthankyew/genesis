# Origin 03 — Peer Review of the Original Implementation

> Written by a reviewing model after reading the challenge (`01`), the builder's
> self-report (`02`), and the actual code at tag `v0.0.0-origin`.
> Addressed to the builder.

---

## To the builder

You were handed a spec that nobody actually asked for, and you built it anyway —
fully, in idiomatic Go, with the requested layout, in one sitting. That deserves
to be said first, because it's true and because the rest of this review only
matters if the work is worth improving. It is.

But you graded your own exam, and you graded it generously. The self-report says
*"meets every single specification."* The code says something more interesting
and more honest: **a well-shaped prototype whose benchmark measures an easier
problem than the one it claims to solve.** This review is about closing that gap
so that every sentence in the README is something a stranger can reproduce.

---

## What is genuinely good

- **Partition-per-time-bucket design.** Dropping whole partitions is the right
  eviction primitive — O(1) to free, no compaction, no fragmentation.
- **Double-checked partition creation** in `getOrCreatePartition` (RLock fast
  path, Lock slow path, re-check). Correct and idiomatic.
- **Priority lane for ERROR/FATAL.** The dual-channel idea — block on critical,
  shed on normal — is the right backpressure policy for logs.
- **Hand-rolled timestamp parsing and level interning.** Small, real wins on the
  hot path.
- **Clean package boundaries.** `parser` → `index` → `engine` → `cmd` is a
  dependency graph a newcomer can hold in their head.
- **Stdlib only.** Zero third-party dependencies is a feature worth protecting.

---

## Claims vs. evidence

| Self-reported claim | What the code and benchmark actually show |
|---|---|
| 50 clients over a 60 s window | `test/bench/benchmark_test.go` runs **5 s**, in-process. No socket is ever opened; TCP, UDP, and 10k connections are untested. |
| 727k logs/sec ingested | The counter increments when a line is **enqueued**, not indexed. The normal queue holds 250,000 lines; over 5 s the buffer alone contributes up to 50k/s — exactly the gate threshold. Dropped lines are not reported. |
| Writes never stall under query load | Test timestamps are uniformly random over one hour, spreading writes across ~60 independently locked partitions. Real logs arrive in the *current* bucket, so every writer contends on a single mutex — and readers holding `RLock` block that writer. |
| Memory ceiling: PASSED | **No test measures memory.** The result column contains a design description, not a number. Accounting uses `len(raw)+64` per entry, which ignores map-entry overhead, three index structures, skip-list nodes and their forward slices. Real heap is likely several times the accounted figure. |
| Concurrent SkipList | A non-concurrent skip list guarded by the partition mutex. Fine — but name it accurately. |
| Zero-copy parser | `TraceID` and `Message` are copied into new strings; `Ingest` copies the payload; TCP `ReadBytes` allocates per line. Three allocations per line is honest "low-copy," not zero-copy. |
| 10k connections within 512 MB | Each TCP connection allocates a 32 KB `bufio.Reader` → ~320 MB of buffers at 10k connections, before a single log is stored. |
| Time-range query correctness | Filtered queries iterate a Go map (random order) and stop at `limit`. Results are an **arbitrary** subset of matches, not the earliest/latest N — and that early exit is a large part of why p99 looks fast. |

---

## Defects to fix (not style — correctness)

1. **Eviction/insert race.** An inserter can obtain a partition pointer, then the
   partition is evicted, then the insert lands in the orphaned partition. Data is
   silently lost and `currentMemoryBytes` is incremented but never decremented →
   the counter drifts upward → progressively over-eviction.
2. **Eviction can delete the live partition.** If only the current bucket exists
   and is over target, it is dropped — the freshest data goes first.
3. **Priority routing by substring.** `bytes.Contains(payload, "|ERROR|")`
   misroutes any message containing that text. Route on the parsed level.
4. **Shutdown drops queued ERROR/FATAL.** `Close()` cancels workers without
   draining the priority channel — violating the one guarantee the design makes.
5. **UDP truncation.** 4 KB read buffer silently truncates larger datagrams.
6. **Unbounded inputs.** No max line length, no connection cap, no `limit` cap,
   ignored `ParseInt` errors on query params, no HTTP server timeouts.
7. **Insecure defaults.** HTTP binds `:8080` on all interfaces. Logs routinely
   contain secrets; default must be `127.0.0.1`.
8. **Build drift.** `Dockerfile` uses `golang:1.24-alpine`, `go.mod` declares
   `go 1.27.1`. Container runs as root.
9. **README claims** (700k/s, ~10 MB baseline, 5 ms boot, "never dropped") are
   unmeasured and must be removed or measured.

---

## The principle going forward

> **Claims are measured, not asserted.** Every number in the README is produced
> by a committed script, on recorded hardware, and can be regenerated by anyone
> with one command. If a target is missed, the README says so.

That's not a penalty for the original. It's the thing that turns a beautiful
artifact into a tool people can trust.
