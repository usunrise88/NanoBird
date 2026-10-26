# Configuration

NanoBird takes its settings from YAML files, from `NANOBIRD_*` environment variables and from flags. This page lists every key with its default. The loader is `internal/config/config.go`.

## Sources

Later sources win:

1. The built-in defaults, listed below.
2. The user file: `$XDG_CONFIG_HOME/nanobird/nanobird.yaml`, or `~/.config/nanobird/nanobird.yaml` when that variable is unset. On Windows it is `%AppData%\NanoBird\nanobird.yaml`.
3. The workspace file, `.nanobird/config.yaml`. An ACP session takes its workspace from the client. `run` and `doctor` take it from `--cwd`.
4. `NANOBIRD_*` environment variables. `NANOBIRD_ABLATION` adds to `mechanisms.disable`.
5. The flags of the command.

A missing file is skipped. A key that the code does not know is an error, and so is a value out of range; the error names the key.

An environment variable is the key path in upper case with dots as underscores. `loop.max_turns` is `NANOBIRD_LOOP_MAX_TURNS`, and `model.key_env` is `NANOBIRD_MODEL_KEY_ENV`. A list is comma-separated.

## Keys

### model

| Key | Default | Meaning |
| --- | --- | --- |
| `model.backend` | `nanollm` | `nanollm` speaks the native NanoLLM API under `/api/v1`. `openai` speaks the OpenAI dialect, for engines without a native API. |
| `model.endpoint` | `http://127.0.0.1:8080` | Base URL of the engine. |
| `model.name` | `gemma-4-e4b` | Model name sent to the engine. |
| `model.key_env` | `NANOBIRD_MODEL_KEY` | Name of the environment variable that holds the API key. The key never goes in a file. |

### loop

| Key | Default | Meaning |
| --- | --- | --- |
| `loop.max_turns` | `60` | Model turns allowed for one task. |
| `loop.output_cap_tokens` | `8192` | Output tokens allowed for one reply. |
| `loop.context_tokens` | `0` | Context size the loop plans for. `0` means 32768. |
| `loop.compaction_at` | `0.9` | Share of the context at which staged compaction runs, in (0, 1]. |
| `loop.temperature` | `0` | Sampling temperature. Must not be negative. |
| `loop.thinking` | `off` | `off` never thinks. `plan` thinks on the first turn of a task and on the turn after a finish is rejected. `on` thinks on every turn. |
| `loop.thinking_budget` | `1024` | Thinking token budget of a request that thinks. NanoBird also cuts a thought that runs past four characters per budget token, and retries the turn without thinking (`think_budget`, SPEC §4.5). |
| `loop.tool_format` | `rescue` | `rescue` reads tool calls from the text. `strict` makes the engine constrain them with a grammar. `strict` needs `model.backend: nanollm`; the OpenAI dialect refuses it at start. A model that the engine does not serve in strict form falls back to `rescue`. |

### mechanisms

| Key | Default | Meaning |
| --- | --- | --- |
| `mechanisms.disable` | none | Mechanism ids to switch off, for ablation runs (SPEC §5). |

Built ids: `anti_loop`, `boundary`, `compaction`, `edit_safety`, `finish_gate`, `format_rescue`, `layering`, `prefill`, `resilience`, `self_check`, `think_budget`, `tool_name_fix`, `verify_feedback`, `write_guard`. Not built, and refused when listed: `disclosure`, `mcp_scope`, `plan_reinject`.

`think_budget` switches off only NanoBird's cut. The engine still receives `loop.thinking_budget`.

### tools

| Key | Default | Meaning |
| --- | --- | --- |
| `tools.shell_timeout_s` | `120` | Seconds one shell command may run. |
| `tools.result_cap_bytes` | `16384` | Bytes of one tool result that reach the model. A longer result keeps its first three quarters and its last quarter, with a note of what was left out. |

### safety

| Key | Default | Meaning |
| --- | --- | --- |
| `safety.unattended` | `deny` | Not read yet. The runs set `unattended` themselves, `run` and ACP sessions alike, so this key has no effect. |

### mcp

| Key | Default | Meaning |
| --- | --- | --- |
| `mcp.config` | `mcp.json` | MCP server file. A relative path is inside `.nanobird/`. |
| `mcp.flat_limit` | `12` | Most MCP tools that load. Above the limit no MCP tool loads, and a warning says so. |
| `mcp.call_timeout_s` | `60` | Seconds one MCP call may take. A server that times out twice leaves the run. |

### skills

| Key | Default | Meaning |
| --- | --- | --- |
| `skills.dirs` | none | Extra skill directories. Skills load from `.nanobird/skills` in the workspace, then from these, then from `nanobird/skills` in the user config directory. |

### acp

| Key | Default | Meaning |
| --- | --- | --- |
| `acp.client_fs` | `off` | `read` lets `read_file` read through the client, when the client offers a file system. |
| `acp.client_terminal` | `off` | `on` lets `run_shell` run in a terminal of the client, when the client offers one. |

### offline

| Key | Default | Meaning |
| --- | --- | --- |
| `offline` | `false` | Keeps the run on the machine. MCP servers reached over a URL do not start, and MCP tools that reach outside are hidden. ACP clients can set it per session. |

## Keys that have no effect yet

These keys are accepted and read nothing:

- `tools.web`, `tools.client_fs`, `tools.client_terminal`. The client's file system and terminal are set under `acp.*`, so the `tools.*` pair duplicates them.
- `safety.unattended`.
- `mcp.active_cap`, `mcp.push`, `mcp.push_size`, `mcp.rerank`, `mcp.call_form`. Scoped MCP disclosure (SPEC §6.5) is not built.

## Flags

- `run`, `acp` and `doctor` take the engine flags: `--endpoint`, `--backend`, `--model`, `--key-env`, `--max-turns` and `--ablation`. A flag overrides the file and environment values for that command.
- `run` and `doctor` take `--cwd`, the workspace directory (default `.`).
- `run` and `acp` take `--log-level`: `debug`, `info`, `warn` or `error`. The default comes from `NANOBIRD_LOG_LEVEL`, and is `info` without it.

## Environment

- The variable named by `model.key_env` (default `NANOBIRD_MODEL_KEY`) holds the API key. NanoBird reads it and never writes it to a file.
- `NANOBIRD_ABLATION`: mechanism ids to switch off, comma-separated.
- `NANOBIRD_LOG_LEVEL`: the default for `--log-level`.
