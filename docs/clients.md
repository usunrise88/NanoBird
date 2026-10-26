# ACP clients

NanoBird is an ACP agent. Any client that launches external ACP agents can run `nanobird acp` as its command (SPEC §8, §11.2). The architecture figure (SPEC §3) lists Zed, JetBrains, Neovim and Toad. This page covers what the agent takes and what it does today. A client's own configuration format is the client's to document.

## Command

    nanobird acp [--endpoint URL] [--backend nanollm|openai] [--model NAME] [--key-env VAR] [--max-turns N] [--ablation IDS] [--log-level LEVEL]

The agent speaks ACP v1 over stdio. Stdout carries the protocol only; logs go to stderr and to `.nanobird/logs`.

- Each session takes its workspace from the client: the `cwd` of `session/new`, which must be an absolute path.
- That workspace's `.nanobird/config.yaml` applies to the session, and so does the user config. The flags above override both for every session. [CONFIG.md](CONFIG.md) lists every key.
- The model key is read from the environment variable that `model.key_env` names. It is never written to a file by NanoBird.
- Each session writes its transcript to `.nanobird/sessions` and its structured log to `.nanobird/logs`, in its workspace.

## What works today

- Methods: `initialize`, `authenticate` (no methods to run), `session/new`, `session/load`, `session/resume`, `session/list`, `session/close`, `session/delete`, `session/prompt`, `session/cancel`, `session/set_mode` and `session/set_config_option`.
- Session updates: the model's text and its thinking as they stream, each tool call with its kind, path and status, the output of each call and the diff of an edit, the plan from the todo list, the usage of each reply, the title from the first task, and the chat or task decision of each prompt. Setup warnings and compactions come as short messages.
- Permissions: a shell command that a safety ring stops asks the client, which can allow it once, allow its command family for the session (ring 2 only), or reject it.
- Config options: `thinking` (off, plan or on), `offline` (off or on), and `model` when the engine lists its models. Values apply from the next prompt.
- MCP servers that the client passes to `session/new`, `session/load` or `session/resume` start with the session: stdio and streamable HTTP. Their tools are untrusted, so each call asks the client for permission, as a shell call does (SPEC §6.4).
- The client's file system and terminal, when the client offers them and the configuration allows: `acp.client_fs: read` and `acp.client_terminal: on`. Both are off by default.
- Extensions: `_meta.nanobird` on a tool call update that a mechanism changed, `_nanobird/stats` for a session's counters, and `_nanobird/ablation` to set the mechanisms a session leaves off, before its first prompt.

## Not yet

- Scoped MCP disclosure (SPEC §6.5) is not built: above `mcp.flat_limit` no MCP tool loads, and the setup warning says so.
- The ACP test kit runs in CI against the scripted model (`make acp-tck`). A run against a live NanoLLM engine is not in CI.
