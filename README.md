# NanoBird

NanoBird is a lightweight agent harness for small local models. It ports the mechanism set of [Mingbird](https://github.com/Mingbird/Mingbird-agent) (Apache-2.0) to one Go binary. Inference runs on [NanoLLM](https://github.com/usunrise88/NanoLLM), and editors connect over the Agent Client Protocol (ACP). The primary target is Gemma 4 E4B.

## Status

Bootstrap. The repository holds the specification, the package layout and the build and CI skeleton. No mechanism is implemented yet; each package is a stub that names the section of the specification it will implement. The stages and their gates are in [docs/ROADMAP.md](docs/ROADMAP.md).

## Documents

- [docs/SPEC.md](docs/SPEC.md): the development specification. Code comments cite it as `SPEC §5.3`.
- [docs/ROADMAP.md](docs/ROADMAP.md): the five stages and the measured gate that closes each one.
- [docs/clients.md](docs/clients.md): setup notes for ACP clients, written with stage 2.

## Build

Requires Go 1.27.3 (see [go.mod](go.mod)). The build uses no cgo and has no runtime dependencies.

```sh
make build    # dist/nanobird for this machine
make test     # unit tests
make lint     # go vet, gofmt, and golangci-lint when it is installed
make cross    # all six release targets into dist/<os>_<arch>/
```

## Layout

| Path | Contents |
| --- | --- |
| `cmd/nanobird` | entry point and CLI verbs |
| `internal/` | loop, mechanisms, tools, safety, model clients, ACP adapter, MCP client, skills, sessions, config |
| `tools/` | LRAB runner, MCP mock servers, decision-set evaluation |
| `docs/` | specification, roadmap, client notes |
| `testdata/golden/` | golden transcripts for the fake model |

## Rules

Three rules from the specification shape every change:

- Every text NanoBird writes for the model or the user is English (SPEC §5.13).
- The static prefill budget is pinned by a test. Growing it needs a recorded reason in the same commit (SPEC §2, rule 4).
- A mechanism or default change lands only with an LRAB delta on the reference machine (SPEC §2, rule 5).

## License

MIT, see [LICENSE](LICENSE). Third-party credits are in [NOTICE](NOTICE).
