package skills

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Skill is one skill: the name and one-line description that the index shows,
// and the file that holds its instructions.
type Skill struct {
	Name        string
	Description string
	Path        string
}

// Load finds the skills in the directories, in order. Each directory may hold
// folders with a SKILL.md, or Markdown files, each with frontmatter giving its
// name and description (the Agent Skills convention, SPEC §5.9). A name found
// earlier wins, and a missing directory is skipped.
func Load(dirs ...string) ([]Skill, error) {
	var out []Skill
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			fallback := strings.TrimSuffix(e.Name(), ".md")
			if e.IsDir() {
				path = filepath.Join(path, "SKILL.md")
				fallback = e.Name()
			} else if !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			s, err := read(path, fallback)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if !seen[s.Name] {
				seen[s.Name] = true
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// Body returns the instructions of a skill, without its frontmatter.
func Body(s Skill) (string, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return "", err
	}
	_, body := splitFrontmatter(data)
	return strings.TrimSpace(string(body)), nil
}

// Index renders the skills as the lines of the prefill index: a name and a
// one-line description each. It returns "" when there are no skills.
func Index(list []Skill) string {
	var b strings.Builder
	for _, s := range list {
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
	}
	return b.String()
}

// read parses one skill file. Its frontmatter may set name and description.
func read(path, fallback string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	meta, _ := splitFrontmatter(data)
	s := Skill{Name: fallback, Path: path}
	if v := meta["name"]; v != "" {
		s.Name = v
	}
	s.Description = meta["description"]
	return s, nil
}

// splitFrontmatter reads the "key: value" lines between leading --- markers and
// returns them with the rest of the file.
func splitFrontmatter(data []byte) (map[string]string, []byte) {
	meta := map[string]string{}
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return meta, data
	}
	rest := data[len("---\n"):]
	sc := bufio.NewScanner(bytes.NewReader(rest))
	consumed := 0
	for sc.Scan() {
		line := sc.Text()
		consumed += len(line) + 1
		if strings.TrimSpace(line) == "---" {
			return meta, rest[min(consumed, len(rest)):]
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			meta[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return map[string]string{}, data
}
