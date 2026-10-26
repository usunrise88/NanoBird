package safety

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Ignore is the ignore list of a workspace: the entries of its .gitignore, read in
// order, where the last entry that matches decides (SPEC §6.1). Nested ignore
// files and the other rules of git are not read.
type Ignore struct {
	rules []ignoreRule
}

// ignoreRule is one entry of an ignore file.
type ignoreRule struct {
	re       *regexp.Regexp
	negate   bool // a "!" entry takes a path back in
	dirOnly  bool // an entry with a trailing slash names folders only
	baseOnly bool // an entry without a slash matches a base name at any depth
}

// LoadIgnore reads the .gitignore at the root of the workspace. A workspace without
// one has an empty list.
func LoadIgnore(root string) (*Ignore, error) {
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if errors.Is(err, fs.ErrNotExist) {
		return &Ignore{}, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseIgnore(string(data)), nil
}

// ParseIgnore reads the entries of an ignore file. Blank lines and comments are
// skipped, and so is an entry that does not compile.
func ParseIgnore(text string) *Ignore {
	ig := &Ignore{}
	for _, line := range strings.Split(text, "\n") {
		if r, ok := parseRule(line); ok {
			ig.rules = append(ig.rules, r)
		}
	}
	return ig
}

// Ignored reports whether the workspace path rel, slash-separated, is ignored. The
// walks skip an ignored folder, so its contents go with it.
func (ig *Ignore) Ignored(rel string, isDir bool) bool {
	ignored := false
	for _, r := range ig.rules {
		if r.dirOnly && !isDir {
			continue
		}
		target := rel
		if r.baseOnly {
			target = path.Base(rel)
		}
		if r.re.MatchString(target) {
			ignored = !r.negate
		}
	}
	return ignored
}

// parseRule reads one line of an ignore file.
func parseRule(line string) (ignoreRule, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ignoreRule{}, false
	}
	var r ignoreRule
	if rest, ok := strings.CutPrefix(line, "!"); ok {
		r.negate = true
		line = rest
	}
	if rest, ok := strings.CutSuffix(line, "/"); ok {
		r.dirOnly = true
		line = rest
	}
	r.baseOnly = !strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")
	re, err := regexp.Compile("^" + globRegexp(line) + "$")
	if line == "" || err != nil {
		return ignoreRule{}, false
	}
	r.re = re
	return r, true
}

// globRegexp turns the pattern of an entry into the body of a regular expression.
// A star and a question mark do not cross a slash, a double star does, and a [...]
// class is kept.
func globRegexp(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); {
		rest := p[i:]
		switch {
		case strings.HasPrefix(rest, "**/"):
			b.WriteString("(?:.*/)?")
			i += 3
		case rest == "/**":
			b.WriteString("/.*")
			i += 3
		case strings.HasPrefix(rest, "**"):
			b.WriteString(".*")
			i += 2
		case rest[0] == '*':
			b.WriteString("[^/]*")
			i++
		case rest[0] == '?':
			b.WriteString("[^/]")
			i++
		case rest[0] == '[' && strings.Contains(rest, "]"):
			end := strings.Index(rest, "]")
			class := rest[1:end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + class + "]")
			i += end + 1
		default:
			b.WriteString(regexp.QuoteMeta(rest[:1]))
			i++
		}
	}
	return b.String()
}
