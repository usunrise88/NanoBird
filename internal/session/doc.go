// Package session records transcripts and checkpoints and runs staged
// compaction, so every run can be audited and resumed (SPEC §4.4, §9.2, §9.3).
//
// Lands with stage 1.
package session
