// Package safety keeps file and shell actions inside the workspace: path
// normalization (M8), the shell checks of rings 1 to 3 and the backups and trash
// of ring 4 (SPEC §5.8, §5.11). Shell commands are checked against pattern
// lists, not sandboxed (SPEC §13).
package safety
