# NanoBird

NanoBird is a lightweight agent harness for small local models. It ports the mechanism set of [Mingbird](https://github.com/Mingbird/Mingbird-agent) (Apache-2.0) to one Go binary. Inference runs on [NanoLLM](https://github.com/usunrise88/NanoLLM), and editors connect over the Agent Client Protocol (ACP). The primary target is Gemma 4 E4B.

## Status

Stage 1 in progress. The agent loop, the built-in tools, the workspace and shell checks, transcripts, checkpoints with `run --resume`, and a headless `nanobird run` are in, with golden tests against a scripted model. The loop-side mechanisms are in: the category router (M1), the chat/task classifier (M2), the finish gate with its self-check and test guard (M3), the anti-loop refusals and escalation ladder (M4), format rescue (M5), the convergence notice (M6), skills (M9), staged compaction (N3), closest tool names (N4), the credential check on shell commands (M8), retries (M7) and the write guard (N1). MCP servers load flat over stdio. Structured logs in `.nanobird/logs` and the plan thinking policy are in. Scoped MCP disclosure and ACP are not built yet.

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
make cross    # the four release targets (Linux, Windows) into dist/<os>_<arch>/
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
