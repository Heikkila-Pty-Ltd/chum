package temporal

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCoverageTotal(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantPercent float64
		wantLine    string
		wantErr     bool
	}{
		{
			name: "standard coverage output",
			input: `github.com/example/pkg/foo.go:	DoThing	66.7%
github.com/example/pkg/bar.go:	Other	100.0%
total:	(statements)	72.5%`,
			wantPercent: 72.5,
			wantLine:    "total:\t(statements)\t72.5%",
		},
		{
			name:        "zero coverage",
			input:       "total:\t(statements)\t0.0%",
			wantPercent: 0.0,
			wantLine:    "total:\t(statements)\t0.0%",
		},
		{
			name:        "100 percent coverage",
			input:       "total:\t(statements)\t100.0%",
			wantPercent: 100.0,
			wantLine:    "total:\t(statements)\t100.0%",
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
		{
			name:    "no total line",
			input:   "github.com/example/pkg/foo.go:\tDoThing\t66.7%",
			wantErr: true,
		},
		{
			name:    "invalid percentage",
			input:   "total:\t(statements)\tnotanumber%",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pct, line, err := parseCoverageTotal(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.InDelta(t, tt.wantPercent, pct, 0.01)
			assert.Equal(t, tt.wantLine, line)
		})
	}
}

func TestParseGoListOutdated(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantLen int
		wantErr bool
	}{
		{
			name:    "empty input",
			input:   "",
			wantLen: 0,
		},
		{
			name: "no outdated deps",
			input: `github.com/example/foo v1.2.3
github.com/example/bar v0.1.0`,
			wantLen: 2,
		},
		{
			name: "with outdated dep",
			input: `github.com/example/foo v1.2.3 [v1.3.0]
github.com/example/bar v0.1.0`,
			wantLen: 2,
		},
		{
			name:    "skips line with module=all",
			input:   "all v0.0.0",
			wantLen: 0,
		},
		{
			name:    "skips single-field lines",
			input:   "something",
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps, err := parseGoListOutdated(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Len(t, deps, tt.wantLen)
		})
	}

	// Verify outdated version is parsed
	t.Run("outdated version extracted", func(t *testing.T) {
		deps, err := parseGoListOutdated("github.com/example/foo v1.0.0 [v2.0.0]")
		require.NoError(t, err)
		require.Len(t, deps, 1)
		assert.Equal(t, "github.com/example/foo", deps[0].Module)
		assert.Equal(t, "v1.0.0", deps[0].Current)
		assert.Equal(t, "v2.0.0", deps[0].Latest)
	})

	// Verify non-outdated has empty Latest
	t.Run("current dep has empty latest", func(t *testing.T) {
		deps, err := parseGoListOutdated("github.com/example/foo v1.0.0")
		require.NoError(t, err)
		require.Len(t, deps, 1)
		assert.Equal(t, "", deps[0].Latest)
	})
}

func TestParseTODOOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		baseDir  string
		wantLen  int
		wantHits []TODOHit
	}{
		{
			name:    "empty input",
			input:   "",
			baseDir: "/project",
			wantLen: 0,
		},
		{
			name:    "single TODO with absolute path",
			input:   "/project/internal/foo.go:42:// TODO: fix this",
			baseDir: "/project",
			wantLen: 1,
			wantHits: []TODOHit{
				{File: "internal/foo.go", Line: 42, Text: "// TODO: fix this", Kind: "TODO"},
			},
		},
		{
			name:    "HACK marker relative path",
			input:   "./internal/bar.go:10:// HACK: workaround for bug",
			baseDir: "/project",
			wantLen: 1,
			wantHits: []TODOHit{
				{File: "./internal/bar.go", Line: 10, Text: "// HACK: workaround for bug", Kind: "HACK"},
			},
		},
		{
			name:    "FIXME marker absolute path stripped",
			input:   "/project/pkg/baz.go:5:// FIXME: this breaks on edge case",
			baseDir: "/project",
			wantLen: 1,
			wantHits: []TODOHit{
				{File: "pkg/baz.go", Line: 5, Text: "// FIXME: this breaks on edge case", Kind: "FIXME"},
			},
		},
		{
			name:    "WORKAROUND marker",
			input:   "/project/main.go:1:// WORKAROUND: upstream bug #123",
			baseDir: "/project",
			wantLen: 1,
			wantHits: []TODOHit{
				{File: "main.go", Line: 1, Text: "// WORKAROUND: upstream bug #123", Kind: "WORKAROUND"},
			},
		},
		{
			name: "multiple hits",
			input: `./a.go:1:// TODO: first
./b.go:2:// HACK: second`,
			baseDir: "/project",
			wantLen: 2,
		},
		{
			name:    "invalid line number ignored",
			input:   "./foo.go:abc:// TODO: bad",
			baseDir: "/project",
			wantLen: 0,
		},
		{
			name:    "wrong format (too few colons) ignored",
			input:   "./foo.go:123",
			baseDir: "/project",
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits := parseTODOOutput(tt.input, tt.baseDir)
			assert.Len(t, hits, tt.wantLen)
			if tt.wantHits != nil {
				for i, want := range tt.wantHits {
					assert.Equal(t, want.File, hits[i].File, "File mismatch at %d", i)
					assert.Equal(t, want.Line, hits[i].Line, "Line mismatch at %d", i)
					assert.Equal(t, want.Text, hits[i].Text, "Text mismatch at %d", i)
					assert.Equal(t, want.Kind, hits[i].Kind, "Kind mismatch at %d", i)
				}
			}
		})
	}
}

func TestParseGolangCILintOutput(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantCount  int
		wantErr    bool
		wantLinter string
	}{
		{
			name:      "empty input",
			input:     "",
			wantCount: 0,
		},
		{
			name:      "whitespace only",
			input:     "   \n  ",
			wantCount: 0,
		},
		{
			name:    "invalid JSON",
			input:   "not json at all",
			wantErr: true,
		},
		{
			name:      "empty issues array",
			input:     `{"Issues": []}`,
			wantCount: 0,
		},
		{
			name: "single issue",
			input: `{"Issues": [{"FromLinter": "govet", "Text": "shadow: declaration of err", "Pos": {"Filename": "main.go", "Line": 42}}]}`,
			wantCount:  1,
			wantLinter: "govet",
		},
		{
			name: "multiple linters",
			input: `{"Issues": [
				{"FromLinter": "govet", "Text": "msg1", "Pos": {"Filename": "a.go", "Line": 1}},
				{"FromLinter": "errcheck", "Text": "msg2", "Pos": {"Filename": "b.go", "Line": 2}},
				{"FromLinter": "govet", "Text": "msg3", "Pos": {"Filename": "c.go", "Line": 3}}
			]}`,
			wantCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, linters, issues, err := parseGolangCILintOutput(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantCount, count)
			if tt.wantLinter != "" {
				require.Len(t, issues, 1)
				assert.Equal(t, tt.wantLinter, issues[0].Linter)
				assert.Contains(t, linters, tt.wantLinter)
			}
		})
	}

	t.Run("linters are deduplicated and sorted", func(t *testing.T) {
		input := `{"Issues": [
			{"FromLinter": "govet", "Text": "a", "Pos": {"Filename": "a.go", "Line": 1}},
			{"FromLinter": "errcheck", "Text": "b", "Pos": {"Filename": "b.go", "Line": 2}},
			{"FromLinter": "govet", "Text": "c", "Pos": {"Filename": "c.go", "Line": 3}}
		]}`
		_, linters, _, err := parseGolangCILintOutput(input)
		require.NoError(t, err)
		assert.Equal(t, []string{"errcheck", "govet"}, linters)
	})
}

func TestParseGoModGraph(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantLen int
	}{
		{
			name:    "empty input",
			input:   "",
			wantLen: 0,
		},
		{
			name:    "whitespace only",
			input:   "   \n  ",
			wantLen: 0,
		},
		{
			name:    "single edge",
			input:   "github.com/example/root@v0.0.0 github.com/example/dep@v1.2.3",
			wantLen: 1,
		},
		{
			name: "multiple edges",
			input: `github.com/root@v0.0.0 github.com/dep-a@v1.0.0
github.com/root@v0.0.0 github.com/dep-b@v2.0.0
github.com/dep-a@v1.0.0 github.com/dep-c@v0.5.0`,
			wantLen: 3,
		},
		{
			name:    "malformed line (single field) skipped",
			input:   "github.com/root@v0.0.0",
			wantLen: 0,
		},
		{
			name:    "malformed line (three fields) skipped",
			input:   "a@v1 b@v2 c@v3",
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			edges := parseGoModGraph(tt.input)
			assert.Len(t, edges, tt.wantLen)
		})
	}

	t.Run("versions are stripped", func(t *testing.T) {
		edges := parseGoModGraph("github.com/root@v0.0.0 github.com/dep@v1.2.3")
		require.Len(t, edges, 1)
		assert.Equal(t, "github.com/root", edges[0].From)
		assert.Equal(t, "github.com/dep", edges[0].To)
	})

	t.Run("module without version preserved", func(t *testing.T) {
		edges := parseGoModGraph("mymodule github.com/dep@v1.0.0")
		require.Len(t, edges, 1)
		assert.Equal(t, "mymodule", edges[0].From)
		assert.Equal(t, "github.com/dep", edges[0].To)
	})
}

func TestStripModVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"github.com/foo@v1.2.3", "github.com/foo"},
		{"github.com/foo/bar@v0.0.0-20230101120000-abcdef123456", "github.com/foo/bar"},
		{"mymodule", "mymodule"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, stripModVersion(tt.input))
		})
	}
}

func TestDetectTODOMarker(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"// TODO: fix this", "TODO"},
		{"// HACK: workaround", "HACK"},
		{"// FIXME: broken", "FIXME"},
		{"// WORKAROUND: upstream bug", "WORKAROUND"},
		{"// todo: lowercase", "TODO"},
		{"// some random comment", "TODO"}, // default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, detectTODOMarker(tt.input))
		})
	}
}

func TestCollectRawFileMetrics(t *testing.T) {
	// Use a known subdirectory with Go files for a reliable test.
	// internal/temporal itself has .go files we can parse.
	workDir := t.TempDir()

	// Write a minimal Go file to parse
	goFile := `package example

import "fmt"

type Foo struct{}

func (f *Foo) Bar() { fmt.Println("hello") }
func Baz() {}
`
	require.NoError(t, os.MkdirAll(workDir, 0o755))
	require.NoError(t, os.WriteFile(workDir+"/example.go", []byte(goFile), 0o644))

	files, packages, errs := collectRawFileMetrics(workDir, "github.com/test/example")
	assert.Empty(t, errs)
	require.Len(t, files, 1)
	assert.Equal(t, "example.go", files[0].File)
	assert.Equal(t, "github.com/test/example", files[0].Package)
	assert.Equal(t, 2, files[0].Functions) // Bar + Baz
	assert.Equal(t, 1, files[0].Methods)   // Bar is a method
	assert.Equal(t, 1, files[0].Types)     // Foo
	assert.Equal(t, 1, files[0].Imports)   // "fmt"

	require.Len(t, packages, 1)
	assert.Equal(t, "github.com/test/example", packages[0].Package)
	assert.Equal(t, 1, packages[0].Files)
}

func TestCollectRawFileMetrics_InvalidDir(t *testing.T) {
	files, packages, errs := collectRawFileMetrics("/nonexistent/path", "")
	assert.Empty(t, files)
	assert.Empty(t, packages)
	assert.NotEmpty(t, errs, "expected error for nonexistent directory")
}

func TestBytesCountLines(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  int
	}{
		{"empty", []byte{}, 0},
		{"single line no newline", []byte("hello"), 1},
		{"single line with newline", []byte("hello\n"), 2},
		{"two lines", []byte("hello\nworld"), 2},
		{"three lines", []byte("a\nb\nc\n"), 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, bytesCountLines(tt.input))
		})
	}
}

func TestCountNonBlankLines(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  int
	}{
		{"empty", []byte{}, 0},
		{"all blank", []byte("\n\n  \n"), 0},
		{"mixed", []byte("hello\n\nworld\n  \n"), 2},
		{"no blanks", []byte("a\nb\nc"), 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, countNonBlankLines(tt.input))
		})
	}
}

func TestFormatCommand(t *testing.T) {
	assert.Equal(t, "go vet ./...", formatCommand("go", "vet", "./..."))
	assert.Equal(t, "grep -RInE pattern .", formatCommand("grep", "-RInE", "pattern", "."))
	assert.Equal(t, "ls", formatCommand("ls"))
}

func TestCollectCommandError(t *testing.T) {
	t.Run("no error skips", func(t *testing.T) {
		var errs []string
		collectCommandError(&errs, CommandResult{Error: ""})
		assert.Empty(t, errs)
	})

	t.Run("error appended", func(t *testing.T) {
		var errs []string
		collectCommandError(&errs, CommandResult{Error: "something failed"})
		require.Len(t, errs, 1)
		assert.Equal(t, "something failed", errs[0])
	})
}

func TestLimitedWriter(t *testing.T) {
	t.Run("under limit", func(t *testing.T) {
		w := &limitedWriter{maxBytes: 100}
		n, err := w.Write([]byte("hello"))
		assert.NoError(t, err)
		assert.Equal(t, 5, n)
		assert.Equal(t, "hello", w.String())
	})

	t.Run("at limit", func(t *testing.T) {
		w := &limitedWriter{maxBytes: 5}
		n, err := w.Write([]byte("hello"))
		assert.NoError(t, err)
		assert.Equal(t, 5, n)
		assert.Equal(t, "hello", w.String())
	})

	t.Run("over limit truncates", func(t *testing.T) {
		w := &limitedWriter{maxBytes: 5}
		n, err := w.Write([]byte("hello world"))
		assert.NoError(t, err)
		assert.Equal(t, 11, n) // reports full length written
		assert.Equal(t, "hello", w.String())
	})

	t.Run("multiple writes respect limit", func(t *testing.T) {
		w := &limitedWriter{maxBytes: 10}
		w.Write([]byte("12345"))
		w.Write([]byte("67890"))
		n, err := w.Write([]byte("overflow"))
		assert.NoError(t, err)
		assert.Equal(t, 8, n) // reports success
		assert.Equal(t, "1234567890", w.String())
	})

	t.Run("zero max bytes discards everything", func(t *testing.T) {
		w := &limitedWriter{maxBytes: 0}
		n, err := w.Write([]byte("data"))
		assert.NoError(t, err)
		assert.Equal(t, 4, n)
		assert.Equal(t, "", w.String())
	})
}

func TestLimitedWriterLargeInput(t *testing.T) {
	// Simulate a pathological subprocess producing large output
	w := &limitedWriter{maxBytes: 1000}
	chunk := bytes.Repeat([]byte("x"), 500)
	for i := 0; i < 100; i++ {
		w.Write(chunk)
	}
	assert.Equal(t, 1000, len(w.String()), "buffer should be capped at maxBytes")
}

func TestShouldSkipDir(t *testing.T) {
	assert.True(t, shouldSkipDir(".git"))
	assert.True(t, shouldSkipDir("vendor"))
	assert.True(t, shouldSkipDir("node_modules"))
	assert.True(t, shouldSkipDir(".idea"))
	assert.False(t, shouldSkipDir("internal"))
	assert.False(t, shouldSkipDir("cmd"))
}

func TestPackagePathForDirectory(t *testing.T) {
	tests := []struct {
		modulePath string
		relDir     string
		want       string
	}{
		{"github.com/example/foo", ".", "github.com/example/foo"},
		{"github.com/example/foo", "", "github.com/example/foo"},
		{"github.com/example/foo", "internal/bar", "github.com/example/foo/internal/bar"},
		{"", ".", "."},
		{"", "internal/bar", "internal/bar"},
	}

	for _, tt := range tests {
		t.Run(tt.modulePath+"/"+tt.relDir, func(t *testing.T) {
			assert.Equal(t, tt.want, packagePathForDirectory(tt.modulePath, tt.relDir))
		})
	}
}

func TestLocalPackageImport(t *testing.T) {
	tests := []struct {
		name       string
		modulePath string
		importPath string
		wantPath   string
		wantLocal  bool
	}{
		{"exact module match", "github.com/example/foo", "github.com/example/foo", "github.com/example/foo", true},
		{"sub-package", "github.com/example/foo", "github.com/example/foo/bar", "github.com/example/foo/bar", true},
		{"external package", "github.com/example/foo", "github.com/other/pkg", "", false},
		{"relative import", "github.com/example/foo", "./bar", "", false},
		{"empty module", "", "anything", "", false},
		{"stdlib", "github.com/example/foo", "fmt", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, isLocal := localPackageImport(tt.modulePath, tt.importPath)
			assert.Equal(t, tt.wantLocal, isLocal)
			assert.Equal(t, tt.wantPath, path)
		})
	}
}

func TestReadGoModulePath(t *testing.T) {
	// Use the actual project go.mod
	modulePath, err := readGoModulePath("../../")
	require.NoError(t, err)
	assert.Equal(t, "github.com/antigravity-dev/chum", modulePath)
}

func TestReadGoModulePath_InvalidDir(t *testing.T) {
	_, err := readGoModulePath("/nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "go.mod")
}

func TestParseTODOOutputPathNormalization(t *testing.T) {
	// grep output with baseDir prefix should be stripped
	input := "/project/internal/foo.go:10:// TODO: test"
	hits := parseTODOOutput(input, "/project")
	require.Len(t, hits, 1)
	assert.Equal(t, "internal/foo.go", hits[0].File)
}

func TestParseGolangCILintOutputIssueFields(t *testing.T) {
	input := `{"Issues": [{"FromLinter": "govet", "Text": "shadow: x", "Pos": {"Filename": "main.go", "Line": 42}}]}`
	count, _, issues, err := parseGolangCILintOutput(input)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	require.Len(t, issues, 1)
	assert.Equal(t, "govet", issues[0].Linter)
	assert.Equal(t, "shadow: x", issues[0].Message)
	assert.Equal(t, "main.go", issues[0].File)
	assert.Equal(t, 42, issues[0].Line)
}

func TestParseGoModGraphVersionStripping(t *testing.T) {
	// Verify complex pseudo-versions are stripped correctly
	input := "github.com/root@v0.0.0-20230101120000-abcdef123456 github.com/dep@v1.2.3-rc.1"
	edges := parseGoModGraph(input)
	require.Len(t, edges, 1)
	assert.Equal(t, "github.com/root", edges[0].From)
	assert.Equal(t, "github.com/dep", edges[0].To)
}

func TestParseGoListOutdatedMultipleEntries(t *testing.T) {
	input := strings.Join([]string{
		"github.com/foo/a v1.0.0 [v1.1.0]",
		"github.com/foo/b v2.0.0",
		"github.com/foo/c v0.1.0 [v0.2.0]",
	}, "\n")
	deps, err := parseGoListOutdated(input)
	require.NoError(t, err)
	require.Len(t, deps, 3)

	// Count outdated
	outdated := 0
	for _, d := range deps {
		if d.Latest != "" {
			outdated++
		}
	}
	assert.Equal(t, 2, outdated)
}
