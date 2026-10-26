// Package mcp is the MCP client over stdio and streamable HTTP, with flat tool
// loading (SPEC §5.9, §6.4). Scoped disclosure above the flat limit (SPEC §6.5)
// waits for the MCP scale suite (SPEC §10.9).
//
// Lands with stage 1 for flat loading; scoped disclosure has no stage yet, see
// docs/ROADMAP.md.
package mcp
