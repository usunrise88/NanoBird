// Package loop runs one prompt as a bounded sequence of model turns: the turn
// states, the stop reasons and the budgets (SPEC §4). The chat/task classifier
// (M2) and the mechanisms hook in at the fixed points of SPEC §3.
package loop
