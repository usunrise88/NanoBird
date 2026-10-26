// Package safety holds the safety rings, path normalization and shell pattern
// checks (SPEC §5.8, §5.11). Shell commands are checked against pattern lists,
// not sandboxed (SPEC §13).
//
// Lands with stage 1.
package safety
