# ACP test kit

`make acp-tck` runs the official ACP test kit, [agentclientprotocol/acp-tck](https://github.com/agentclientprotocol/acp-tck), against `nanobird acp` (SPEC §8, gate G2). The `acp-tck` job in CI runs the same target on Linux.

## What runs

- **The kit** is fetched at the commit in `run.sh` (`KIT_REF`) and installed from its `uv.lock`. The lock names 11 packages from PyPI, each at a fixed version, and no git sources. To move the kit, change the commit, read its diff, and check the lock again.
- **The agent** is `nanobird acp`, built from this tree.
- **The model** is `tools/acptck/model`, a scripted OpenAI-dialect server on `127.0.0.1`, port 18081 by default. A request with tools gets one `finish` call. A request without tools gets the text `ok`. It needs no engine and no key.

The kit launches the agent as a subprocess over stdio. Its source imports no network module.

## Running it

You need `uv` (CI pins 0.12.3), Go, and network access to GitHub and PyPI. On the first run `uv` also fetches Python 3.14, which the kit requires.

    make acp-tck

Working files go to `.acp-tck/`, which git ignores: the kit checkout, the built binaries, and `report.json`, the kit's full JSON report. The target fails when the kit's verdict is not CONFORMANT.

## What the kit does not cover

- Capabilities that the agent does not advertise are skipped: auth logout, prompt images, audio and embedded context, additional directories, and the system prompt.
- CI runs against the scripted model only. A run against NanoLLM needs the engine and the endpoint key, so it is not part of CI.
