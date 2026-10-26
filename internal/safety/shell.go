package safety

import (
	"regexp"
	"strings"
)

// Level is the verdict of a shell check (SPEC §5.11).
type Level int

const (
	// Allow means no ring objects to the command.
	Allow Level = iota
	// Ask means a ring needs permission: an ACP prompt when attended, a
	// denial when unattended.
	Ask
	// Refuse means ring 1 forbids the command in every mode.
	Refuse
)

// Decision is the verdict on one command and the reason a message can show.
type Decision struct {
	Level  Level
	Reason string
}

// ring1 lists the irreversible commands, refused in every mode.
var ring1 = []struct {
	reason string
	re     *regexp.Regexp
}{
	{"formats a disk", regexp.MustCompile(`(?i)\bformat(\.com)?\s+[a-z]:`)},
	{"partitions a disk", regexp.MustCompile(`(?i)\bdiskpart\b`)},
	{"creates a file system", regexp.MustCompile(`(?i)\bmkfs(\.\w+)?\b`)},
	{"writes to a raw device", regexp.MustCompile(`(?i)\bdd\b[^|;&]*\bof=/dev/`)},
	{"deletes shadow copies", regexp.MustCompile(`(?i)\bvssadmin\b.*\bdelete\s+shadows\b`)},
	{"unregisters a WSL distribution", regexp.MustCompile(`(?i)\bwsl(\.exe)?\b.*--unregister\b`)},
	{"runs DISM", regexp.MustCompile(`(?i)\bdism(\.exe)?\b`)},
	{"removes a driver", regexp.MustCompile(`(?i)\bpnputil(\.exe)?\b.*/delete-driver\b|\bRemove-WindowsDriver\b`)},
	{"deletes a system user", regexp.MustCompile(`(?i)\buserdel\b`)},
}

// ring2 lists system mutations that need permission.
var ring2 = []struct {
	reason string
	re     *regexp.Regexp
}{
	{"uninstalls packages", regexp.MustCompile(`(?i)\b(apt|apt-get|dnf|yum|zypper|pacman|snap|winget|choco|brew|pip|pip3)\s+(remove|purge|uninstall|autoremove|erase)\b`)},
	{"changes the system environment", regexp.MustCompile(`(?i)\bsetx(\.exe)?\b|\breg(\.exe)?\s+(add|delete)\b|\bSetEnvironmentVariable\b`)},
}

// recursiveDelete matches the commands that delete whole trees (ring 3).
var recursiveDelete = regexp.MustCompile(`(?i)\brm\s+(-[a-z]*r|--recursive)|\b(rd|rmdir|del)\s+/s\b|\bRemove-Item\b.*-Recurse\b`)

// AssessShell checks a command against the rings. The checks are pattern lists,
// not a sandbox: a command can still reach outside the workspace (SPEC §13).
func AssessShell(command string) Decision {
	for _, r := range ring1 {
		if r.re.MatchString(command) {
			return Decision{Level: Refuse, Reason: r.reason}
		}
	}
	for _, r := range ring2 {
		if r.re.MatchString(command) {
			return Decision{Level: Ask, Reason: r.reason}
		}
	}
	if recursiveDelete.MatchString(command) && namesOutside(command) {
		return Decision{Level: Ask, Reason: "recursive delete outside the workspace"}
	}
	return Decision{Level: Allow}
}

// namesOutside reports whether a command names a path outside the workspace:
// an absolute or home-relative path, a variable, a drive or a parent directory.
// It is a heuristic over the words of the command and errs toward asking.
func namesOutside(command string) bool {
	for _, w := range strings.Fields(command) {
		w = strings.Trim(w, `"'`)
		if isSwitch(w) {
			continue
		}
		if strings.HasPrefix(w, "/") || strings.HasPrefix(w, `\`) || strings.HasPrefix(w, "~") ||
			strings.Contains(w, "$") || strings.Contains(w, "..") || hasDrive(w) {
			return true
		}
	}
	return false
}

// isSwitch matches a Windows command switch such as /s.
func isSwitch(w string) bool {
	return len(w) == 2 && w[0] == '/' && isASCIILetter(w[1])
}

// hasDrive matches a drive letter such as C:.
func hasDrive(w string) bool {
	return len(w) >= 2 && w[1] == ':' && isASCIILetter(w[0])
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
