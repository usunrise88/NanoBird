// Package session records what a run did, one JSON record per event
// (SPEC §9.2). The transcript is the audit trail that makes every benchmark
// number recomputable. Checkpoints and staged compaction come later (SPEC §4.4,
// §9.3).
package session
