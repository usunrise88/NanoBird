// Command langcheck enforces English-only text (SPEC §5.13). It fails when a Go
// string literal, or a golden transcript, contains a letter outside the Latin
// script. Comments are not checked: the rule covers what the harness writes for
// the model and for the user.
//
//	go run ./tools/langcheck
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"unicode"
)

// allowed lists non-Latin letters that a fixture may need. SPEC §1 and §5.13
// write τ in benchmark names, so τ stays allowed.
var allowed = map[rune]bool{'τ': true}

// fixtureFiles lists the files whose job is to hold text in other scripts. They
// are skipped, and each one is named here so the exception stays visible.
var fixtureFiles = map[string]bool{
	"tools/langcheck/main_test.go": true,
}

func main() {
	var problems []string
	for _, root := range []string{"cmd", "internal", "tools"} {
		problems = append(problems, checkGoFiles(root)...)
	}
	problems = append(problems, checkGoldenFiles("testdata/golden")...)
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, p)
	}
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "langcheck: %d problem(s); NanoBird text is English only (SPEC §5.13)\n", len(problems))
		os.Exit(1)
	}
	fmt.Println("langcheck: ok")
}

// checkGoFiles parses every Go file under root and checks its string literals.
func checkGoFiles(root string) []string {
	var problems []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".go" || fixtureFiles[filepath.ToSlash(path)] {
			return err
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if bad, ok := nonLatin(value); ok {
				problems = append(problems, fmt.Sprintf("%s: string literal has the non-Latin letter %q", fset.Position(lit.Pos()), bad))
			}
			return true
		})
		return nil
	})
	if err != nil {
		problems = append(problems, fmt.Sprintf("%s: %v", root, err))
	}
	return problems
}

// checkGoldenFiles checks every file of the golden transcripts.
func checkGoldenFiles(root string) []string {
	var problems []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bad, ok := nonLatin(string(data)); ok {
			problems = append(problems, fmt.Sprintf("%s: golden file has the non-Latin letter %q", path, bad))
		}
		return nil
	})
	if err != nil {
		problems = append(problems, fmt.Sprintf("%s: %v", root, err))
	}
	return problems
}

// nonLatin returns the first letter of s outside the Latin script. Punctuation
// and symbols are allowed, as SPEC §5.13 says.
func nonLatin(s string) (rune, bool) {
	for _, r := range s {
		if unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r) && !allowed[r] {
			return r, true
		}
	}
	return 0, false
}
