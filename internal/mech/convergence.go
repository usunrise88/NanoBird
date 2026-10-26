package mech

// convergenceHint is the notice an edit that undoes the edit before it draws.
const convergenceHint = "This edit undid the one before it, so the file is going back and forth. Read the file again and change your approach."

// Convergence notices an edit that returns a file to the version it had two
// writes earlier (M6). It keeps the last three versions of each file.
type Convergence struct {
	versions map[string][]string
}

// NewConvergence returns a tracker for one task.
func NewConvergence() *Convergence {
	return &Convergence{versions: map[string][]string{}}
}

// Observe records the content a write left in file. It returns the notice when
// the content matches the version two writes ago and differs from the one in
// between.
func (c *Convergence) Observe(file, content string) string {
	v := append(c.versions[file], content)
	if len(v) > 3 {
		v = v[len(v)-3:]
	}
	c.versions[file] = v
	if len(v) == 3 && v[0] == v[2] && v[0] != v[1] {
		return convergenceHint
	}
	return ""
}
