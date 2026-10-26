// Package openai is the backend for OpenAI-compatible chat endpoints, NanoLLM's
// OpenAI dialect first (SPEC §7.3). It streams replies, collects tool calls and
// ends a turn that stays silent for the stall timeout (SPEC §7.5).
package openai
