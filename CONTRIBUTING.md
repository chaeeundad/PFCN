# Contributing

Thanks for helping build an open scientific computing commons.

## Ground rules

- Read the project constitution and frozen decisions in
  [docs/architecture.md](docs/architecture.md) (§2, §59). Changing a frozen
  decision needs an ADR in [docs/adr/](docs/adr/).
- Priority order when trade-offs appear (§63): security > scientific
  reproducibility > protocol correctness > contributor autonomy >
  decentralization > operational simplicity > UX > performance > features.
- No placeholder security checks. Errors fail closed.
- Every network message is versioned; signed objects use canonical JSON
  (`pkg/canonical`) and the envelope in `internal/envelope`.

## Development

```bash
make build      # bin/pumat
make test       # unit + integration tests, no containers needed
make race       # with the race detector
make lint       # go vet + gofmt
make e2e        # two local nodes with the real QE container (Docker/Podman)
```

Fuzz a decoder: `go test ./internal/solver/qe -run '^$' -fuzz FuzzParseCIF -fuzztime 60s`.

Changing `cmd/pumat-qe-parser` or `internal/qeparse` changes the parser
artifact digest: run `make parser`, commit the new `.wasm`, and publish a new
solver manifest that pins it. CI fails if the committed artifact is not
reproducible.

## Adding a solver

A solver needs: an image whose entrypoint wrapper is the only launcher, a job
`kind` with a strict adapter (parameter allowlist, adapter-controlled paths,
`BuildInput`), a WASI parser, reference tests with tolerances, and a signed
manifest. Open an issue first; public-namespace solvers go through review
(§53.2).

## Commits

Small, focused commits with a clear message. Include tests for behavior
changes. By contributing you agree that code contributions are licensed under
Apache-2.0 and documentation contributions under CC BY 4.0 (see README).
