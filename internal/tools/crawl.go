package tools

import (
	"fmt"
)

// The crawl guard of read_file (SPEC §6.3): a budget per file, counted in bytes of the
// text the model is sent. The guard never refuses a read. Past the budget, a read returns
// the first crawlThrottle characters of the range asked for, and a note.
const (
	crawlMultiplier = 2         // the budget is twice the file size,
	crawlFloor      = 64 << 10  // at least 64 KB,
	crawlCap        = 512 << 10 // and at most 512 KB
	crawlThrottle   = 2000      // characters a read returns once the budget is spent
	crawlMaxNudges  = 2         // one-line hints a file gets, at most
)

// crawlNudgeAt are the reads of a file that get a one-line hint (SPEC §6.3).
var crawlNudgeAt = [...]int{4, 10}

// crawlFile is what the task has read of one file.
type crawlFile struct {
	reads  int
	bytes  int
	nudges int
	warned bool
}

// ResetCrawl clears the crawl guard's counts. The loop calls it when a task starts, so
// that every task has its own budgets.
func (e *Env) ResetCrawl() {
	e.crawl = nil
}

// crawlBudget is the byte budget of a file of the given size.
func crawlBudget(size int) int {
	return min(max(crawlMultiplier*size, crawlFloor), crawlCap)
}

// crawlRead counts one read of a file of size bytes that returns body, and returns what the
// model sees. Past the budget it returns the first characters of the body, with the trailer
// that keeps the position of the rest, so that reading can continue.
func (e *Env) crawlRead(path string, size int, body, trailer string) string {
	if e.crawl == nil {
		e.crawl = map[string]*crawlFile{}
	}
	f := e.crawl[path]
	if f == nil {
		f = &crawlFile{}
		e.crawl[path] = f
	}
	f.reads++
	f.bytes += len(body)
	budget := crawlBudget(size)
	if f.bytes > budget {
		note := fmt.Sprintf("[crawl guard: %s is read at a limited rate, at most %d characters a read (%d of %d bytes).]\n",
			path, crawlThrottle, f.bytes, budget)
		if !f.warned {
			f.warned = true
			note = fmt.Sprintf("[crawl guard: this task has read %d bytes of %s, past its budget of %d bytes (twice the file size, between 64 KB and 512 KB). "+
				"Reads are never refused, but each read of this file now returns at most %d characters. To work through a large file, "+
				"run a script with run_shell that counts, searches or aggregates it once, and write the result to a file.]\n",
				f.bytes, path, budget, crawlThrottle)
		}
		return note + firstRunes(body, crawlThrottle) + trailer
	}
	for _, at := range crawlNudgeAt {
		if f.reads == at && f.nudges < crawlMaxNudges {
			f.nudges++
			return fmt.Sprintf("[hint: this is read %d of %s. For counting, searching or aggregating, one run_shell script is faster; "+
				"a plain re-read can be ignored.]\n", f.reads, path) + body + trailer
		}
	}
	return body + trailer
}

// firstRunes keeps the first n characters of s, never splitting one.
func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
