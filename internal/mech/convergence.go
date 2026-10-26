package mech

// convergenceHint is the notice a write that undoes an earlier edit draws.
const convergenceHint = "This edit undid an earlier edit to the same text, so the file is going back and forth. Read the file again and change your approach."

// Edit is one exact replacement that edit_file made.
type Edit struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// Convergence notices writes that undo one another (M6). It keeps the last three
// versions of each file and the last three edits to each file.
type Convergence struct {
	versions map[string][]string
	edits    map[string][]Edit
}

// NewConvergence returns a tracker for one task.
func NewConvergence() *Convergence {
	return &Convergence{versions: map[string][]string{}, edits: map[string][]Edit{}}
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

// ObserveEdit records an exact replacement made in file. It returns the notice
// when the replacement undoes one of the last three edits to file: it puts back
// the old text of that edit and takes away its new text.
func (c *Convergence) ObserveEdit(file string, e Edit) string {
	hint := ""
	for _, prev := range c.edits[file] {
		if e.Old == prev.New && e.New == prev.Old {
			hint = convergenceHint
		}
	}
	v := append(c.edits[file], e)
	if len(v) > 3 {
		v = v[len(v)-3:]
	}
	c.edits[file] = v
	return hint
}
