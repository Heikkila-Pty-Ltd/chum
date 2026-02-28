package temporal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListSourceFiles(t *testing.T) {
	dir := t.TempDir()

	// Create source files
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "util.go"), []byte("package main"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# readme"), 0o644)) // not a source file

	// Create a vendor dir that should be skipped
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "vendor"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "vendor", "dep.go"), []byte("package dep"), 0o644))

	files := listSourceFiles(dir)
	require.Len(t, files, 2)
	require.Contains(t, files, "main.go")
	require.Contains(t, files, "util.go")
}

func TestListSourceFiles_NodeModulesSkipped(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "index.js"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.tsx"), []byte(""), 0o644))

	files := listSourceFiles(dir)
	require.Len(t, files, 1)
	require.Equal(t, "app.tsx", files[0])
}

func TestFormatBlastRadiusSection_Nil(t *testing.T) {
	require.Empty(t, FormatBlastRadiusSection(nil))
}

func TestFormatBlastRadiusSection_Empty(t *testing.T) {
	report := &BlastRadiusReport{Language: "go", TotalPkgs: 5}
	require.Empty(t, FormatBlastRadiusSection(report))
}

func TestFormatBlastRadiusSection_WithData(t *testing.T) {
	report := &BlastRadiusReport{
		Language:  "go",
		TotalPkgs: 10,
		TotalFiles: 42,
		HotFiles: []HotFile{
			{Path: "internal/store", ImportedBy: 8},
			{Path: "internal/config", ImportedBy: 5},
		},
		CircularDeps: []string{"a -> b -> a"},
	}
	section := FormatBlastRadiusSection(report)
	require.Contains(t, section, "internal/store")
	require.Contains(t, section, "imported by 8")
	require.Contains(t, section, "a -> b -> a")
	require.Contains(t, section, "10 packages")
}

func TestScanGoBlastRadius_NoGoProject(t *testing.T) {
	dir := t.TempDir()
	report := &BlastRadiusReport{}

	// No go.mod — go list will fail
	scanGoBlastRadius(t.Context(), dir, report)
	require.NotEmpty(t, report.ScanErrors)
}
