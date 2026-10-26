# NanoBird Development Specification

> Imported from the NanoBird Development Specification, revision 51 (2026-10-26). The three figures are described in text; the interactive versions stay in the source artifact.

2026-10-26 · @Anton

## 1. Purpose and scope

NanoBird is a lightweight agent harness for small local models (2–12B): one Go binary that runs the Mingbird mechanism set, talks to NanoLLM for inference, and serves editors and UIs through the Agent Client Protocol (ACP). Section numbers here (`SPEC §5.3`) are referenced from code comments, as in NanoLLM and NanoASR.

**Primary target.** Gemma 4 E4B (Unsloth QAT, Q4_0, with MTP drafter) served by NanoLLM. Gemma 4 12B and Qwen3.5-9B follow as NanoLLM adds them. Any OpenAI-compatible engine works with reduced features (§7.3).

**Target hardware.** The machines NanoLLM targets: iGPU laptops (Iris Xe class) with 16 GB RAM, CUDA laptops, Apple Silicon. NanoBird itself adds no inference load beyond its own prompt text.

**In scope (v1):**

- Agent loop with the Mingbird mechanisms M1–M10 and the five safety rings, each switchable for ablation (§5).
- Built-in tools for files, search, shell, tests, optional web fetch, and an MCP client with scoped disclosure for servers with many tools (§6.4–§6.5).
- ACP agent over stdio, plus a headless CLI mode for benchmarks and scripts (§8).
- A model client for the NanoLLM native API with capability detection and an OpenAI-compatible fallback (§7).
- LRAB over ACP as the evaluation and regression harness (§10).
- Optional decision-model hooks over the System One protocol, off by default until measured (§5.14, §7.6).

**Out of scope (v1):**

- Inference of any kind. NanoLLM owns weights, templates, parsing and decoding; NanoBird only calls its API.
- An own GUI. Interfaces come from ACP clients; an own web UI waits for a standard ACP remote transport (§8.7).
- Parallel sub-agents and multi-agent orchestration. Mingbird ships them default-deny; NanoBird leaves them out until measured.
- Cloud model routing, accounts, telemetry, update checks.
- MoE models, matching NanoLLM scope.
- Serving decision models. NanoBird is a System One client; NanoLLM or another server hosts the model.

**Success criterion.** On LRAB (18 tasks, deterministic artifact scoring), NanoBird with E4B on NanoLLM scores at least as high as stock Mingbird with the same model on the same NanoLLM instance, same machine and budgets (§10.3). On τ²-bench it scores at least as high as Mingbird under one shared protocol.

**Name.** NanoBird credits Mingbird, whose mechanism design it ports. Mingbird is Apache-2.0; see §11.4 for attribution.

## 2. Design principles

The harness compensates for what small models cannot do reliably, and every compensation must be measured. Seven rules follow from that.

1. **Policy here, primitives in the engine.** NanoBird decides what the model sees, when it may stop, and what happens after a bad step. NanoLLM provides cache control, decoding control and parse results (see the companion RFC: NanoLLM Harness API). NanoBird never patches templates or parsers; a defect there is fixed in NanoLLM.
2. **Each mechanism answers a named failure form.** A mechanism ships only with the small-model failure it addresses, its trigger, its default and its cost (§5). This is the Mingbird inventory: prefill overflow, tool-demonstration loops, false finishes, repeated identical calls, malformed calls, destructive edits, silent stalls.
3. **Every mechanism has an off switch.** `NANOBIRD_ABLATION=<id>[,<id>]` or the config `mechanisms:` block disables any mechanism without code changes, so a leave-one-out run is a flag, not a branch.
4. **Fixed static prefill budget.** The factory static prefill (system prompt + routed tool schemas for the default category) is pinned by a unit test. Any change that grows it fails CI unless the budget constant is raised in the same commit with a recorded reason.
5. **Measured before merged.** A mechanism or default change lands only with an LRAB delta on the reference machine (§10). Deltas smaller than the same-configuration replication spread are reported as directional, never as effects.
6. **Fail loudly.** When the model cannot finish, the harness says so with the blocker named. A silent abandonment is treated as a harness defect.
7. **English only.** Every text NanoBird writes for the model or the user is English: prompts, tool schemas, gate and loop messages, compaction summaries, skills. Mingbird mixes Chinese into its prompts, interventions and heuristics; NanoBird inherits none of it, and CI enforces this (§5.13).

**Evidence and its grade.** The mechanism set and the failure taxonomy come from Wang and Huang, *Mingbird* ([arXiv 2610.02001](https://arxiv.org/abs/2610.02001), October 2026). It is a preprint on a self-built benchmark, run on one machine with single trials; its ablation is directional only. The general claim that harness choice moves agent scores most for weak models is also supported by *Harness-Bench* ([arXiv 2605.27922](https://arxiv.org/abs/2605.27922)), likewise a preprint. No peer-reviewed study or ISO standard covers agent harnesses for small models as of this date.

## 3. Architecture overview

NanoBird is one process with one loop at its center: ACP in front, the model client behind, mechanisms and tools hooked into the loop at fixed points.

> **Figure: NanoBird packages and what each calls.** Titled "Mechanisms hook into one loop between ACP and the engine". Inside the `nanobird` process: `acp` (ACP v1 agent: sessions, permissions, plan), `loop` (chat/task classifier; turn states, budgets), `mech` (M1–M10 and N1–N4 as hooks, each with an ablation switch), `session` (transcripts, checkpoints, staged compaction), `tools` and safety (built-in tools, MCP client; rings, path checks, backups) and the model client (NanoLLM native, OpenAI fallback; engine doctor probes). Dashed boxes lie outside the process: ACP clients (Zed, JetBrains, Neovim, Toad), the LRAB runner, the workspace (files on disk, `.nanobird/`), MCP servers (stdio or HTTP) and NanoLLM (Harness API or OpenAI dialect). ACP clients and the LRAB runner reach `acp` over stdio; `acp` and `loop` talk both ways; `loop` calls `mech`, `session`, `tools` and `model`; `tools` act on the workspace and the MCP servers; `model` talks to NanoLLM.

A prompt enters through `acp`, which owns sessions, plans and permission prompts. `loop` runs the turn states of §4; `tools` act on the workspace and MCP servers; `model` talks to NanoLLM; `session` records every event so any run can be audited and resumed. Dependencies point downward only: mechanisms never call ACP, tools never call the model.

**Hook points.** The loop calls mechanisms at five fixed points, and each mechanism registers at one or more of them.

- `before_request`: M1 prefill and router, M3d plan re-injection, N3 compaction.
- `after_reply`: M5 format rescue, N4 tool-name correction, M7 empty-turn handling.
- `before_call`: M4 anti-loop, M8 boundary, safety rings, N1 write guard.
- `after_call`: M6 backups, M3c verification feedback, output caps.
- `before_finish`: M3a finish gate, M3b self-check.

## 4. Agent loop

One prompt runs as a bounded loop of model turns. Each turn assembles a small prompt, gets one model reply, repairs and checks it, executes at most the calls that pass the guards, and either continues or tries to finish through the gate.

### 4.1 Request classification (M2)

Every incoming prompt is classified as `chat` or `task` before any tool is offered.

- `chat`: minimal prefill, no tool schemas, one reply, loop ends. This removes the tool-demonstration loop that small models fall into on casual input.
- `task`: the routed toolset (M1) and the full loop.
- A short follow-up in a session that has already produced artifacts is classified as `task` (continuation). On doubt, `task` wins, so a real task is never stranded without tools.
- The classifier is rules first (keywords, imperative form, attachments, session history), with an optional one-shot model call when rules disagree. Its decision is logged and surfaced to the client as the session mode (§8.4).

### 4.2 Turn states

1. **Assemble.** Static prefill for the routed category (M1), skill index, the plan as last written (M3 re-injection), history within the context budget.
2. **Generate.** One request to the model client with the turn's thinking policy (§4.5) and output cap (§6.3).
3. **Parse.** Take the engine's parsed tool calls; if the engine reports a failed or repaired parse, run format rescue (M5).
4. **Guard.** Signature anti-loop (M4), safety rings (§5.11), workspace boundary (M8). A blocked call returns a corrective message instead of a result.
5. **Execute.** Run the surviving calls sequentially, with edit safety (M6) and output caps.
6. **Observe.** Append results; on test runs, inject exact failures (verification feedback, part of M3). Go to 1.
7. **Finish attempt.** A `finish` call goes to the finish gate (M3). Accepted: the turn ends with `end_turn`. Rejected: the rejection carries the next executable action, and the loop continues.

Only one model request is in flight per session. Parallel tool calls in one reply are executed in order, stopping at the first guard rejection.

### 4.3 Stop conditions

| Condition | ACP stop reason | Default |
|---|---|---|
| Finish accepted by the gate | `end_turn` | — |
| Turn budget exhausted | `max_turn_requests` | 60 turns per prompt |
| Model output hits the cap with no recoverable call | `max_tokens` | 8192 tokens per call |
| Escalation ladder exhausted (§5.4): graceful exit with the blocker named | `end_turn` + blocker message | 3 rungs |
| Client cancelled | `cancelled` | — |
| Time-box reached (optional, off by default) | `end_turn` + partial-delivery note | user-set |

### 4.4 Context budget

- Default context: 32,768 tokens, Mingbird's default and the LRAB protocol value. Mingbird's published runs peaked exactly at that limit, with a median peak of 14,624 tokens per turn.
- At 90% of the context, compaction runs in a fixed order: drop bodies of old tool results (keep the call and a one-line digest), then old reasoning, then summarize the oldest turns with one model call.
- The task text and the current plan are never compacted. They are re-injected verbatim after every compaction (M3).
- Compaction is a measured mechanism with its own switch: a component study on large models found that aggressive context compression lowered repository-generation scores ([arXiv 2609.32459](https://arxiv.org/abs/2609.32459), preprint).

### 4.5 Thinking policy

Thinking is a harness-level lever, not a model constant. In LRAB, switching it moved rival harnesses by up to 0.69 on one domain, while Mingbird moved by 0.011 on τ²-bench totals.

- Default: thinking **off** on tool turns, matching the LRAB unified protocol.
- Option `thinking: plan`: thinking on with a budget (default 1,024 tokens) only on the first turn of a task and after a finish rejection; off elsewhere.
- When the engine supports a reasoning budget (RFC §R3), an exceeded budget closes the thought and forces an answer. Otherwise NanoBird aborts the stream at the budget and retries the turn with thinking off, keeping the partial reasoning as context (the little-coder pattern).
- The E4B default is decided by an LRAB run of all three settings (§10.4), not by assumption.

### 4.6 Sampling

Temperature 0 by default, as in the LRAB protocol, so harness comparisons are not confounded by sampling. Other sampling parameters stay at the engine's defaults for the model. A retry after a format failure keeps temperature 0; the corrective message, not randomness, changes the next reply.

## 5. Mechanisms

NanoBird ports Mingbird's ten core mechanisms and five safety rings, and adds four mechanisms from little-coder and the context-compression study. Every row has an ablation id; the evidence column says what has actually been measured.

| ID | Mechanism | Failure form it answers | Ablation id | Evidence so far |
|---|---|---|---|---|
| M1 | Flat prefill, category router, fixed budget | Tool schemas crowd out the task | `prefill` | Mingbird ablation −0.003 at 2B (inert on LRAB's narrow tool surface); prefill cost measured on iGPU |
| M2 | Chat/task layering | Tool-demonstration loops on casual input | `layering` | Not ablated |
| M3a | Finish gate (executable guards) | False or premature completion | `finish_gate` | −0.098 single trial; +0.095 mean over 3 matched replications vs text re-read alone |
| M3b | Delivery self-check (task text re-read) | Drift from the acceptance criteria | `self_check` | Present in both arms of the matched comparison; not isolated |
| M3c | Verification feedback | Model cannot localize a test failure | `verify_feedback` | −0.048 single trial, directional |
| M3d | Plan re-injection after compaction | Plan lost to a summary | `plan_reinject` | Not ablated |
| M4 | Signature anti-loop with escalation | Identical calls repeated with no output | `anti_loop` | −0.015 single trial; sign flips without one task |
| M5 | Format rescue and false-finish rejection | Tool calls emitted as text, malformed calls | `format_rescue` | Not ablated |
| M6 | Edit safety | Destructive edits the model cannot undo | `edit_safety` | Not ablated; little-coder's write guard fired on 57% of exercises |
| M7 | Resilience | Empty turns, encoding crashes, killed runs | `resilience` | Not ablated |
| M8 | Workspace boundary | Writes outside the workspace, credential reads | `boundary` | 54-case probe set: file tools held on every case |
| M9 | Progressive disclosure (skills, MCP) | Capabilities inflate the prefill | `disclosure` | Not ablated |
| N1 | Write refuses existing files | Whole-file rewrite destroys working code | `write_guard` | little-coder, observational only |
| N2 | Thinking budget with forced exit | Open-ended deliberation eats the turn | `think_budget` | little-coder, observational only |
| N3 | Staged compaction | Context overflow on long tasks | `compaction` | Compression hurt in a large-model study; must be measured here |
| N4 | Unknown-tool correction | Hallucinated tool names end the turn | `tool_name_fix` | None; new |
| N5 | Decision hooks over System One | Keyword and string-similarity heuristics misjudge intent, relevance and stagnation | decision_<hook> | None; offline decision set first (§10.7) |
| N6 | Scoped MCP: harness-built tool tree, pushed shortlist, tools navigation | Hundreds of MCP schemas crowd the prefill, confuse tool choice, and break the prefix cache when the set changes | mcp_scope | None on small models; hierarchy helped GPT-4 in AnyTool (ICML 2024); H20–H25 on the MCP scale suite (§10.9) |

All Mingbird figures come from one 2B model (gemma4:e2b), 18 tasks, single trials; the authors report same-configuration replications moving the mean by up to 0.069, which is the size of most deltas above. Treat them as priors to re-measure on E4B, not as effects.

### 5.1 M1 · Flat prefill and category router

- **Core set, always loaded:** `read_file`, `create_file`, `edit_file`, `list_dir`, `run_shell`, `todo`, `skills`, `tools`, `finish`.
- **Routed categories:** `files`, `code`, `web`, `memory`. The router picks categories from English keywords in the task text before the first turn (§5.13); `tools` lets the model ask for more later. Misrouting is recoverable by design.
- **Flat schemas:** no nested objects where a flat parameter works; MCP tool schemas are compressed (parameters past the eighth trimmed to required ones), as in Mingbird.
- **Budget:** the factory static prefill is pinned by a unit test, in bytes (tokenizer-independent) and in tokens for each reference model, counted through the engine's tokenize endpoint. Target: at or under Mingbird's 797 tokens. Growth fails CI unless the constant changes in the same commit.
- **Cost note:** Mingbird measured prefill on an iGPU at about 0.2 ms per token for a 4B model, and 33.6 s per turn at 2B with 256 tools. With a NanoLLM prefix-cache handle (RFC §R2) the static block is computed once per session; the budget still holds because long inputs degrade reasoning, not only latency.

### 5.2 M2 · Chat/task layering

Specified in §4.1. Ablating it routes every prompt as `task`.

### 5.3 M3 · Finish gate, self-check, verification feedback, plan re-injection

A `finish` call runs these checks in order; the first failure rejects the finish.

1. **No-work refusal.** No productive tool call since the prompt: rejected with a concrete first step.
2. **Plan sync.** Unchecked items in the model's own `todo` plan: rejected, listing them.
3. **Claim check.** Every path the finish summary names must exist and be non-empty.
4. **Test guard.** If the workspace has tests for a detected runner (pytest, `go test`, `npm test`, `cargo test`), run them; failures reject with file, line and the original error text (M3c).
5. **Self-check (M3b).** Once per task, re-inject the original task text and require a point-by-point check before a second `finish` is accepted.

Every rejection carries the next executable action. After 3 rejections of the same claim, the finish is accepted with an explicit **unverified** flag and the open checks listed, so the run fails loudly instead of looping. Mingbird observed a 2B model restating a refused claim after one bounce; the gate cannot force a true self-report, only expose a false one.

The `todo` interface exposes an explicit completion action (`todo(update, all=true)`), so completion is an act, not a stylistic drift. After any compaction the plan is re-injected verbatim (M3d).

### 5.4 M4 · Signature anti-loop

- **Signature:** tool name plus canonical arguments (JSON keys sorted, whitespace collapsed, paths normalized).
- **Triggers:** 6 identical signatures in a task, or 15 consecutive calls with no state change (no new or changed file, no new output). Dialogue-style tools use a tighter limit of 4, as in Mingbird's τ² adapter.
- **Ladder:** rung 1, a corrective message on each rejection, with a stronger hint from the third ("read the actual state first, or change strategy"); rung 2, after 6 consecutive empty or rejected turns, a hard reset of the turn (drop the failed turns, re-inject task and plan); rung 3, graceful exit with the blocker named (§4.3).
- Thresholds sit well above legitimate repetition such as re-running tests after edits, which changes inputs between calls.

### 5.5 M5 · Format rescue and false-finish rejection

- Runs only when the engine reports `parse.status` = `failed` or `repaired`, or returns text with no call while a call was expected (RFC §R4). Without that report, NanoBird applies the same rules to the raw text.
- **Rescued shapes:** a JSON tool object in prose, a fenced block holding one, placeholder-quoted pseudo-XML, a double-quoted pseudo-call. Anything else is rejected with a schema example; there is no speculative parsing.
- A repeated malformed call after a rescue triggers a corrective hint with a minimal valid example for that tool.
- **Strict mode** (`tool_format: strict`) instead asks the engine for a lazy tool-call grammar (RFC §R4). Rescue and strict mode are alternatives chosen per model by an LRAB run; Mingbird chose rescue for 2–9B models and did not measure strict decoding.

### 5.6 M6 · Edit safety (with N1)

- Every write first stores the previous content in `.nanobird/backups/<path>@<timestamp>`; `rollback(path)` restores the last version. Backups never land beside the user's files.
- `delete_file` moves to `.nanobird/trash/`.
- **N1:** `create_file` on an existing path returns a structured error pointing to `edit_file`. Overwriting a large file with much shorter content requires `replace=true`.
- **Convergence guard:** alternating destructive edits on the same region trigger a hint to re-read the file.
- `.nanobird/` is excluded from benchmark artifact scoring and added to the workspace's ignore files on first use.

### 5.7 M7 · Resilience

- Subprocess output is decoded as UTF-8 with replacement; on Windows the console code page is detected first. No byte pattern can stall the reader.
- An empty generation, or a reply that is only a reasoning wrapper, counts as empty: one retry with a nudge, then a hard reset of the turn.
- A checkpoint is written after every completed step (§9.3). A killed run resumes from it through ACP `session/load` or `session/resume`, or `nanobird run --resume`.
- Model server errors: 3 retries with backoff, then a clear failure naming the endpoint.

### 5.8 M8 · Workspace boundary

- Workspace roots come from the ACP session `cwd` plus any additional roots the client sends.
- Paths are normalized before every file operation: symlinks and junctions resolved, `\\?\` and `..` handled, case-insensitive comparison on Windows and macOS.
- Sensitive patterns (`.ssh`, credential stores, `.env` **outside** the workspace) need explicit permission. Mingbird's evaluation found its sensitive-path match had no workspace scoping and refused a benign in-workspace `.env` copy; NanoBird scopes the pattern.
- **Honest limit:** shell commands are checked against pattern lists, not sandboxed. In Mingbird's probe set, every confirmed leak went through the raw shell (2 of 44 cases on the 2B model, 11 of 44 on the 4B). OS-level sandboxing is an open question (§13).

### 5.9 M9 · Progressive disclosure

- **Skills:** Markdown files with frontmatter (`name`, `description`) in `<workspace>/.nanobird/skills/` and the user config directory. Only the index (name plus one line) stays in the prefill; `skills(load, name)` brings in the body. The skill format follows the Agent Skills convention so existing skill folders work unchanged.
- **MCP:** up to 12 tools in total load flat, as `server.tool`; above that, scoped disclosure applies (N6, §6.5). Neither path depends on the category router.

### 5.10 M10 · Quality base

CI carries the prefill budget assertion, the loop thresholds, the English-only lint (§5.13), and golden transcripts replayed against a scripted fake model (the NanoLLM fake backend), so mechanism behavior is tested without a live model.

### 5.11 Safety rings

| Ring | Rule | Attended | Unattended |
|---|---|---|---|
| 1 | Irreversible commands refused: `format`, `diskpart`, `mkfs`, `dd` to raw devices, `vssadmin delete shadows`, `wsl --unregister`, `dism`, driver removal, `userdel` | refused | refused |
| 2 | System mutation: package uninstall, system environment changes | ACP permission prompt | denied |
| 3 | Recursive delete only inside the workspace; refusals offer a per-file alternative | ACP permission prompt for out-of-bounds | denied |
| 4 | Reversibility: backups before writes, trash for deletes, `replace=true` for shrinking overwrites | always | always |

Ring 0 (sub-agent sandbox) is not needed in v1, which has no sub-agents. `NANOBIRD_UNSAFE=1` disables rings 1–3 at the operator's risk; ring 4 stays on.

### 5.12 N3, N4

- **N3** is the staged compaction of §4.4.
- **N4:** a call to an unknown tool name gets the closest known names and their one-line descriptions back, instead of an error that ends the turn.

### 5.13 Language: English only

NanoBird is English-only: every text the harness authors is English, and no heuristic depends on another language. The target model is Gemma 4 E4B and the reference benchmarks (LRAB, τ²-bench) are English, so one language keeps prompts, interventions and evaluation consistent.

**Why remove rather than switch.** Mingbird's own code comment says its Chinese system prompt made Qwen-family models drift into Chinese on English tasks. Its `AGENT_LANG=en` switch only appends "Always respond in English" to the two system prompts, while gate, loop and safety messages stay Chinese, and Mingbird issue #3 reports Qwen mirroring Chinese in English sessions. NanoBird removes the source instead of adding a language switch.

**Inventory of Chinese in Mingbird v3.1.4 and the NanoBird replacement**

| Mingbird element | Language there | NanoBird |
|---|---|---|
| `SYSTEM` prompt: intent-routing block and home-directory rule | Chinese inside an English frame | Rewritten in English from intent, not translated line by line; counted in the M1 budget |
| `CHAT_SYSTEM` (chat persona) | Chinese | English, at most 4 lines |
| Interventions: finish-gate rejections, anti-loop nudges, safety-gate blocks, MCP errors, argument-type errors, `enable_tools` notices | Chinese or mixed | English, from one message resource file |
| Compaction summary (at most 150 Chinese characters) | Chinese | English summary with a token budget |
| Category router: Chinese category names and keyword lists; only `bash`, `python`, `mcp`, `api` in Latin script | Chinese | Not translated: English categories and keywords; whether a keyword router exists at all is decided by its ablation (§10.4) |
| Chat/task classifier `_is_qa()`: about 30 Chinese task verbs, 什么/怎么/吗 question cues, a few English cues, a 40-character rule | Mostly Chinese | Rewritten for English, rules derived from transcripts and measured (M2) |
| Markers that tell harness-injected messages from user ones (`[已拦截`, `提示:`) | Chinese text prefixes | Structured: injected messages are tagged in the transcript, never detected by text |
| Token estimate `cjk*2 + other/3` | CJK-calibrated heuristic | Engine tokenizer (RFC R5), byte budget as fallback |
| Artifact-claim check tolerating CJK verbs glued to file names | CJK-specific | Removed |
| Time-box phrase parser | Chinese and English | English only |
| Skill library | Chinese-oriented frontmatter convention in AGENTS.md | Not imported as-is; any reused skill is rewritten in English |

**Rules**

- All harness-authored strings live in English resource files (prompts in `internal/prefill/`, messages beside the mechanism that emits them), reviewable in one place and counted in the prefill budget where they are static.
- The system prompt states once that plans, todo items, summaries and created files are written in English. User tasks in another language are passed through unchanged; NanoBird does not translate them.
- **CI lint `lang-check`:** fails if any Go string literal, prompt or message resource, skill file or golden transcript authored by NanoBird contains a letter outside the Latin script (Unicode letter categories only; punctuation and symbols such as `—`, `≥`, `τ²` are allowed). Fixtures that need other scripts are listed explicitly.
- **Drift metric:** the transcript monitor counts non-Latin letters in model output and emits a `language_drift` mechanism event (§8.8). It is reported in `_nanobird/stats` and LRAB results, never corrected silently.
- **Third-party text:** MCP tool descriptions come from external servers and are passed through, but loading a server whose descriptions contain non-Latin letters logs a warning and sends a client notice.
- **Porting:** Mingbird's logic is ported; its text is rewritten in English. Rewritten Mingbird text is still attributed in `NOTICE` (§11.4).

### 5.14 N5 · Decision hooks over System One

NanoBird can ask a decision model typed questions at fixed points of the loop, over the System One protocol (§7.6). Every hook is off by default and is enabled only after it beats its heuristic on the offline decision set and does not lose on LRAB (§10.7).

**Contract.** A decision model receives a state and typed questions and returns probabilities, not text; it never calls tools and never speaks as the agent. A hook can only add friction (a rejection, a nudge, a permission prompt, a dropped context item); it never overrides an executable check or a safety ring. If the decision backend is absent, slow or failing, the hook falls back to its heuristic and logs the fallback.

| Hook | Mechanism | Fires | Questions | Effect | Fallback |
|---|---|---|---|---|---|
| `intent` | M2 | Each prompt, before the loop | `choice`: chat, task, continuation | Sets the loop mode | English rules of §4.1 |
| `skills` | M9 | First turn of a task | one `noul` per candidate skill (at most 26) | Loads skills above threshold; tool block unchanged | Skill index only |
| `categories` | M1 | First turn, only if the router survives its ablation | one `noul` per category | Sets routed categories once per task | Default set |
| `scope` | N6 | Scoped mode only: first turn of a task and each change of the active `todo` item | one `noul` per BM25 candidate tool card (at most 64) | Reorders the push shortlist; pushes nothing when no candidate passes the threshold | BM25 order |
| `stuck` | M4 | After 3 consecutive turns without a state change | `score`: progressing, unclear, repeating | Moves the anti-loop ladder up one rung | Signature counters only |
| `finish_check` | M3 | After every executable finish check has passed | `noul`: the summary claims something the evidence does not show | One extra rejection naming the doubt, at most once per task | Skipped |
| `keep` | N3 | At compaction level 1 | one `noul` per old tool result (at most 64) | Drops low-scoring results first | Oldest-first truncation |
| `risk` | Rings 2–3 | Shell commands not refused by ring 1 | `choice`: read-only, workspace write, system change, destructive | System change or destructive → ACP permission prompt | Pattern lists only |

**Rules**

- One request per hook firing: all questions of a hook travel together, so the state is read once.
- English only (§5.13): states, instructions and criteria are English.
- Each hook has a deterministic state builder capped to the backend limits of §7.6; overflow is cut by fixed rules, never by a model.
- Thresholds are set per hook on the offline decision set. Raw `confidence` is not treated as calibrated; StartLux states its confidence can overstate accuracy.
- Budget: at most 12 decision requests per prompt, 2 s timeout each; past either, the heuristic decides.
- Every decision goes to the transcript with question ids, probabilities, the pinned model id from the response, latency, and whether it changed behaviour; tool-call updates carry `_meta.nanobird` `{"mechanism":"decision","hook":"stuck"}` (§8.8).
- `finish_check` and `risk` are one-sided by design: they can delay a finish or ask the user, never accept a finish or permit a command.
- `scope` is the one hook that is not one-sided: it changes which tools the model sees. It stays recoverable because `tools(find)` is always present, it never pushes a tool that asks on every call (§6.4), and its online gate is the MCP scale suite (§10.9) instead of LRAB.

**Why default-off.** Mingbird's corresponding heuristics are keyword and string-similarity rules (router keywords, `_is_qa()`, a 0.75 difflib cut-off), and its router was neutral in ablation (−0.003). A learned decision model may judge these better and acts as an external judge rather than self-reflection, which hurt 8B models in Liu's factorial study (arXiv 2605.05716, preprint). No study yet measures decision models inside agent harnesses for small models, so every hook must earn its place.

## 6. Tools

Nine core tools are always present; the rest load by category (M1) or through `tools`, which finds, browses and loads tools (§6.5). `tools` replaces Mingbird's `enable_tools`, which stays as an alias. Names and parameters otherwise stay close to Mingbird's so its prompts and transcripts remain a usable reference.

### 6.1 Built-in tools

| Tool | Loaded | Parameters | Behavior and limits |
|---|---|---|---|
| `read_file` | core | `path`, `offset?`, `limit?` | Line-numbered output; crawl guard (§6.3) |
| `list_dir` | core | `path`, `depth?` (default 1) | Ignores `.git`, `.nanobird`, ignore-file entries |
| `create_file` | core | `path`, `content` | Refuses existing paths (N1) |
| `edit_file` | core | `path`, `old`, `new`, `replace_all?` | Exact-match replace; zero or several matches return the count and nearby lines |
| `run_shell` | core | `command`, `timeout_s?` (default 120) | Runs in the workspace; safety rings; output capped (§6.3) |
| `todo` | core | `action` (`set`, `update`, `show`), `items?`, `all?` | The plan the gate checks (M3) |
| `skills` | core | `action` (`list`, `load`), `name?` | Progressive disclosure (M9) |
| `tools` | core | `action` (`find`, `list`, `enable`), `query?`, `path?`, `names?` | Finds, browses and loads tools, built-in categories included (§6.5); `enable_tools(categories)` is an alias |
| `finish` | core | `summary` | Goes through the finish gate (M3) |
| `append_file` | files | `path`, `content` | For chunked writes of large files |
| `delete_file` | files | `path` | Moves to `.nanobird/trash/` |
| `rollback` | files | `path` | Restores the last backup (M6) |
| `search_files` | code | `pattern`, `path?`, `glob?` | Built-in regex search, ignore-file aware, 200 matches max |
| `run_tests` | code | `path?` | Detects the runner; same runner the gate uses |
| `web_fetch` | web | `url` | Off in offline mode |
| `web_search` | web | `query` | Off in offline mode; backend configurable |
| `memory_store`, `memory_recall` | memory | `text` / `query` | Per-workspace notes in `.nanobird/memory.md` |
| `server.tool` | mcp | per MCP schema | Flat up to `mcp.flat_limit` tools in total; above it, scoped (§6.5) |

`run_tests` is new relative to Mingbird. It gives a small model one obvious way to verify, and it is measured like any other mechanism (§10.4).

### 6.2 Shell per platform

The prefill names the shell in one line (`Shell: PowerShell 7` on Windows, `Shell: bash` elsewhere), and `run_shell` uses it. Mingbird's LRAB data shows why: one baseline harness lost 14 of 18 cells at the 35B tier by sending Unix commands to a Windows shell, a platform failure that looked like a model failure.

### 6.3 Output and read limits

- **Model output cap:** 8,192 tokens per call (Mingbird raised it from 2,048 in v1.5.0). When a reply is truncated mid-file, the next turn gets "write the skeleton, then add the rest with `append_file`" instead of an empty-turn spiral.
- **Tool result cap:** 16 KB per result sent to the model, as the first 12 KB plus the last 4 KB with a byte-count note. The full output is kept in the transcript and in the ACP tool-call content for the user.
- **Crawl guard on `read_file`:** a per-file read budget of twice the file size, clamped to 64–512 KB per task. Past the budget it returns the first 2,000 characters of the requested range with the position kept, so reading can continue; it never refuses (Mingbird crawl guard v2).

The 16 KB and 120 s defaults are starting values to tune on LRAB, not measured choices.

### 6.4 MCP client

MCP servers come from `mcp.json` (stdio and streamable HTTP) or from the client in ACP `session/new`; both take the same path. Tool lists are probed at startup, cached, and re-read on `notifications/tools/list_changed`. Up to `mcp.flat_limit` tools in total (default 12) load flat, as `server.tool`; above it, every MCP tool goes through scoped disclosure (§6.5).

- **Workspace root.** A server that also exposes file tools must have the workspace as its root, or it is not loaded, because duplicate file tools with another root write to the wrong place.
- **No duplicates.** An MCP tool that duplicates a built-in (file read or write, shell) is hidden; the built-in wins.
- **Optional curation.** Per server in `mcp.json`: `allow` and `deny` name lists, `pin` (always loaded), `rename`, `describe` and group overrides, `trusted`, `auto_approve`. Nothing is required; defaults come from the index (§6.5).
- **Results are data.** MCP output goes through the 16 KB cap (§6.3) and enters the context as a tool result. Text in it is never read as instructions, and a result cannot load tools or change the scope.
- **Timeouts.** Each call has `mcp.call_timeout_s` (default 60), as the MCP spec recommends. A server that times out twice in one task leaves the active set for the rest of that task.
- **Names.** Tools are exposed as `server.tool`, or `server__tool` on backends that reject dots in function names.

**Permissions from annotations.** The MCP spec says clients MUST treat tool annotations as untrusted unless the server is trusted, so annotations from an untrusted server never remove a prompt. Servers are untrusted unless `mcp.json` marks them `trusted`.

| Annotation | Trusted server | Untrusted server (default) |
|---|---|---|
| `readOnlyHint: true` | no prompt | ask once per tool, `allow_always` offered (§8.5) |
| `destructiveHint: false` | ask once per tool | ask on every call |
| `destructiveHint: true`, or no annotations | ask on every call | ask on every call |

Unattended, every ask becomes a denial unless the tool is listed in `auto_approve`. Tools with `openWorldHint: true` are off in offline mode, like `web_fetch`.

### 6.5 Scoped MCP (N6)

Above the flat limit, no MCP schema sits in the static prefill. NanoBird indexes the tools into a shallow tree, pushes a small shortlist for each subtask, and lets the model search or browse the rest with `tools`. The user ranks nothing. N6 extends M9 and does not depend on the category router, so it holds whatever H7 decides. Switch: `mcp_scope`; with it off, all MCP tools load flat (the control arm of §10.9).

> **Figure: Scoped MCP · prompt layout, push and pull.** Titled "Tools load at the tail, so the cached prefix survives every load": schemas in the request's tool list would sit at the start and drop the cache on each load. The cached prefix is the static prefill (core tools including `tools`, one line per MCP server, fixed for the session) and the history (earlier turns and results, growing by appending). The tail holds the schemas loaded this turn, at most 12 with pins included. The harness pushes up to 6 tools on each `todo` item, ranked by BM25 over an index of server → group → tool with cards and aliases only, optionally reranked. The model pulls more with `tools` (`find`, `list`, `enable`); `find` loads the top 3. Push and pull both append schemas to the tail. An optional decision model (the `scope` hook over System One, or E4B readout, RFC R7) reranks the push; it is drawn dashed.

The prefill and history stay byte-identical while tools come and go; only the tail is new, and a schema loaded there becomes history on the next turn.

**Evidence.** All of it comes from large models; none of it was run at E4B's size, so every default below is swept (H20–H25).

- AnyTool (ICML 2024, peer-reviewed): over 16,000 APIs, removing the hierarchy dropped GPT-4 from 58.9% to 22.4% on G2-I. Each query cost about 43 GPT-4 calls and 135K tokens, so navigation turns are expensive.
- MCP-Zero (preprint): the model states a `{server, tool}` need and the harness retrieves in two levels, with up to 98% fewer tool tokens. SING (preprint) adds an intent graph and gains 10 points over MCP-Zero on MCP-Universe.
- Anthropic Tool Search (vendor evaluation): Opus 4 rose from 49% to 74% on MCP tasks with large tool libraries; the 3–5 most used tools stay loaded.
- ToolRet (Findings of ACL 2025, peer-reviewed): retrievers that score well on standard IR benchmarks do poorly on tool retrieval, which lowers task pass rates. A plain retriever is the weak link, hence the optional reranker.

**Index, built when a server loads**

- Tree: server → group → tool, depth at most 2, at most 12 children per node. A server with 12 tools or fewer has no group level. The top level also holds the built-in categories (files, code, web, memory).
- Groups come, in this order, from shared name prefixes (`issue_*`, `pull_request_*`), then annotations (read-only or write) as a second split, then clustering of descriptions. E4B writes a one-line English label per group, once, offline.
- Cards carry no schemas. A group card is its path, label, tool count and up to 5 tool names. A tool card is its name and the first sentence of its description, at most 120 characters.
- Aliases: names E4B tends to guess for a tool, found by sampling E4B as PA-Tool does (preprint: up to +17 points on 3–8B models). A call to an alias resolves to the tool (N4).
- The index is one JSON file per server in the user cache directory, keyed by a hash of the `tools/list` result and rebuilt when the hash changes. Overrides live in `mcp.json`, not in the index.

**Run time**

1. **Static prefill:** the core tools including `tools`, and one line per MCP server (name, label, tool count). It does not change during a session.
2. **Push:** on the first turn of a task and whenever the active `todo` item changes, NanoBird ranks tools against the task text and that item and loads up to `push_size` schemas (default 6). Ranking is BM25 over names, cards, group labels and aliases, optionally reranked by the `scope` decision hook (§5.14) or by E4B readout (RFC R7), plus a small boost for tools that succeeded earlier in this workspace. Tools that would ask on every call (§6.4) are never pushed.
3. **Pull:** the model calls `tools`. `find` takes free text, returns up to 8 tool cards and loads the top 3. `list` shows a node's children; with no path, the top level. `enable` loads named tools or a whole group of at most 12.

```text
tools(action="find", query="close the duplicate github issue")
loaded:  github.update_issue, github.add_issue_comment, github.get_issue
more:    github.list_issues — List issues in a repository
         github.update_pull_request — Update a pull request
browse:  github/issues (14), github/pull_requests (11)
```

**Active set.** At most `active_cap` MCP schemas are callable at once (default 12, pins included). Loading past the cap unloads the least recently used tool not called during the current `todo` item. The model sees one line per change: `loaded: … · unloaded: …`.

**Prompt placement.** Schemas loaded mid-task are written as a message at the tail, never into the request's tool list. The chat template renders that list at the start of the prompt, so changing it would drop the whole prefix cache on every scope change (H8, H21). The parser, and the lazy grammar where available (RFC R4), get the active set with each request; grammar acts at sampling time and does not touch the cache. Unloaded schemas stay in the text until compaction level 1 drops them, and calls to them get the N4 correction. If E4B calls tail-declared tools poorly, the fallback is a proxy tool `mcp_call(tool, args)` that NanoBird validates against the schema (H22).

**Turn cost.** Navigation calls count toward the turn budget. M4 treats a repeated `find` with the same query as a repeat. After 3 `tools` calls in a row with no other tool call, the loop nudges the model to act with what is loaded. When nothing fits, the model says so; NanoBird never invents a tool.

N6 needs nothing from NanoLLM; only the readout reranker needs RFC R7. Keys are in §9.1.

## 7. Model client

NanoBird talks to one model endpoint through a small interface, detects what the engine can do at startup, and turns each missing feature into a documented fallback. NanoLLM is the reference engine; the full feature set needs the Harness API RFC.

### 7.1 Interface

```go
type Client interface {
    Capabilities(ctx context.Context) (Caps, error)
    Complete(ctx context.Context, req Request) (Stream, error) // one model turn, streamed
    Tokenize(ctx context.Context, text string) (int, error)
    Prefix(ctx context.Context, msgs []Message, tools []Tool) (PrefixHandle, error)
    Release(ctx context.Context, h PrefixHandle) error
}
```

`Request` carries messages, routed tools, an optional prefix handle, the thinking policy, the tool format (`rescue` or `strict`), an optional assistant prefix, and the output cap. `Stream` yields text, reasoning and tool-call deltas, then a final record with usage, the finish reason and the parse report.

### 7.2 NanoLLM native backend

| Feature NanoBird uses | RFC section | Fallback when missing |
|---|---|---|
| Capabilities: context size, thinking support, tool format, features | §R1 | Probe once and cache the result per model |
| Prefix cache handle for the static prefill | §R2 | Rely on the engine's implicit prompt cache |
| Reasoning budget with forced answer | §R3 | Abort the stream at the budget, retry with thinking off (§4.5) |
| Parse report: status, raw text, error | §R4 | Run M5 on raw text, if the engine returns it |
| Lazy tool-call grammar | §R4 | Strict mode unavailable; rescue only |
| Assistant prefix continuation | §R5 | Chunked writes through `append_file` only |
| Tokenize | §R5 | Byte budgets with a fixed bytes-per-token ratio per model |
| Cancellation that keeps partial output | §R5 | Discard the partial turn |
| Session state snapshot | §R6 (later) | Transcript replay on resume |

### 7.3 OpenAI-compatible fallback

Any engine exposing `/v1/chat/completions` with tools works: NanoLLM's OpenAI dialect, llama-server, vLLM, Ollama's `/v1`. Expect weaker results where the engine drops malformed calls without returning the raw text, since M5 then has nothing to repair. This backend exists so the same harness can be measured on other engines (§10.2), not as a primary path.

### 7.4 Engine doctor

`nanobird doctor` runs fixed probes against the configured engine before a benchmark and on demand. A failed probe is reported by name, never averaged into a score.

1. **Generation prompt after a tool result, thinking off.** The reply must be non-empty. A vLLM Gemma 4 template ended the prompt on a closed turn in exactly this case, so the model answered with nothing, and that pair of arms moved from 0.5856 to 0.6863 once fixed.
2. **Tool-call argument round trip.** A call whose argument holds `jq '.items[] | {id, name}'` must arrive intact. The same vLLM parser cut undelimited arguments at the first `}`, `,` or `]`.
3. **Empty-completion rate.** 20 fixed tool-turn prompts at temperature 0; any empty completion fails the probe.
4. **Context size.** The reported context matches what a long probe actually accepts.

### 7.5 Connection

Default endpoint `http://127.0.0.1:8080`, NanoLLM's default listen address. The API key comes from `NANOBIRD_MODEL_KEY` or the config file, never from the ACP client. Timeouts: 10 s to connect, none on a streaming response while tokens arrive, 120 s of silence before the turn is treated as stalled (M7).

### 7.6 Decision client (System One)

NanoBird speaks System One as a client: `POST {endpoint}/v1/systemone`, one request per hook firing. The protocol comes from TypeSafe AI (its Jev model, released 15 September 2026) and is implemented by OpenRouter, LLM Gateway, Ollama 0.35 and later for local decision models, and the Pydantic AI and LangChain clients; StartLux decision models declare compatibility with it.

**Request.** `model`; `state` as a string, object or array; `questions` as a map from question id to `{type, instructions, criteria}`. Types used: `noul` (criteria with `true` and `false` keys), `choice` (criteria map from option to guidance), `score` (ordered criteria array). `box` is not used.

**Response.** `answers` keyed by question id: `noul` → probability of true; `choice` → `choice`, `confidence`, `probabilities`; `score` → `score`, `confidence`, `probabilities`, `legend`. Plus `model` (the pinned, dated id that answered) and `usage`.

```json
{
  "model": "startlux-decision-0.8b-instant",
  "state": { "last_turns": "...", "files_changed": 0, "same_signature_calls": 4 },
  "questions": {
    "stuck": {
      "type": "score",
      "instructions": "Is the agent making progress on its current plan item?",
      "criteria": ["Progressing", "Unclear", "Repeating the same approach without progress"]
    }
  }
}
```

- **Extensions:** `none_probability` (StartLux) is read when present and treated as an alarm, not a probability. NanoBird never sends `think_mode`: decisions must be single-pass, and Instant models reject other modes with `422`.
- **Limits:** a profile per backend with maximum options, score levels, questions and request bytes. Defaults follow Pydantic AI's profile for Ollama: 26 options, 26 levels, 64 questions, 64 KiB. NanoLLM advertises its own limits through RFC R1.
- **Backends (configured, none bundled):** NanoLLM with RFC R7, the preferred path, offering native decision models and a readout mode over the loaded chat model; Ollama 0.35+ on loopback; the StartLux reference server, with user-supplied weights under CC BY-NC 4.0; remote System One endpoints such as Jev via OpenRouter, only when configured explicitly, refused in offline mode and flagged in reports because decisions then leave the machine.
- **Not in v1:** OpenAI's `/v1/decisions`, a different schema (`input` plus an array of named questions with `predicate`, `choice`, `score`). LLM Gateway already lists `/v1/systemone` as legacy for non-TypeSafe models, so an adapter is tracked in §13.
- **Doctor:** `nanobird doctor` sends one `noul`, one `choice` and one `score` probe with known answers, checks the response shape, the pinned model id and latency, and compares probabilities with a golden file when `decision.golden` is set. That is the only practical guard against a server that silently reads a bidirectional decision model's prompt causally.

```yaml
decision:
  enabled: false
  endpoint: http://127.0.0.1:8080   # NanoLLM (R7), Ollama 0.35+, or a StartLux server
  model: ""
  key_env: NANOBIRD_DECISION_KEY
  timeout_ms: 2000
  max_calls_per_prompt: 12
  hooks: []                          # intent, skills, categories, scope, stuck, finish_check, keep, risk
  thresholds: {}                     # per hook, from the offline decision set
  golden: ""                         # optional probe file with reference probabilities
```

## 8. ACP interface

NanoBird is an ACP agent: `nanobird acp` speaks ACP v1 over stdio, and every client that launches ACP agents can drive it. The protocol is JSON-RPC 2.0; all paths are absolute and lines 1-based, as the spec requires.

### 8.1 Target and SDK

- **Protocol:** ACP v1 (stable). ACP v2 is published as a draft; NanoBird tracks it and migrates when it stabilizes.
- **SDK:** [coder/acp-go-sdk](https://github.com/coder/acp-go-sdk) (Apache-2.0, v0.13.5 at writing). The agent implements `acp.Agent` plus `acp.AgentLoader` for `session/load`, over `acp.NewAgentSideConnection(agent, os.Stdout, os.Stdin)`.
- **Conformance:** the ACP Test Compatibility Kit runs in CI against `nanobird acp` with the fake model backend.

### 8.2 Methods

| Method | Supported | Notes |
|---|---|---|
| `initialize` | yes | Advertises `loadSession`, prompt capabilities (text, resources, images when the model supports them), session config options |
| `authenticate` | no methods advertised | Local tool; the model key lives in NanoBird's config |
| `session/new` | yes | `cwd` becomes the workspace root (M8); MCP servers passed by the client load as §6.4 |
| `session/load`, `session/resume` | yes | Replays history from the transcript, then the checkpoint (§9.3) |
| `session/list`, `session/close`, `session/delete` | yes | Sessions are per workspace |
| `session/prompt` | yes | One prompt runs the loop of §4; stop reasons as §4.3 |
| `session/cancel` | yes | Aborts the model stream and running tools; replies `cancelled`, never an error |
| `session/set_mode` | yes | Modes in §8.4 |

### 8.3 Session updates

- `agent_message_chunk` streams the model's text; `agent_thought_chunk` streams reasoning only when thinking is on.
- `tool_call` / `tool_call_update` report every call with `name`, `kind`, `locations` and `status` (`pending`, `in_progress`, `completed`, `failed`). Edits carry `diff` content (`path`, `oldText`, `newText`); results carry text content, capped as §6.3 for the model but sent whole to the client.
- A call blocked by a guard is reported as `failed` with the corrective message as content, so the user sees why the model changed course.
- `plan` mirrors the model's `todo` list on every change, with entry `status` and `priority`.
- `usage_update` sends `used` and `size` in tokens from the engine's usage; no cost field for a local model.
- `compaction_update` reports each compaction when the client advertises `session.compaction`; otherwise a short agent message says what was compacted.
- `notice` (when the client advertises `session.notices`) carries doctor warnings, unavailable MCP servers and similar; nothing required for correctness travels only as a notice.
- `current_mode_update` reports the chat/task decision of §4.1; `session_info_update` sets a session title from the first task.

Tool kinds: `read_file`, `list_dir` → `read` · `search_files` → `search` · `create_file`, `edit_file`, `append_file`, `rollback` → `edit` · `delete_file` → `delete` · `run_shell`, `run_tests` → `execute` · `web_fetch`, `web_search` → `fetch` · `todo` → `think` · everything else → `other`.

### 8.4 Modes and config options

- **Modes:** `auto` (default, classifier of §4.1), `chat`, `task`.
- **Config options:** `thinking` (`off`, `plan`, `on`), `tool_format` (`rescue`, `strict`, shown only when the engine supports a lazy grammar), `offline` (boolean; web tools and URL-based MCP servers are not even assembled into the prompt), `model` (category `model_config`, from the engine's model list).

### 8.5 Permissions

Safety rings 2 and 3 (§5.11) call `session/request_permission` with the pending tool call and options `allow_once` and `reject_once`; ring 2 adds `allow_always`, remembered per command family for the session only. Ring 1 never asks: it refuses. A `cancelled` outcome counts as a refusal. A client may auto-answer permissions by its own settings, which NanoBird cannot see, so ring 4 (backups, trash) stays on regardless.

### 8.6 Client file system and terminals

NanoBird executes its own tools by default, so backups, rings and benchmark runs behave the same in every client.

- `client_fs: read` (opt-in): when the client advertises `fs.readTextFile`, `read_file` reads through it, so the model sees unsaved editor buffers. Writes stay local, because backups need the file on disk.
- `client_terminal: on` (opt-in): when the client advertises `terminal`, `run_shell` runs through `terminal/create` and embeds the live terminal in the tool call. Rings are checked before the command is sent.
- Benchmark runs force both off.

### 8.7 Transports

- **v1:** stdio only.
- **Remote:** ACP has no stable remote transport yet. An RFD proposes Streamable HTTP with a WebSocket upgrade on one `/acp` endpoint, and a Transports Working Group was formed to stabilize it. The Rust SDK already ships an HTTP + SSE transport with optional WebSocket. NanoBird adds `nanobird acp --listen` once the Go SDK or the spec settles; until then a web UI uses a small WebSocket-to-stdio bridge.

### 8.8 Extensions

- Each `tool_call_update` produced by a mechanism carries `_meta.nanobird` with the mechanism id and rung, for example `{"mechanism":"anti_loop","rung":1}`. The LRAB runner counts mechanism firings from the protocol stream, with no log parsing.
- `_nanobird/stats` returns per-session counters: turns, tool calls, mechanism firings, tokens, wall time.
- `_nanobird/ablation` sets the ablation list for a new session (benchmark use; refused once a prompt has run).

## 9. Configuration and state

One YAML file configures NanoBird, every key has a `NANOBIRD_*` environment override, and all session state lives inside the workspace under `.nanobird/`.

### 9.1 Config file

Loaded in order, later wins: built-in defaults, the user file (`~/.config/nanobird/nanobird.yaml`, `%APPDATA%\NanoBird\nanobird.yaml`, `~/Library/Application Support/NanoBird/nanobird.yaml`), the workspace file `.nanobird/config.yaml`, environment variables, ACP session config options.

```yaml
model:
  backend: nanollm          # nanollm | openai
  endpoint: http://127.0.0.1:8080
  name: gemma-4-e4b
  key_env: NANOBIRD_MODEL_KEY
loop:
  max_turns: 60
  output_cap_tokens: 8192
  context_tokens: 0         # 0 = take from engine capabilities, else 32768
  compaction_at: 0.9
  temperature: 0
  thinking: off             # off | plan | on
  thinking_budget: 1024
  tool_format: rescue       # rescue | strict
mechanisms:
  disable: []               # ablation ids from §5
tools:
  shell_timeout_s: 120
  result_cap_bytes: 16384
  web: false
  client_fs: off            # off | read
  client_terminal: off
safety:
  unattended: deny          # deny | ask
mcp:
  config: mcp.json          # servers, curation, trust (§6.4)
  flat_limit: 12            # total MCP tools that load flat; above it, §6.5
  active_cap: 12            # callable MCP schemas at once, pins included
  push: true                # harness-side shortlist per todo item
  push_size: 6
  rerank: none              # none | decision | readout
  call_form: native         # native | proxy (H22)
  call_timeout_s: 60
skills:
  dirs: []
offline: false
```

Unknown keys are an error, as in NanoLLM's config loader.

### 9.2 Sessions and transcripts

- `.nanobird/sessions/<id>/transcript.jsonl`: append-only, one record per event: the exact request sent to the model (or its prefix handle and the delta), the reply, the parse report, every tool call and result, every mechanism event with its id and rung.
- The transcript is the audit trail for benchmarks: every LRAB number must be recomputable from it.
- Sessions never leave the machine. There is no telemetry, crash reporting or update check.

### 9.3 Checkpoints

After each completed step, `checkpoint.json` records the turn index, the plan, ladder counters, read budgets and the transcript offset. Resume reloads it, replays the transcript from that offset into context, and continues. A run killed mid-tool repeats that tool call only if it is read-only; a write is checked against its backup first.

### 9.4 Logging

In ACP mode stdout belongs to the protocol, so logs go to stderr and to `.nanobird/logs/` as structured JSON (`log/slog`). `--log-level` sets the level; prompts appear in logs only at `debug`.

## 10. Evaluation

Every mechanism and default is justified by LRAB runs driven over ACP, replicated three times, with a frozen held-out task set at stage gates. The runner is an ACP client, so the same code measures NanoBird and any other ACP agent under one protocol.

### 10.1 LRAB over ACP

- `tools/lrab` is a Go ACP client. Per cell it copies fresh fixtures into a new workdir, spawns the agent command, sends the task text as one `session/prompt` with that workdir as `cwd`, and waits for the stop reason or the budget.
- **Budgets:** 90 minutes per workflow cell, 180 per long-horizon cell, as in LRAB. A timeout scores 0.
- **Scoring:** LRAB's published deterministic artifact scorer, unchanged (code Apache-2.0, data CC BY 4.0). `.nanobird/` is excluded from artifact checks.
- **Telemetry for analysis:** mechanism firings come from `_meta.nanobird` on tool-call updates (§8.8), counts from `_nanobird/stats`.
- Mingbird compared harnesses through per-CLI adapters. A single ACP runner removes that source of difference, though only for agents that speak ACP.

### 10.2 Arms

| Arm | Harness | Engine path | Purpose |
|---|---|---|---|
| A0 | Stock Mingbird v3.1.4 (commit 7a3bc35) | NanoLLM, Ollama dialect | Baseline for the success criterion |
| A1 | NanoBird, defaults | NanoLLM native | Main result |
| A2 | NanoBird, defaults | NanoLLM OpenAI dialect | Isolates the Harness API primitives (A1 − A2) |
| A3 | OpenCode over ACP | NanoLLM OpenAI dialect | Product-harness reference under the same runner |
| F0 / F1 | Mingbird / NanoBird | Ollama, `gemma4:e2b` and `qwen3.5:4b` | Port fidelity on models with published LRAB cells |

The fidelity arms run before anything else. Mingbird's own reruns of its full arm landed 0.081 below its published 2B mean, so NanoBird is compared to a fresh Mingbird run on the same machine (F0), not to the paper.

### 10.3 Protocol

- Temperature 0; thinking as configured per arm and recorded; one reference machine per campaign, named with its hardware profile; model weights pinned by digest; engine version pinned.
- **Three replications** per cell for A0, A1, A2 and all ablations. Mingbird reported same-configuration replications moving the 18-task mean by up to 0.069 with single trials; three runs give NanoBird its own spread estimate.
- **Statistics:** the 18 tasks are the unit; paired Wilcoxon signed-rank tests with Holm correction and task-level bootstrap 95% intervals, as in the Mingbird paper. Deltas inside the replication spread are reported as directional.
- Every cell, transcript and score is published with the run, so any aggregate can be recomputed.

### 10.4 Ablations and knobs

On E4B, leave-one-out for every ablation id in §5, three replications each, plus these knob sweeps: `thinking` off / plan / on; `tool_format` rescue / strict; `run_tests` present / absent; compaction on / off. Each sweep decides a default in §9.1 and is recorded beside it.

### 10.5 Held-out tasks

LRAB has no held-out split, and Mingbird's finish gate and anti-loop were tuned with LRAB in the loop. NanoBird adds 12 new tasks in LRAB's four tiers, written and frozen before any tuning, scored by the same rubric style. They run only at stage gates (§12), never during development.

### 10.6 External check

τ²-bench, all three domains, under the unified protocol: the same user simulator and judge for every arm. For a zero-cloud setup the simulator can be a local model, which makes absolute scores incomparable to the leaderboard but keeps arms comparable. The chosen simulator is named in every report.

### 10.7 Decision models

Decision hooks are judged in two steps: offline on labelled decisions, then online on LRAB. The offline step is cheap and needs neither NanoBird nor NanoLLM changes.

1. **Offline decision set** (`tools/decisions-eval`): 200–500 hand-labelled items per hook, built from transcripts of LRAB development tasks with each hook's own state builder. Held-out tasks (§10.5) are never used.
2. **Arms:** the heuristic fallback; NanoLLM readout mode over E4B (RFC R7); one or more dedicated decision models, for example StartLux Decision 0.8B and 2B Instant, or models served by Ollama.
3. **Metrics:** accuracy or F1 per hook, calibration error, latency and resident memory on the reference machine.
4. **Offline gate:** a hook passes when its best arm beats the heuristic by a margin fixed before the run; thresholds are frozen on this set.
5. **Online gate:** LRAB leave-one-out per enabled hook (`decision_<hook>`), three replications, non-inferiority against hooks off, plus decision calls and milliseconds per prompt in the resource report.

### 10.8 Hypothesis register: improvements over Mingbird

Twenty-five improvements over Mingbird are adopted as hypotheses, not features: each comes from another small-model harness or a study, names the Mingbird v3.1.4 behaviour it changes, and carries a prediction and a test. A hypothesis becomes a default only if its target metric improves and LRAB is non-inferior to the arm without it (§10.3), confirmed once on the held-out set (§10.5).

**Evidence grade.** Only five sources here are peer-reviewed: Forge as an ACM CAIS '26 demo, the NeurIPS 2026 SLM-Agents workshop posters ("The Parser Is Part of the Agent", "Ordering or Pruning"), AnyTool (ICML 2024) and ToolRet (Findings of ACL 2025). Everything else is a preprint, a blog post or a README claim, labelled in the table. No ISO/IEC standard applies. The comparison with other harnesses is in the report "Mingbird и агенты для малых LLM".

| ID | Hypothesis and source | Mingbird v3.1.4 today | Prediction and test |
|---|---|---|---|
| H1 | `finish` takes a required `summary` argument. *Source:* "The Parser Is Part of the Agent" (peer-reviewed workshop poster): violations 93% → 0%, strict success 7/40 → 29/40 on Qwen2.5-1.5B | `summary` optional; fake-finish check looks for empty content and summary | Fewer fake-finish rejections per cell, higher completion. Arm `finish_arg` on/off |
| H2 | Before finishing, the model sees its own diff once when 8+ lines changed. *Source:* Local Quench (README, no results) | Self-check re-reads the task text only | Fewer incomplete deliveries on edit tasks. Arms: task re-read, diff, both |
| H3 | Verification beyond pytest: a verify command per task or per detected runner. *Source:* Local Quench verify loop (README); CMU harness optimiser added pytest-required hooks itself (preprint) | Test guard runs only for `test_*.py` (pytest, then unittest) | More true gate catches on non-Python tasks. Arm `verify_cmd` on/off |
| H4 | Extra loop detectors: A-B cycles, polling, different commands with the same output. *Source:* Local Quench (README); loops are the dominant failure in Li et al. (preprint) | Exact signature, same-tool streaks 5/8, text similarity 0.75 | Fewer turn-budget exhaustions and wasted turns. Arm `loop_detectors_ext` |
| H5 | Hard timeout on every command plus detection of foreground servers, moved to background. *Source:* Li et al.: 34 pi runs hung without a timeout (preprint); LRAB: opencode blocked on a foreground mock server | No general per-command policy documented beyond the 300 s test guard | Zero hung cells. Count hangs with and without |
| H6 | Trust decay: a tool is demoted after 3 consecutive failures and removed after 5. *Source:* SmallCode (README) | Only `edit_file` is disabled after 3 failures; loop rules remove tools | Less thrashing, at the risk of removing a needed tool. Arm `trust_decay` |
| H7 | Fixed core toolset plus `tools` instead of a keyword router. *Source:* pi (<1K prefix), Leaf (5 typed tools, preprint); Mingbird's own router ablation −0.003 and +330 tokens | Keyword router with Chinese keywords; English tasks fall to the default set | Non-inferior score with a smaller, stable prefix. Router on/off (§5.1) |
| H8 | Everything dynamic goes at the tail of the prompt, never into the system block. *Source:* little-coder: 61,910 → 25,579 prompt tokens over 4 turns (CHANGELOG); "Ordering or Pruning": a stable tool block is 11–75× cheaper on vLLM (peer-reviewed poster) | Workdir, platform and language lines are appended to the system prompt per task | Higher prefix-cache hit rate and lower wall time at equal score. Measure cached tokens and seconds per cell |
| H9 | Tool names chosen to match the model's priors, plus aliases. *Source:* Harness-Bench v2: 68 of 69 calls to missing tools were `read` or `read_file` (preprint) | 13-name alias map | Fewer unknown-tool events. Probe E4B for preferred names; compare naming schemes |
| H10 | Write guard extended to shell redirects, `tee` and `dd of=`, with a backup on every overwrite. *Source:* little-coder (README; guard fired on about 57% of exercises) | Clobber guard only (over 300 B and under 50% of size); `create_file` makes no backup | Fewer destroyed files; more `edit_file` use. Arm `write_guard` (N1) |
| H11 | Edit matching cascade up to fuzzy ≥0.88, rejection of edits that introduce syntax errors, read-before-edit. *Source:* Local Quench (README), little-coder (README) | Exact match; `edit_file` disabled after 3 failures | Lower failed-edit rate, fewer disabled tools. Arms `fuzzy_edit`, `syntax_check` |
| H12 | Keep `edit_file` for E4B. *Source:* Leaf: Qwen3.5-4B 37.2% vs 8.3% SWE-bench Verified with typed edit (preprint); ai-muninn: E4B 16.67% SWE-bench Lite with single-match edit (blog, one run) | `edit_file` removed for models parsed as 4B or smaller, which a tag like `gemma4:e4b` may trigger | Higher score on edit-heavy tasks. Arm `small_model_downgrade` on/off |
| H13 | Malformed calls get the parser error back as a tool result and a retry (max 3), instead of only silent salvage. *Source:* Forge (peer-reviewed demo): same-mode gains Qwen3 8B 85.2 → 95.7; "The Parser Is Part of the Agent": silent repair inflates scores and loses 1–4 tasks per seed | Salvage only when native `tool_calls` are empty; fired 4 times in 72 cells | Fewer lost tasks than silent salvage. Arms: salvage, retry-with-error, both; lazy grammar as a fourth arm (§10.4) |
| H14 | Turn contract: every reply ends in a tool call or a visible answer; at most 3 reasoning-only retries. *Source:* nanoagent ≤8B mode (README) | Empty and think-only turns: nudge at 3, reset at 6 | Faster recovery from think-only turns. Threshold sweep |
| H15 | After compaction, pin the original request, changed files, git status, the task list and commands that worked. *Source:* Local Quench (README); Forge compaction ablation 0% / 58% / 76% at 3,600 tokens (peer-reviewed demo) | Re-injects the plan and a file listing | Better long-horizon scores under small contexts. Context sweep 8K / 16K / 32K × pin sets |
| H16 | Compaction never drops tool results the model has not read yet. *Source:* NanoBot dropped unread results in Harness-Bench v2 (preprint) | Level 1 truncates old outputs by age | No loss of unread evidence. Count unread drops; compaction ablation |
| H17 | Thinking budget with abort and retry, value swept. *Source:* little-coder: 2,048, later 4,096 tokens (blog, CHANGELOG) | Thinking on or off only | A budget beats both extremes on E4B. Sweep 1,024 / 2,048 / 4,096 (N2) |
| H18 | Ban interpreter one-liners (`python -c`, `node -e`) and shell writes outside the workspace. *Source:* little-coder bans; Mingbird concedes `python -c` bypasses its boundary | 11 of 44 adversarial probes confirmed on qwen3.5:4b, all through the shell | Confirmed leaks drop on the 44-case probe set, with benign controls still passing. Re-run the probe set |
| H19 | Mechanism profiles by model strength: `small` (all gates) and `strong` (gates off). *Source:* Local Quench `strong` profile (README); Mingbird's frontier probe: 0.977 with gates vs 0.997 without (single run); Liu: full scaffold hurts 8B, neutral on Haiku (preprint) | One mechanism stack for every model | Gates help E4B and hurt or do nothing on 12B and above. Run both profiles on E4B and Gemma 4 12B |
| H20 | Above a flat limit, MCP tools go through a harness-built tree with a pushed shortlist and `tools` navigation (N6, §6.5). *Source:* AnyTool: without the hierarchy GPT-4 fell from 58.9% to 22.4% (ICML 2024); MCP-Zero: up to 98% fewer tool tokens (preprint); Anthropic Tool Search: Opus 4 49% → 74% (vendor evaluation) | MCP tools load flat when their category is routed; no index | At 200 and 500 tools: higher success and fewer hallucinated names than flat-all, non-inferior to the curated-12 oracle. Arm `mcp_scope` on the MCP scale suite (§10.9) |
| H21 | Tools loaded mid-task are declared in a tail message, not in the request's tool list. *Source:* "Ordering or Pruning": a stable tool block is 11–75× cheaper on vLLM (peer-reviewed poster); H8 | Routed MCP tools join the tool list, which the template renders at the start of the prompt | Higher cached-token share after scope changes at equal call accuracy. Arm `mcp_placement` tail or list |
| H22 | Tail-declared tools are called natively or through a proxy `mcp_call(tool, args)` that NanoBird validates. *Source:* none; tools declared outside the template's tool block may be off-distribution for E4B | Native calls only | Keep the form with fewer malformed calls (M5) and N4 events. Arm `mcp_call_form` |
| H23 | Shortlist ranking: BM25, embeddings, the `scope` decision hook, or E4B readout (RFC R7). *Source:* ToolRet: retrievers strong on standard IR benchmarks do poorly on tools, which lowers pass rates (Findings of ACL 2025); RAG-MCP: tool selection 13.62% → 43.13% with retrieval (preprint) | No ranking; keyword router only | A reranker beats BM25 on recall@6 offline (§10.7) and on success at 500 tools. Arm `mcp_rerank` |
| H24 | Tree depth 1 (server → tools) or 2 (server → group → tools), and `find` or `list` as the first move. *Source:* AnyTool's cost of about 43 GPT-4 calls and 135K tokens per query (ICML 2024) | No tree | Depth 2 with find first needs at most 2 navigation calls per task at 500 tools. Count navigation calls per arm |
| H25 | The active MCP schema cap is swept at 6 / 12 / 20. *Source:* Speakeasy: Qwen3-1.7B got 19 of 20 calls right with 20 tools and produced wrong names with 107 (blog); "Ordering or Pruning": 4 → 16 tools, −11.6 points at 0.5B and +7.3 at 7B (peer-reviewed poster) | No cap beyond the routed categories | An interior optimum for E4B, expected near 12. Sweep in the MCP scale suite |

**Test order.** First the hypotheses with peer-reviewed or measured support and cheap arms: H1, H13, H15, H7 and H12. Then H5, H8, H10, H17 and H18, which need little LRAB time or have their own metric. The MCP hypotheses run on their own suite (§10.9) after H7: H21 and H25 first, because they fix placement and the cap, then H20, H23, H22 and H24. The rest (H2, H3, H4, H6, H9, H11, H14, H16, H19) rest on README claims and are tested last. Each run reports its target metric beside the LRAB score.

### 10.9 MCP scale suite

LRAB has almost no MCP surface, so N6 and H20–H25 run on a separate suite of local mock servers. It needs no network and no accounts.

- **Servers:** `tools/mcp-mock`, a Go stdio server that serves a tool list from a fixture file and records every call. Fixtures model common server shapes (issue tracker, code host, database, file store, chat) at 8, 50, 200 and 500 tools, with near-duplicate distractors such as `update_issue` and `update_pull_request`. Some tools are annotated destructive; some carry no annotations.
- **Tasks:** 40, each needing 1–4 MCP calls plus built-in tools; 10 are held out and kept private (§10.5). Scoring is deterministic from the call log and the workdir: the right tools, the right key arguments, the expected artifact, and no destructive call the task did not ask for.
- **Arms:** flat-all (control, `mcp_scope` off), curated-12 (oracle: the needed tools plus distractors up to 12), scoped pull-only, scoped push with BM25, push plus `scope` rerank, push plus readout. At the 200-tool level these are crossed with schema placement (H21) and call form (H22); the cap sweep (H25) and depth arms (H24) run at 500.
- **Metrics:** success, tool-selection accuracy, N4 events (hallucinated names), navigation calls per task, prefill tokens per turn, cached-token share, wall time on the reference machine, permission prompts.
- **Protocol:** three replications per cell (§10.3). A scoped arm becomes the default only if it is non-inferior to curated-12 at 50 tools and beats flat-all at 200, confirmed once on the held-out tasks.

## 11. Delivery

NanoBird ships as one static Go binary per platform, with no cgo and no runtime dependencies, following NanoLLM's build and repository conventions.

### 11.1 Platforms

Windows, Linux and macOS on amd64 and arm64. Windows is a first-class target from stage 1: Mingbird is Windows-first and its LRAB data shows platform fit dominating results for one harness, so path handling, shells and encodings are tested on all three in CI.

### 11.2 CLI

| Verb | Purpose |
|---|---|
| `nanobird acp` | ACP agent over stdio; what clients launch |
| `nanobird run --task FILE --cwd DIR` | Headless run of one task; same loop, prints the stop reason and a summary |
| `nanobird run --resume SESSION` | Resume from the last checkpoint |
| `nanobird doctor` | Engine probes (§7.4) and config check |
| `nanobird sessions list` / `show` | Inspect local sessions and transcripts |
| `nanobird version` | Version, commit, prefill budget in bytes and tokens |

Clients that launch external ACP agents take `nanobird acp` as the command. Per-client setup notes live in `docs/clients.md`, and NanoBird is submitted to the ACP Registry at the first stable release.

### 11.3 Repository layout

```
cmd/nanobird/         entry point, CLI verbs
internal/acp/         ACP agent adapter over coder/acp-go-sdk
internal/loop/        turn state machine, chat/task classifier, budgets
internal/mech/        one file per mechanism id, registry with ablation switches
internal/prefill/     category router, prompt text, budget test
internal/tools/       built-in tools
internal/safety/      rings, path normalization, shell checks
internal/model/       Client interface; nanollm/, openai/, fake/
internal/mcp/         MCP client, tool index, scoped disclosure (§6.5)
internal/skills/      skill index and loader
internal/session/     transcripts, checkpoints, compaction
internal/config/      YAML load, validation, env overrides
tools/lrab/           ACP benchmark runner and scorer adapter
tools/mcp-mock/       mock MCP servers for the MCP scale suite (§10.9)
testdata/golden/      golden transcripts for the fake model
docs/                 SPEC.md (this document), clients.md, ROADMAP.md
```

Go version, linting (`golangci-lint`), Makefile targets and release scripts mirror NanoLLM.

### 11.4 License and attribution

- Proposed license: MIT, matching NanoLLM and NanoASR.
- Mingbird is Apache-2.0. NanoBird ports its designs, which carries no obligation. Any code or prompt text translated from Mingbird keeps Mingbird's copyright notice and NOTICE content in NanoBird's `NOTICE`, with changes marked, as Apache-2.0 section 4 requires.
- The LRAB scorer (Apache-2.0) and task data (CC BY 4.0) used in `tools/lrab` are credited in `NOTICE` and in every published benchmark report.

## 12. Stages and roadmap

Five stages, each closed by a measured gate. Stages 0–2 need nothing new from NanoLLM; stage 3 needs the Harness API RFC.

> **Figure: Roadmap · 5 stages, 5 gates.** Each stage closes on a measured gate; G3 is the success criterion. Stage 0 Baseline closes on G0, Stage 1 Core port on G1, Stage 2 ACP on G2, Stage 3 NanoLLM primitives on G3, and Stage 4 Release on G4. The stages and their gates are listed in [ROADMAP.md](ROADMAP.md).

Gate G3 is the success criterion of §1. A stage that misses its gate stays open, and the next stage does not start; arm names (A0–A3, F0, F1) are defined in §10.2.

## 13. Open questions and risks

The largest open risk is that shell commands are pattern-checked, not sandboxed; the largest dependency is NanoLLM accepting the Harness API RFC.

| Question or risk | Impact | Current plan |
|---|---|---|
| Shell commands are not sandboxed | A methodical model can reach outside the workspace through the shell, as Mingbird's 4B did in 11 of 44 probes | v1 ships pattern lists and permissions and says so; evaluate Landlock or bubblewrap (Linux), `sandbox-exec` (macOS), job objects with a restricted token (Windows) for v1.1 |
| NanoLLM declines or delays parts of the RFC | Stage 3 slips; strict mode and reasoning budgets stay unavailable | Fallbacks in §7.2 keep stages 1–2 independent of the RFC |
| Rescue or strict tool format for E4B | Unknown which is better; Mingbird never measured strict decoding | Decide by the §10.4 sweep |
| Thinking default for E4B | Rival harnesses moved by up to 0.69 on this lever in LRAB | Decide by the §10.4 sweep |
| Context default of 32,768 tokens | E4B accepts far more, but long prefill is slow on an iGPU and long inputs degrade reasoning | Keep 32K for benchmark parity; sweep 64K on the reference machine |
| LRAB tuned by Mingbird's authors on the same tasks | Our gains may not transfer | Frozen held-out set at gates (§10.5); keep part of it private across releases |
| τ²-bench needs a user simulator | A cloud simulator breaks the zero-cloud rule; a local one changes absolute scores | Local simulator by default, named in every report |
| ACP v2 and remote transport are drafts | Migration work; web UIs need a bridge meanwhile | Track the Transports Working Group; keep the ACP adapter thin |
| Name close to NanoBot (HKUDS), a harness in the same niche | Search and citation confusion | Keep NanoBird; state the Mingbird lineage in the README's first line |
| Sub-agents are left out | Some long tasks may need them | Revisit after v1 with ACP's subagent-sessions RFD |
| Decision-model weights are often non-commercial (StartLux: CC BY-NC 4.0) | Cannot be shipped with NanoBird or used in commercial products without a separate licence | NanoBird ships no weights; backends are user-configured; each tested model's licence is recorded in the evaluation report |
| Two incompatible decision APIs: System One and OpenAI /v1/decisions; LLM Gateway lists /v1/systemone as legacy for non-TypeSafe models | Protocol churn | Thin client behind one interface; add a /v1/decisions adapter once a local server speaks it |
| A server reads a bidirectional decision model's prompt causally and gives no error | Silent quality loss that looks like a weak model | Golden probe file in doctor (§7.6); NanoLLM refuses unsupported attention modes (RFC R7) |
| The harness-built tree groups a server's tools badly | The model browses the wrong branch and spends turns | `find` searches the whole index regardless of groups; groups and labels can be overridden in `mcp.json`; the suite reports navigation calls per server (§10.9) |
| E4B calls tail-declared tools less reliably than tools in the template's tool block | Scoped mode loses on accuracy what it saves on prefill | Proxy `mcp_call` arm (H22); failing that, change the tool list only at `todo` boundaries and accept the cache loss |
| MCP tool descriptions and results can carry prompt injection | A server steers the model or pulls workspace data out through a later call | Results are data and cannot load tools (§6.4); annotations never remove a prompt on an untrusted server; tools that ask on every call are never pushed |

## 14. References

Of the sources below, only AnyTool (ICML 2024) and ToolRet (Findings of ACL 2025) are peer-reviewed; the arXiv entries are preprints and the rest are project documentation, specifications, blog posts or datasets. No ISO standard covers agent harnesses.

- Wang, Huang. [Mingbird: A Local-First Agent Harness Enabling Small Open Models to Complete Real Tasks](https://arxiv.org/abs/2610.02001). arXiv 2610.02001, October 2026. Preprint; self-built benchmark, single machine, single trials.
- [Mingbird-agent repository](https://github.com/Mingbird/Mingbird-agent) (Apache-2.0; benchmark data CC BY 4.0) and its [AGENTS.md](https://github.com/Mingbird/Mingbird-agent/blob/main/AGENTS.md).
- Inbar. [Honey, I Shrunk the Coding Agent](https://itayinbarr.substack.com/p/honey-i-shrunk-the-coding-agent) (little-coder on Qwen3.5-9B, Aider Polyglot). Blog post; observational, no ablation.
- [Harness-Bench: Measuring Harness Effects across Models in Realistic Agent Workflows](https://arxiv.org/abs/2605.27922). arXiv 2605.27922. Preprint.
- Hu et al. [Beyond the Model: Demystifying Harness Effects in Software Engineering Agents](https://arxiv.org/pdf/2609.32459). arXiv 2609.32459. Preprint; large API models only.
- [pinchbench-pi-trajectories](https://huggingface.co/datasets/yangw1234/pinchbench-pi-trajectories). Dataset card documenting the Gemma 4 vLLM template and parser defects. One run per arm.
- Agent Client Protocol: [v1 overview](https://agentclientprotocol.com/protocol/v1/overview.md), [prompt turn](https://agentclientprotocol.com/protocol/v1/prompt-turn.md), [tool calls](https://agentclientprotocol.com/protocol/v1/tool-calls.md), [Streamable HTTP and WebSocket transport RFD](https://agentclientprotocol.com/rfds/streamable-http-websocket-transport.md), [clients](https://agentclientprotocol.com/get-started/clients.md), [community libraries](https://agentclientprotocol.com/libraries/community.md).
- [coder/acp-go-sdk](https://github.com/coder/acp-go-sdk) (Apache-2.0).
- [NanoLLM](https://github.com/usunrise88/NanoLLM) (MIT) and the companion RFC: NanoLLM Harness API.
- τ²-bench (Sierra Research), as cited and run in the Mingbird paper.
- System One protocol: [OpenRouter API reference](https://openrouter.ai/docs/api/api-reference/systemone/submit-a-system-one-request.md), [LLM Gateway](https://docs.llmgateway.io/features/system-one), [Pydantic AI](https://pydantic.dev/docs/ai/models/system-one/), [ngrok: Decisions and System One](https://ngrok.com/docs/ai-gateway/guides/decisions-and-system-one.md). Vendor documentation.
- [Jev inside TypeSafe AI's first System One decision model](https://www.hyperstack.cloud/technical-resources/tutorials/jev-inside-typesafe-ais-first-system-one-decision-model). Blog; vendor claims marked as such.
- [Ollama adds local decision models](https://runtimewire.com/article/ollama-local-decision-models-systemone). News, Ollama 0.35.
- [StartLux-Decision-v1.1-0.8B-Instant](https://huggingface.co/startlux-models/StartLux-Decision-v1.1-0.8B-Instant). Model card; self-reported benchmarks; CC BY-NC 4.0.
- [Model Context Protocol specification: Tools](https://modelcontextprotocol.io/specification/2025-06-18/server/tools) (2025-06-18). Annotations untrusted unless the server is trusted; clients should time out tool calls.
- [AnyTool: Self-Reflective, Hierarchical Agents for Large-Scale API Calls](https://ar5iv.labs.arxiv.org/html/2402.04253). ICML 2024, peer-reviewed; GPT-4 only.
- [MCP-Zero: Active Tool Discovery for Autonomous LLM Agents](https://arxiv.org/pdf/2506.01056). arXiv 2506.01056. Preprint; frontier models only, main test on 48 tools.
- [SING: Synthetic Intention Graph for Scalable Active Tool Discovery in LLM Agents](https://arxiv.org/pdf/2606.16591). arXiv 2606.16591. Preprint; strong models only.
- Shi et al. [Retrieval Models Aren't Tool-Savvy: Benchmarking Tool Retrieval for Large Language Models](https://preview.aclanthology.org/dashboard/2025.findings-acl.1258) (ToolRet). Findings of ACL 2025, peer-reviewed.
- [RAG-MCP: Mitigating Prompt Bloat in LLM Tool Selection via Retrieval-Augmented Generation](https://arxiv.org/html/2505.03275v1). arXiv 2505.03275. Preprint.
- Lee et al. [Don't Adapt Small Language Models for Tools; Adapt Tool Schemas to the Models](https://arxiv.org/pdf/2510.07248) (PA-Tool). arXiv 2510.07248. Preprint; 3–8B models.
- Anthropic. [Advanced tool use](https://www.anthropic.com/engineering/advanced-tool-use) (Tool Search Tool). Vendor evaluation on Claude models.
- Speakeasy. [Less is more](https://www.speakeasy.com/mcp/tool-design/less-is-more) (tool count versus accuracy on small models). Blog; informal test.
