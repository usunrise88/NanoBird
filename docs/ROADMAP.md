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

## Placement the spec leaves open

The spec does not put every part of the design into a stage. These are my readings; confirm or correct them before the plan is final.

- **Flat MCP client.** Stage 1 includes M9, progressive disclosure, which covers MCP loading up to `mcp.flat_limit` tools (SPEC §5.9, §6.4). `internal/mcp` therefore lands with stage 1.
- **Scoped MCP (N6).** No stage. Its default is decided on the MCP scale suite (SPEC §10.9), after hypotheses H20–H25 (SPEC §10.8).
- **Decision hooks (N5).** No stage. Each hook stays off until it passes the offline decision set (SPEC §10.7) and then the LRAB leave-one-out (SPEC §5.14).
- **Engine doctor.** G0 needs doctor probes on NanoLLM, so `nanobird doctor` (SPEC §7.4) is a stage 0 deliverable, ahead of the rest of the binary.
- **Hypotheses H1–H25.** SPEC §10.8 gives their test order, not their stages. Each runs as an LRAB arm in the stage whose code it touches.
- **Release mechanics.** Stage 4 says "Tag v1.0" but does not specify the release workflow or the archive scripts. SPEC §11.3 says they mirror NanoLLM (`release.yml`, `scripts/dist.sh`).
