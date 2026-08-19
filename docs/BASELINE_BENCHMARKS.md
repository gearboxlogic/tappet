# Milestone 0 benchmark baseline

Recorded: 2026-08-19

These benchmarks measure the inherited fixed outward surface and the common in-process server-construction path. They intentionally exclude command startup, configuration and hierarchy disk I/O, transport binding, and downstream provider startup. Provider startup remains lazy and is covered by lifecycle characterization tests.

Run:

```bash
go test ./internal/server -run '^$' \
  -bench 'Benchmark(NewCapScopeServer|OutwardToolSurfaceEncoding)$' \
  -benchmem -count=5
```

Reference environment:

```text
go version go1.24.13 linux/amd64
AMD Ryzen 9 9950X3D2 16-Core Processor
```

Median results from five runs:

| Benchmark | Time | Heap bytes | Allocations | Fixed surface |
| --- | ---: | ---: | ---: | ---: |
| `BenchmarkNewCapScopeServer-32` | 1,136 ns/op | 4,932 B/op | 42 allocs/op | 2 tools |
| `BenchmarkOutwardToolSurfaceEncoding-32` | 6,018 ns/op | 4,617 B/op | 59 allocs/op | 2 tools, 1,095 JSON bytes |

The values are a comparison baseline, not a performance budget. Re-run on the same host and Go toolchain when changing outward tool definitions or the shared constructor.
