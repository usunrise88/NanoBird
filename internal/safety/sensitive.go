package safety

import (
	"path"
	"strings"
)

// credentialNames are the path components of credential stores (SPEC §5.8, M8).
var credentialNames = map[string]bool{
	".ssh": true, ".aws": true, ".gnupg": true, ".kube": true, ".docker": true,
	".netrc": true, "id_rsa": true, "id_ed25519": true,
}

// TouchesSensitive reports whether a shell command names a credential store and,
// if so, which one. A .env file counts only when the command names it from
// outside the workspace, as SPEC §5.8 scopes the pattern.
func TouchesSensitive(command string) (string, bool) {
	for _, word := range strings.Fields(command) {
		word = strings.Trim(word, `"'`)
		parts := strings.FieldsFunc(word, func(r rune) bool { return r == '/' || r == '\\' })
		for _, p := range parts {
			if credentialNames[p] {
				return p, true
			}
		}
		outside := strings.HasPrefix(word, "~") || strings.HasPrefix(word, "/") ||
			strings.HasPrefix(word, `\`) || strings.Contains(word, "$") ||
			strings.Contains(word, "..") || hasDrive(word)
		if base := path.Base(strings.ReplaceAll(word, `\`, "/")); outside && (base == ".env" || strings.HasPrefix(base, ".env.")) {
			return ".env outside the workspace", true
		}
	}
	return "", false
}
