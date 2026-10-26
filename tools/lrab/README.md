# tools/lrab

The LRAB runner (SPEC §10.1). It is an ACP client. Each cell gets a fresh workdir with the task's fixtures, one `session/prompt` with the task text and that workdir as `cwd`, and a budget. Then LRAB's deterministic artifact scorer grades the workdir, unchanged, with the judge off.

## Running it

    go run ./tools/lrab -agent "nanobird acp --endpoint http://127.0.0.1:8081 --model gemma-4-e4b-qat" -only WF-01 -reps 3

The `-agent` value is split on spaces, so the program path cannot contain spaces yet. A key, when the engine needs one, comes from the environment (`NANOBIRD_MODEL_KEY`) and is never written into a result.

Flags:

- `-only`: task ids, separated by commas. The default runs all 18 dev tasks.
- `-reps`: replications of each task (default 1).
- `-budget`: a budget for every cell. The default follows SPEC §10.1.
- `-permissions`: `deny` (default) or `allow`, which approves each request once.
- `-ablate`: mechanisms that the session leaves off, separated by commas (SPEC §8.8).
- `-python`: the interpreter that runs the scorer (default `python3`).
- `-out`: the results directory (default `.lrab/results/<UTC time>`).

Results go under the results directory, which git ignores. Each cell has a folder `<task>/rep<n>/` with `cell.json` (the score, the stop reason, the timing, the mechanism firings from `_meta.nanobird` and `_nanobird/stats`), `agent.log` (the agent's stderr and the protocol log), `nanobird/` (the agent's own folder), and `work/` (the workdir as the agent left it). `summary.json` has the means.

## What is vendored

| Path | From Mingbird's `bench/lrab` | License |
| --- | --- | --- |
| `tasks/` | the 18 dev tasks | CC BY 4.0 |
| `fixtures/` | the fixtures of those tasks | CC BY 4.0 |
| `scoring/score_task.py` | the scorer, unchanged | Apache-2.0 |
| `LICENSE-DATA` | Mingbird's data license | none of its own |

The held-out tasks are not vendored. SPEC §12 reserves them for gate G4. Credits are in [NOTICE](../../NOTICE).

## Choices the spec leaves open

- **Budgets.** SPEC §10.1 sets 90 minutes for a workflow cell and 180 for a long-horizon cell. The task files still give `max_wall_minutes` as 60 and 90, but those were authoring-time values. Mingbird's protocol freeze raised them to 90 and 180 (`benchmarks/DESIGN.md` in Mingbird), and the runner uses that protocol.
- **Permissions.** Requests are rejected by default, as an unattended `nanobird run` rejects them. ACP and headless runs then share one policy (SPEC §12, G2).
- **Timeouts.** A timeout cancels the prompt, waits up to 30 seconds for the stop, and scores 0 (SPEC §10.1).
- **Judge.** Off. The judge would call a model server, and the runs use no Ollama. The scorer renormalizes its weights without the judge.
- **Audit.** The agent's `.nanobird/` folder moves out of the workdir before grading, so it does not count in the artifact checks (SPEC §10.1).
- **Resume test.** LH-03 carries a `resume_test`. The runner kills the agent at `kill_at_pct` of the budget (50 by default), starts it again in the same workdir, resumes the session with `session/resume`, and sends the task text again with the rest of the budget. The second attempt counts, as Mingbird's protocol says ("1 retry, latest attempt wins"). A run that ends before the kill point is not resumed.

## Tests

`go test ./tools/lrab/` runs the runner against a fake ACP agent: the fixtures, the budgets, a timeout that cancels and scores 0, the mechanism counts, the kill and resume of a resume-test cell, the summary, and the vendored scorer (skipped without `python3`).
