# NanoBird roadmap

The specification is in [SPEC.md](SPEC.md); section numbers refer to it. NanoBird moves through five stages, and each closes on a measured gate (SPEC §12). A stage that misses its gate stays open, and the next one does not start. Arm names (A0–A3, F0, F1) are defined in SPEC §10.2.

| Stage | Name | Lands | Gate |
| --- | --- | --- | --- |
| 0 | Baseline | Stock Mingbird on NanoLLM's OpenAI dialect, E4B (A0). No Ollama, so F0 has no replacement yet (see Decisions). Engine doctor. | **G0:** 3 replications of A0 and F0 recorded; doctor probes pass on NanoLLM |
| 1 | Core port | Loop, tools, M1–M10, safety rings, OpenAI client. Headless run, transcripts, golden tests. | **G1:** F1 within F0's replication spread; prefill budget test green in CI |
| 2 | ACP | ACP agent: sessions, permissions, plan, modes. LRAB runner as an ACP client. | **G2:** ACP test kit passes in CI; ACP and headless runs agree within spread |
| 3 | NanoLLM primitives | Native backend on RFC R1–R5 as NanoLLM ships them. Knob sweeps decide the defaults. | **G3:** A1 ≥ A0 on E4B, the success criterion (SPEC §1); A1 − A2 measured and published |
| 4 | Release | Held-out tasks, τ²-bench, docs, ACP Registry. Tag v1.0. | **G4:** held-out score ≥ A0 held-out; τ²-bench ≥ Mingbird under one protocol |

## Decisions after the spec

| Decision | What changes from the spec |
| --- | --- |
| macOS waits | No macOS CI job and no darwin release targets for now; the project builds and tests for Windows and Linux. Spec §11.1 still lists macOS, so this is a deliberate deviation. |
| No Ollama | The engine is NanoLLM, reached through its OpenAI dialect. Nothing in NanoBird targets Ollama. Spec §7.3 and §10.2 (arm F0) still name Ollama and need a replacement arm before G0 can close. |
| Agent first | The core loop, the tools and the transcripts land before any benchmark. Spec §2 rule 5 (a mechanism lands only with an LRAB delta) and the gates of §12 still decide when a mechanism's default changes. |
| Gate for mechanisms | Tier 0: CI for every change (unit tests, golden transcripts, prefill budget, lang-check). Tier 1: offline replay over recorded transcripts for trigger counts and false rejections, once a corpus exists. Tier 2: before a default flips, a smoke set of four LRAB tasks, switch on against off, one replication, judged against the replication spread. Tier 3: the full protocol (18 tasks, three replications, held-out set) only at stages G3 and G4 and for release numbers. This replaces the per-change LRAB delta of SPEC §2, rule 5: a full matrix costs about 80 compute hours, too much for each change. |
| Mechanisms on by default | M1 to M6, M8, N1 and N4 ship on, as Mingbird ships them, so leave-one-out runs have a baseline (SPEC §10.4). Changing any default needs at least tier 2. |
| Prefill token budget | The 797 tokens of SPEC §5.1 is not a tokenizer count. It came from Mingbird's own heuristic estimator (CJK-calibrated), and Mingbird's docs call the wording stale; its current pin is 1,250 estimated tokens. Real counts of NanoBird's default prefill (system prompt and the nine core tool schemas, one user turn): Gemma 4 E4B 1,258 tokens rendered through its template (1,102 raw); Qwen3.5-4B 1,575 rendered (1,051 raw), because Qwen's tool block adds about 520 tokens of template text. The budget is therefore per model. `prefill.BudgetBytes` is pinned at 4,438 bytes, the core set. The router (M1) adds routed categories per task, so the per-task count is measured separately. |
| Ignore files | `list_dir` and `search_files` read only the root `.gitignore` of the workspace, with git's rules for the common forms (SPEC §6.1 says "ignore-file entries" without naming the files). Nested ignore files and git's global ignore are not read. |
| Stage 2 before G1 | Stage 2 started with the ACP agent while G1 is open. The agent-first decision (see Agent first) put the agent before the gate, and G1 still needs a replacement for arm F0 and the prefill budget test in CI. The gate is not closed by this work, and stage 2 is not closed either until G2 is met. |
| ACP test kit | The official ACP test kit (agentclientprotocol/acp-tck) is the conformance check. It runs as `make acp-tck` and in the `acp-tck` CI job, pinned to one commit and installed from its uv.lock (11 PyPI packages). NanoBird writes no kit of its own. The user allowed the kit after its dependencies were reviewed. |
| Offline option | The ACP option `offline` is a select (off, on), not the boolean of SPEC §8.4. The ACP rule allows a boolean only when the client advertised booleans, and the SDK does not expose that capability. |
| Delete of a missing session | `session/delete` succeeds for an id that is gone or that the agent has not listed (ACP rule D2). The agent moves only sessions of workspaces it has listed or opened in this process, so a session in another workspace stays on disk. |
| Client MCP servers | `session/new`, `session/load` and `session/resume` start the MCP servers that the client passes (SPEC §6.4). Stdio and streamable HTTP start. SSE and ACP servers do not start, and the client hears why. |
| Native backend | `model.backend: nanollm` (the default) talks to NanoLLM's native API (`/api/v1`) and probes the model once with `GET /api/v1/models/{id}/capabilities` (SPEC §7.2, RFC R1). It uses the tool format (`loop.tool_format`, falling back to rescue when the engine has no strict format), the parse report (RFC R4), the reasoning budget (R3, `max_reasoning_tokens`) and tokenization (R5). Prefix handles (R2) and cancellation that keeps partial output (R5) are not built, so NanoBird relies on the prompt cache and discards a cancelled turn. The engine side is NanoLLM PR #9 (`docs/HARNESS-API.md` there). `--backend openai` selects the OpenAI dialect for other engines. |
| Replacement for arm F0 | F0 named Ollama (SPEC §10.2). Stock Mingbird runs on any OpenAI-compatible local server without patches (its README), so A0 is stock Mingbird v3.1.4 on NanoLLM's OpenAI dialect, and F0 and F1 run on the same engine. The SPEC's "NanoLLM, Ollama dialect" wording for A0 is replaced by this, as the Ollama decision requires. |
| LRAB runner | The runner gives each cell the budget of SPEC §10.1: 90 minutes for a workflow cell and 180 for a long-horizon cell. The task files' `max_wall_minutes` (60 and 90) are authoring-time values; the protocol freeze raised them to 90 and 180 (Mingbird `benchmarks/DESIGN.md`). Permission requests are rejected unless `-permissions allow` is given, as an unattended run rejects them, so ACP and headless runs share one policy. An LH task with `resume_test` is killed at `kill_at_pct` and resumed with `session/resume`; the second attempt counts ("1 retry, latest attempt wins"). |

## Placement the spec leaves open

The spec does not put every part of the design into a stage. These are my readings; confirm or correct them before the plan is final.

- **Flat MCP client.** Stage 1 includes M9, progressive disclosure, which covers MCP loading up to `mcp.flat_limit` tools (SPEC §5.9, §6.4). `internal/mcp` therefore lands with stage 1.
- **Scoped MCP (N6).** No stage. Its default is decided on the MCP scale suite (SPEC §10.9), after hypotheses H20–H25 (SPEC §10.8).
- **Decision hooks (N5).** No stage. Each hook stays off until it passes the offline decision set (SPEC §10.7) and then the LRAB leave-one-out (SPEC §5.14).
- **Engine doctor.** G0 needs doctor probes on NanoLLM, so `nanobird doctor` (SPEC §7.4) is a stage 0 deliverable, ahead of the rest of the binary.
- **Hypotheses H1–H25.** SPEC §10.8 gives their test order, not their stages. Each runs as an LRAB arm in the stage whose code it touches.
- **Release mechanics.** Stage 4 says "Tag v1.0" but does not specify the release workflow or the archive scripts. SPEC §11.3 says they mirror NanoLLM (`release.yml`, `scripts/dist.sh`).

## Open gaps
- **Live runs.** The ACP test kit has run against the scripted model only. A run against NanoLLM needs the endpoint key, which the user exports, so it is not part of CI yet.
