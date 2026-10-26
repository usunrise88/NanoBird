// Package nanollm is the native NanoLLM backend over the Harness API (SPEC §7.2).
// It probes a model's capabilities once, and each missing feature falls back as the
// specification says: no prefix handle, so the static prefill relies on the engine's
// prompt cache; rescue instead of strict tool calls when the engine has no strict
// format; and no reasoning budget when the engine has none.
package nanollm
