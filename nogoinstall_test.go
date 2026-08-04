package selfupdate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenCommand is assembled at runtime rather than written as a
// literal so this file does not itself contain the phrase it bans, and a
// repository-wide grep for the pattern stays clean.
var forbiddenCommand = "go" + " " + "install"

// TestNoGoInstallPath fails if a toolchain-install route reappears
// anywhere in the module's non-test sources.
//
// This library installs from release archives and nothing else. The
// reason is mechanical, not stylistic: a single `replace` directive in a
// consuming module makes a versioned module query impossible, so a
// "fallback" through the toolchain is dead code posing as a safety net —
// it cannot fire for exactly the projects that would need it. A comment
// saying so would not survive a future contributor's reasonable-looking
// patch; this test will.
//
// Comments are deliberately exempt. The design decision has to be
// explainable in prose, including in the doc comment you are reading.
func TestNoGoInstallPath(t *testing.T) {
	root := moduleRoot(t)

	files := nonTestGoFiles(t, root)
	if len(files) == 0 {
		t.Fatal("walked the module and found no non-test Go files; the guard would pass vacuously")
	}

	for _, path := range files {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}

		src, err := os.ReadFile(path) //nolint:gosec // path comes from walking this module's own tree
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}

		assertNoForbiddenText(t, rel, src)
		assertNoForbiddenExecArgs(t, rel, path, src)
	}
}

// assertNoForbiddenText reports the banned phrase anywhere in src that
// is not inside a comment.
func assertNoForbiddenText(t *testing.T, rel string, src []byte) {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}

	// Comment spans are exempt; everything else is code.
	type span struct{ start, end token.Pos }
	comments := make([]span, 0, len(file.Comments))
	for _, group := range file.Comments {
		comments = append(comments, span{group.Pos(), group.End()})
	}

	base := fset.File(file.Pos()).Base()
	lower := strings.ToLower(string(src))
	for offset := 0; ; {
		idx := strings.Index(lower[offset:], forbiddenCommand)
		if idx < 0 {
			return
		}
		at := token.Pos(base + offset + idx)
		offset += idx + len(forbiddenCommand)

		inComment := false
		for _, c := range comments {
			if at >= c.start && at < c.end {
				inComment = true
				break
			}
		}
		if !inComment {
			t.Errorf("%s: found a toolchain-install route at %s; this module installs from release archives only",
				rel, fset.Position(at))
		}
	}
}

// assertNoForbiddenExecArgs reports a call that passes the toolchain
// binary and the install subcommand as adjacent string arguments, the
// shape a text scan alone would miss.
func assertNoForbiddenExecArgs(t *testing.T, rel, path string, src []byte) {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		literals := make([]string, 0, len(call.Args))
		for _, arg := range call.Args {
			lit, isLit := arg.(*ast.BasicLit)
			if !isLit || lit.Kind != token.STRING {
				literals = append(literals, "")
				continue
			}
			literals = append(literals, strings.Trim(lit.Value, "`\""))
		}
		for i := 0; i+1 < len(literals); i++ {
			if literals[i] == "go" && literals[i+1] == "install" {
				t.Errorf("%s: call at %s shells out to a toolchain install; this module installs from release archives only",
					rel, fset.Position(call.Pos()))
			}
		}
		return true
	})
}

// moduleRoot walks up from the working directory to the directory
// holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("walked to the filesystem root without finding go.mod")
		}
		dir = parent
	}
}

// nonTestGoFiles returns every .go file in the module that is not a test
// file, skipping vendored and hidden directories.
func nonTestGoFiles(t *testing.T, root string) []string {
	t.Helper()

	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}
	return files
}
