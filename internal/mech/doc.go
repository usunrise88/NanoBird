// Package mech holds the mechanisms, one file per id: M1 to M10 and N1 to N4
// (SPEC §5). Each registers at its hook points and carries an ablation id, so
// any of them can be switched off without a code change (SPEC §2, rule 3).
//
// Lands with stage 1.
package mech
