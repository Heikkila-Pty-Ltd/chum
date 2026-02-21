package temporal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func collectCommandError(target *[]string, cmd CommandResult) {
	if cmd.Error == "" {
		return
	}
	*target = append(*target, cmd.Error)
}

// stingrayMaxBufferBytes caps in-memory subprocess output to prevent OOM from
// pathological commands. 2x the truncation length so we capture enough context.
const stingrayMaxBufferBytes = 2 * stingrayCommandOutputTruncateLen

// limitedWriter wraps a bytes.Buffer and stops writing after maxBytes.
// Excess data is silently discarded to bound memory usage.
type limitedWriter struct {
	buf      bytes.Buffer
	maxBytes int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	n := len(p) // report full length to avoid breaking the subprocess
	remaining := w.maxBytes - w.buf.Len()
	if remaining <= 0 {
		return n, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	w.buf.Write(p)
	return n, nil
}

func (w *limitedWriter) String() string {
	return w.buf.String()
}

func runCommand(ctx context.Context, workDir string, timeout time.Duration, name string, args ...string) CommandResult {
	command := formatCommand(name, args...)
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(cmdCtx, name, args...)
	cmd.Dir = workDir

	stdout := &limitedWriter{maxBytes: stingrayMaxBufferBytes}
	stderr := &limitedWriter{maxBytes: stingrayMaxBufferBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	duration := time.Since(start)

	timedOut := cmdCtx.Err() == context.DeadlineExceeded
	if timedOut {
		if err == nil {
			err = cmdCtx.Err()
		}
	}

	exitCode := 0
	if err != nil {
		exitCode = -1
		var execErr *exec.ExitError
		if errors.As(err, &execErr) {
			exitCode = execErr.ExitCode()
		}
	}

	commandErr := ""
	if err != nil {
		commandErr = err.Error()
	}
	if timedOut {
		if commandErr == "" {
			commandErr = "command timed out"
		} else {
			commandErr = "command timed out: " + commandErr
		}
	}

	return CommandResult{
		Command:    command,
		ExitCode:   exitCode,
		DurationMs: duration.Milliseconds(),
		Succeeded:  err == nil && !timedOut,
		TimedOut:   timedOut,
		Stdout:     truncate(stdout.String(), stingrayCommandOutputTruncateLen),
		Stderr:     truncate(stderr.String(), stingrayCommandOutputTruncateLen),
		Error:      truncate(commandErr, stingrayCommandOutputTruncateLen),
	}
}

func formatCommand(name string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, name)
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}

func parseGolangCILintOutput(raw string) (count int, linters []string, issues []GolangCILintIssue, err error) {
	var payload struct {
		Issues []struct {
			FromLinter string `json:"FromLinter"`
			Text       string `json:"Text"`
			Pos        struct {
				Filename string `json:"Filename"`
				Line     int    `json:"Line"`
			} `json:"Pos"`
		} `json:"Issues"`
	}

	if strings.TrimSpace(raw) == "" {
		return 0, nil, nil, nil
	}
	if err = json.Unmarshal([]byte(raw), &payload); err != nil {
		return 0, nil, nil, err
	}

	seen := make(map[string]struct{})
	issues = make([]GolangCILintIssue, 0, len(payload.Issues))
	for _, issue := range payload.Issues {
		seen[issue.FromLinter] = struct{}{}
		issues = append(issues, GolangCILintIssue{
			Linter:  issue.FromLinter,
			Message: issue.Text,
			File:    issue.Pos.Filename,
			Line:    issue.Pos.Line,
		})
	}

	linters = make([]string, 0, len(seen))
	for l := range seen {
		linters = append(linters, l)
	}
	sort.Strings(linters)

	count = len(payload.Issues)
	return
}

func parseCoverageTotal(raw string) (percent float64, line string, err error) {
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		line = strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "total:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, "", fmt.Errorf("unexpected total line: %q", line)
		}
		percentStr := strings.TrimSuffix(fields[len(fields)-1], "%")
		percent, err = strconv.ParseFloat(percentStr, 64)
		if err != nil {
			return 0, "", fmt.Errorf("invalid coverage percentage %q: %w", fields[len(fields)-1], err)
		}
		return percent, line, nil
	}
	if err = sc.Err(); err != nil {
		return 0, "", err
	}
	return 0, "", fmt.Errorf("coverage total line not found")
}

func parseGoListOutdated(raw string) ([]OutdatedDependency, error) {
	outdated := make([]OutdatedDependency, 0)
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		dep := OutdatedDependency{
			Module:  parts[0],
			Current: parts[1],
		}
		if len(parts) >= 3 && strings.HasPrefix(parts[2], "[") && strings.HasSuffix(parts[2], "]") {
			dep.Latest = strings.Trim(parts[2], "[]")
		}
		if dep.Module == "all" {
			continue
		}
		outdated = append(outdated, dep)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return outdated, nil
}

func parseGoModGraph(raw string) []DepGraphEdge {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	sc := bufio.NewScanner(strings.NewReader(raw))
	edges := make([]DepGraphEdge, 0)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		// Strip version suffixes (module@v1.2.3 -> module) for cleaner graph.
		from := stripModVersion(parts[0])
		to := stripModVersion(parts[1])
		edges = append(edges, DepGraphEdge{From: from, To: to})
	}
	return edges
}

func stripModVersion(s string) string {
	if idx := strings.LastIndex(s, "@"); idx > 0 {
		return s[:idx]
	}
	return s
}

func parseTODOOutput(raw, baseDir string) []TODOHit {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	base := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(baseDir)), "/") + "/"

	sc := bufio.NewScanner(strings.NewReader(raw))
	hits := make([]TODOHit, 0)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		lineno, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		relPath := strings.TrimPrefix(filepath.ToSlash(parts[0]), base)
		text := strings.TrimSpace(parts[2])
		kind := detectTODOMarker(text)

		hits = append(hits, TODOHit{
			File: relPath,
			Line: lineno,
			Text: text,
			Kind: kind,
		})
	}
	return hits
}

func detectTODOMarker(line string) string {
	upper := strings.ToUpper(line)
	switch {
	case strings.Contains(upper, "TODO"):
		return "TODO"
	case strings.Contains(upper, "HACK"):
		return "HACK"
	case strings.Contains(upper, "FIXME"):
		return "FIXME"
	case strings.Contains(upper, "WORKAROUND"):
		return "WORKAROUND"
	default:
		return "TODO"
	}
}

func collectRawFileMetrics(workDir, modulePath string) ([]RawFileMetric, []PackageMetric, []string) {
	packages := make(map[string]*PackageMetric)
	fileMetrics := make([]RawFileMetric, 0)
	fanOut := make(map[string]map[string]struct{})
	errors := make([]string, 0)

	err := filepath.WalkDir(workDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			errors = append(errors, walkErr.Error())
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if shouldSkipDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(name) != ".go" {
			return nil
		}

		relPath, err := filepath.Rel(workDir, path)
		if err != nil {
			errors = append(errors, err.Error())
			return nil
		}
		relPath = filepath.ToSlash(relPath)

		content, readErr := os.ReadFile(path)
		if readErr != nil {
			errors = append(errors, fmt.Sprintf("read %s: %v", relPath, readErr))
			return nil
		}

		fset := token.NewFileSet()
		fileAst, parseErr := parser.ParseFile(fset, path, content, parser.AllErrors|parser.ParseComments)
		if parseErr != nil {
			errors = append(errors, fmt.Sprintf("parse %s: %v", relPath, parseErr))
		}

		metric := RawFileMetric{
			File:     relPath,
			LOC:      bytesCountLines(content),
			NonBlank: countNonBlankLines(content),
		}
		if fileAst != nil {
			metric.Package = fileAst.Name.Name
		}

		if fileAst == nil {
			fileMetrics = append(fileMetrics, metric)
			return nil
		}

		metric.Imports = len(fileAst.Imports)
		packageName := packagePathForDirectory(modulePath, filepath.ToSlash(filepath.Dir(relPath)))
		metric.Package = packageName

		packageMetric := ensurePackageMetric(packages, packageName, filepath.ToSlash(filepath.Dir(relPath)))
		packageMetric.Files++
		packageMetric.LOC += metric.LOC
		packageMetric.NonBlank += metric.NonBlank

		localImports := make(map[string]struct{})
		for _, decl := range fileAst.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			metric.Functions++
			packageMetric.Functions++
			if fn.Recv != nil && len(fn.Recv.List) > 0 {
				metric.Methods++
				packageMetric.Methods++
			}
		}

		for _, decl := range fileAst.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				if _, ok := spec.(*ast.TypeSpec); ok {
					metric.Types++
					packageMetric.Types++
				}
			}
		}

		for _, imp := range fileAst.Imports {
			importPath := strings.Trim(imp.Path.Value, "\"")
			packageMetric.Imports++
			if localPath, isLocal := localPackageImport(modulePath, importPath); isLocal {
				localImports[localPath] = struct{}{}
				metric.LocalImports++
			}
		}

		if len(localImports) > 0 {
			if _, ok := fanOut[packageName]; !ok {
				fanOut[packageName] = make(map[string]struct{})
			}
			for target := range localImports {
				fanOut[packageName][target] = struct{}{}
			}
		}

		fileMetrics = append(fileMetrics, metric)
		return nil
	})

	if err != nil {
		errors = append(errors, err.Error())
	}

	fanIn := make(map[string]map[string]struct{})
	for source, targets := range fanOut {
		for target := range targets {
			if _, ok := packages[target]; !ok {
				continue
			}
			if _, ok := fanIn[target]; !ok {
				fanIn[target] = make(map[string]struct{})
			}
			fanIn[target][source] = struct{}{}
		}
	}

	for pkgName, pm := range packages {
		if targets, ok := fanOut[pkgName]; ok {
			pm.FanOut = len(targets)
		}
		if sources, ok := fanIn[pkgName]; ok {
			pm.FanIn = len(sources)
		}
	}

	packageList := make([]PackageMetric, 0, len(packages))
	for _, pm := range packages {
		packageList = append(packageList, *pm)
	}
	sort.Slice(packageList, func(i, j int) bool { return packageList[i].Package < packageList[j].Package })

	sort.Slice(fileMetrics, func(i, j int) bool { return fileMetrics[i].File < fileMetrics[j].File })

	return fileMetrics, packageList, errors
}

func ensurePackageMetric(packages map[string]*PackageMetric, pkgName, dir string) *PackageMetric {
	pm, ok := packages[pkgName]
	if ok {
		return pm
	}
	pm = &PackageMetric{
		Package: pkgName,
		Dir:     dir,
	}
	packages[pkgName] = pm
	return pm
}

func packagePathForDirectory(modulePath, relDir string) string {
	relDir = filepath.ToSlash(relDir)
	if relDir == "." || relDir == "" {
		if modulePath == "" {
			return "."
		}
		return modulePath
	}
	if modulePath == "" {
		return relDir
	}
	return modulePath + "/" + relDir
}

func localPackageImport(modulePath, importPath string) (string, bool) {
	if modulePath == "" {
		return "", false
	}
	if importPath == modulePath {
		return modulePath, true
	}
	if strings.HasPrefix(importPath, modulePath+"/") {
		return importPath, true
	}
	if strings.HasPrefix(importPath, "./") || strings.HasPrefix(importPath, "../") {
		return "", false
	}
	return "", false
}

func shouldSkipDir(name string) bool {
	if name == ".git" || name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".") {
		return true
	}
	return false
}

func bytesCountLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	return bytes.Count(data, []byte{'\n'}) + 1
}

func countNonBlankLines(data []byte) int {
	count := 0
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			count++
		}
	}
	return count
}

func readGoModulePath(workDir string) (string, error) {
	modPath := filepath.Join(workDir, "go.mod")
	data, err := os.ReadFile(modPath)
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			return fields[1], nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("module directive not found in go.mod")
}
