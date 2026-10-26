# ACP clients

NanoBird is an ACP agent. Any client that launches external ACP agents can run `nanobird acp` as its command (SPEC §8, §11.2). The architecture figure (SPEC §3) lists Zed, JetBrains, Neovim and Toad. This page covers what the agent takes and what it does today. A client's own configuration format is the client's to document.

## Command

    nanobird acp [--endpoint URL] [--model NAME] [--key-env VAR] [--max-turns N] [--ablation IDS] [--log-level LEVEL]

The agent speaks ACP v1 over stdio. Stdout carries the protocol only; logs go to stderr and to `.nanobird/logs`.

- Each session takes its workspace from the client: the `cwd` of `session/new`, which must be an absolute path.
- That workspace's `.nanobird/config.yaml` applies to the session, and so does the user config. The flags above override both for every session.
- The model key is read from the environment variable that `model.key_env` names. It is never written to a file by NanoBird.
- Each session writes its transcript to `.nanobird/sessions` and its structured log to `.nanobird/logs`, in its workspace.

## What works today

- `initialize`, `session/new`, `session/prompt` and `session/cancel`.
- Session updates: the model's text as it streams, each tool call with its kind, path and status, the output of each call, the diff of an edit, and the plan from the todo list.
- A prompt continues the conversation of the session before it.
- A prompt ends with one of the stop reasons of SPEC §4.3. A cancelled prompt answers `cancelled`.

Not yet: `session/load`, `session/list`, `session/close`, `session/resume`, permission prompts (a ring 2 or 3 call is refused, because the agent runs unattended), modes and config options, thinking, usage and notices, and the client's file system and terminal. The stage-2 gate (SPEC §12) also needs the ACP test compatibility kit in CI, which is not in place yet.
