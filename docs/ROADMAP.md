# NanoBird roadmap

The specification is in [SPEC.md](SPEC.md); section numbers refer to it. NanoBird moves through five stages, and each closes on a measured gate (SPEC §12). A stage that misses its gate stays open, and the next one does not start. Arm names (A0–A3, F0, F1) are defined in SPEC §10.2.

| Stage | Name | Lands | Gate |
| --- | --- | --- | --- |
| 0 | Baseline | Stock Mingbird on NanoLLM (Ollama dialect), E4B. Fidelity runs on `gemma4:e2b` and `qwen3.5:4b`. Engine doctor. | **G0:** 3 replications of A0 and F0 recorded; doctor probes pass on NanoLLM |
| 1 | Core port | Loop, tools, M1–M10, safety rings, OpenAI client. Headless run, transcripts, golden tests. | **G1:** F1 within F0's replication spread; prefill budget test green in CI |
| 2 | ACP | ACP agent: sessions, permissions, plan, modes. LRAB runner as an ACP client. | **G2:** ACP test kit passes in CI; ACP and headless runs agree within spread |
| 3 | NanoLLM primitives | Native backend on RFC R1–R5 as NanoLLM ships them. Knob sweeps decide the defaults. | **G3:** A1 ≥ A0 on E4B, the success criterion (SPEC §1); A1 − A2 measured and published |
| 4 | Release | Held-out tasks, τ²-bench, docs, ACP Registry. Tag v1.0. | **G4:** held-out score ≥ A0 held-out; τ²-bench ≥ Mingbird under one protocol |

## Decisions after the spec

| Decision | What changes from the spec |
| --- | --- |
| macOS waits | No macOS CI job and no darwin release targets for now; the project builds and tests for Windows and Linux. Spec §11.1 still lists macOS, so this is a deliberate deviation. |

## Placement the spec leaves open

The spec does not put every part of the design into a stage. These are my readings; confirm or correct them before the plan is final.

- **Flat MCP client.** Stage 1 includes M9, progressive disclosure, which covers MCP loading up to `mcp.flat_limit` tools (SPEC §5.9, §6.4). `internal/mcp` therefore lands with stage 1.
- **Scoped MCP (N6).** No stage. Its default is decided on the MCP scale suite (SPEC §10.9), after hypotheses H20–H25 (SPEC §10.8).
- **Decision hooks (N5).** No stage. Each hook stays off until it passes the offline decision set (SPEC §10.7) and then the LRAB leave-one-out (SPEC §5.14).
- **Engine doctor.** G0 needs doctor probes on NanoLLM, so `nanobird doctor` (SPEC §7.4) is a stage 0 deliverable, ahead of the rest of the binary.
- **Hypotheses H1–H25.** SPEC §10.8 gives their test order, not their stages. Each runs as an LRAB arm in the stage whose code it touches.
- **Release mechanics.** Stage 4 says "Tag v1.0" but does not specify the release workflow or the archive scripts. SPEC §11.3 says they mirror NanoLLM (`release.yml`, `scripts/dist.sh`).
