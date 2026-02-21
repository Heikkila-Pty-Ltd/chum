// Package temporal implements the core CHUM Temporal workflows and activities.

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

	"go.temporal.io/sdk/activity"
)

const (
	stingrayTimeoutGoVet             = 2 * time.Minute
	stingrayTimeoutGolangCILint      = 2 * time.Minute
	stingrayTimeoutGoTestCoverage    = 4 * time.Minute
	stingrayTimeoutGoCover           = 90 * time.Second
	stingrayTimeoutGoListOutdated    = 2 * time.Minute
	stingrayTimeoutTODOs             = 90 * time.Second
	stingrayTimeoutGoModGraph        = 90 * time.Second
	stingrayCommandOutputTruncateLen  = 5000
	stingrayPrefix                   = "\033[33m🦂 STINGRAY\033[0m"
)

type StingrayMetricsRequest struct {
	Project string `json:"project"`
	WorkDir string `json:"work_dir"`
}

type CommandResult struct {
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	Succeeded  bool   `json:"succeeded"`
	TimedOut   bool   `json:"timed_out"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	Error      string `json:"error,omitempty"`
}

type RawFileMetric struct {
	File         string `json:"file"`
	Package      string `json:"package"`
	LOC          int    `json:"loc"`
	NonBlank     int    `json:"non_blank"`
	Functions    int    `json:"functions"`
	Methods      int    `json:"methods"`
	Types        int    `json:"types"`
	Imports      int    `json:"imports"`
	LocalImports int    `json:"local_imports"`
}

type PackageMetric struct {
	Package  string `json:"package"`
	Dir      string `json:"dir"`
	Files    int    `json:"files"`
	LOC      int    `json:"loc"`
	NonBlank int    `json:"non_blank"`
	Functions int    `json:"functions"`
	Methods  int    `json:"methods"`
	Types    int    `json:"types"`
	Imports  int    `json:"imports"`
	FanIn    int    `json:"fan_in"`
	FanOut   int    `json:"fan_out"`
}

type TODOHit struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
	Kind string `json:"kind"`
}

type TODOScanResult struct {
	Command   CommandResult `json:"command"`
	Hits      []TODOHit     `json:"hits"`
	HitCount  int           `json:"hit_count"`
}

type CoverageResult struct {
	Test            CommandResult `json:"test"`
	Report          CommandResult `json:"report"`
	TotalPercent    float64       `json:"total_percent"`
	TotalLine       string        `json:"total_line"`
	ParseError      string        `json:"parse_error,omitempty"`
}

type OutdatedDependency struct {
	Module  string `json:"module"`
	Current string `json:"current"`
	Latest  string `json:"latest"`
}

type OutdatedDependenciesResult struct {
	Command       CommandResult       `json:"command"`
	Dependencies  []OutdatedDependency `json:"dependencies"`
	OutdatedCount int                `json:"outdated_count"`
	ParseError    string             `json:"parse_error,omitempty"`
}

type GolangCILintIssue struct {
	Linter   string `json:"linter"`
	Message  string `json:"message"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

type GolangCILintResult struct {
	Command    CommandResult       `json:"command"`
	IssueCount int                 `json:"issue_count"`
	Linters    []string            `json:"linters"`
	Issues     []GolangCILintIssue `json:"issues"`
	ParseError string              `json:"parse_error,omitempty"`
}

type DepGraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type DepGraphResult struct {
	Command   CommandResult  `json:"command"`
	Edges     []DepGraphEdge `json:"edges"`
	EdgeCount int            `json:"edge_count"`
}

type StingrayMetrics struct {
	Project       string                   `json:"project"`
	WorkDir       string                   `json:"work_dir"`
	GatheredAtUTC string                   `json:"gathered_at_utc"`
	GoVet         CommandResult            `json:"go_vet"`
	GolangCILint  GolangCILintResult       `json:"golangci_lint"`
	Coverage      CoverageResult           `json:"coverage"`
	OutdatedDeps  OutdatedDependenciesResult `json:"outdated_deps"`
	ToDos         TODOScanResult           `json:"todos"`
	DepGraph      DepGraphResult           `json:"dep_graph"`
	FileMetrics   []RawFileMetric          `json:"file_metrics"`
	Packages      []PackageMetric          `json:"package_metrics"`
	Errors        []string                 `json:"errors"`
}

// GatherMetricsActivity runs static analysis and file-metrics collection for Stingray.
func (a *Activities) GatherMetricsActivity(ctx context.Context, req StingrayMetricsRequest) (*StingrayMetrics, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(stingrayPrefix+" Gathering code health metrics", "Project", req.Project, "WorkDir", req.WorkDir)

	workDir := strings.TrimSpace(req.WorkDir)
	if workDir == "" {
		return nil, fmt.Errorf("work_dir is required")
	}
	workDir = filepath.Clean(workDir)

	if _, err := os.Stat(workDir); err != nil {
		return nil, fmt.Errorf("invalid work_dir %q: %w", workDir, err)
	}

	project := strings.TrimSpace(req.Project)
	if project == "" {
		project = "unknown"
	}

	result := &StingrayMetrics{
		Project:       project,
		WorkDir:       workDir,
		GatheredAtUTC: time.Now().UTC().Format(time.RFC3339),
	}

	modulePath, moduleErr := readGoModulePath(workDir)
	if moduleErr != nil {
		result.Errors = append(result.Errors, moduleErr.Error())
	}

	result.GoVet = runCommand(
		ctx,
		workDir,
		stingrayTimeoutGoVet,
		"go",
		"vet",
		"./...",
	)
	collectCommandError(&result.Errors, result.GoVet)

	// golangci-lint requires at least one source file set and a working binary in PATH.
	lintCmd := runCommand(
		ctx,
		workDir,
		stingrayTimeoutGolangCILint,
		"golangci-lint",
		"run",
		"--out-format",
		"json",
		"./...",
	)
	lintOut := lintCmd.Stdout
	if strings.TrimSpace(lintOut) == "" {
		lintOut = lintCmd.Stderr
	}
	issueCount, linters, issues, parseErr := parseGolangCILintOutput(lintOut)
	result.GolangCILint = GolangCILintResult{
		Command:    lintCmd,
		IssueCount: issueCount,
		Linters:    linters,
		Issues:     issues,
	}
	if parseErr != nil {
		result.GolangCILint.ParseError = parseErr.Error()
		result.Errors = append(result.Errors, parseErr.Error())
	}
	collectCommandError(&result.Errors, result.GolangCILint.Command)

	coverageProfile, covErr := os.CreateTemp("", "stingray-coverage-*.out")
	if covErr != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("create coverage profile: %v", covErr))
	} else {
		profilePath := coverageProfile.Name()
		if closeErr := coverageProfile.Close(); closeErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("close coverage profile temp file: %v", closeErr))
		}

		result.Coverage.Test = runCommand(
			ctx,
			workDir,
			stingrayTimeoutGoTestCoverage,
			"go",
			"test",
			"-coverprofile="+profilePath,
			"./...",
		)
		collectCommandError(&result.Errors, result.Coverage.Test)

		result.Coverage.Report = runCommand(
			ctx,
			workDir,
			stingrayTimeoutGoCover,
			"go",
			"tool",
			"cover",
			"-func="+profilePath,
		)
		if covPercent, totalLine, parseCoverageErr := parseCoverageTotal(result.Coverage.Report.Stdout); parseCoverageErr == nil {
			result.Coverage.TotalPercent = covPercent
			result.Coverage.TotalLine = totalLine
		} else {
			result.Coverage.ParseError = parseCoverageErr.Error()
			result.Errors = append(result.Errors, parseCoverageErr.Error())
		}
		collectCommandError(&result.Errors, result.Coverage.Report)

		if rmErr := os.Remove(profilePath); rmErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("remove coverage profile: %v", rmErr))
		}
	}

	result.OutdatedDeps.Command = runCommand(
		ctx,
		workDir,
		stingrayTimeoutGoListOutdated,
		"go",
		"list",
		"-m",
		"-u",
		"all",
	)
	deps, depErr := parseGoListOutdated(result.OutdatedDeps.Command.Stdout + "\n" + result.OutdatedDeps.Command.Stderr)
	if depErr != nil {
		result.OutdatedDeps.ParseError = depErr.Error()
		result.Errors = append(result.Errors, depErr.Error())
	} else {
		result.OutdatedDeps.Dependencies = deps
		outdated := 0
		for _, d := range deps {
			if d.Latest != "" {
				outdated++
			}
		}
		result.OutdatedDeps.OutdatedCount = outdated
	}
	collectCommandError(&result.Errors, result.OutdatedDeps.Command)

	result.ToDos.Command = runCommand(
		ctx,
		workDir,
		stingrayTimeoutTODOs,
		"grep",
		"-RInE",
		"--include=*.go",
		"--exclude-dir=.git",
		"--exclude-dir=.idea",
		"--exclude-dir=vendor",
		"TODO|HACK|FIXME|WORKAROUND",
		".",
	)
	result.ToDos.Hits = parseTODOOutput(result.ToDos.Command.Stdout+"\n"+result.ToDos.Command.Stderr, workDir)
	result.ToDos.HitCount = len(result.ToDos.Hits)
	// grep exits 1 when no matches found — that's not an error, just zero TODOs.
	if !(result.ToDos.Command.ExitCode == 1 && result.ToDos.HitCount == 0) {
		collectCommandError(&result.Errors, result.ToDos.Command)
	}

	result.DepGraph.Command = runCommand(
		ctx,
		workDir,
		stingrayTimeoutGoModGraph,
		"go",
		"mod",
		"graph",
	)
	result.DepGraph.Edges = parseGoModGraph(result.DepGraph.Command.Stdout)
	result.DepGraph.EdgeCount = len(result.DepGraph.Edges)
	collectCommandError(&result.Errors, result.DepGraph.Command)

	fileMetrics, packageMetrics, metricErrors := collectRawFileMetrics(workDir, modulePath)
	if len(metricErrors) > 0 {
		result.Errors = append(result.Errors, metricErrors...)
	}
	result.FileMetrics = fileMetrics
	result.Packages = packageMetrics

	logger.Info(stingrayPrefix+" Metrics collected",
		"Project", project,
		"GoVetExit", result.GoVet.ExitCode,
		"LintIssues", result.GolangCILint.IssueCount,
		"TODOHits", result.ToDos.HitCount,
		"DepGraphEdges", result.DepGraph.EdgeCount,
		"Files", len(result.FileMetrics),
		"Packages", len(result.Packages),
	)

	return result, nil
}

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

func parseGolangCILintOutput(raw string) (int, []string, []GolangCILintIssue, error) {
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
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return 0, nil, nil, err
	}

	linters := make(map[string]struct{})
	parsed := make([]GolangCILintIssue, 0, len(payload.Issues))
	for _, issue := range payload.Issues {
		linters[issue.FromLinter] = struct{}{}
		parsed = append(parsed, GolangCILintIssue{
			Linter:  issue.FromLinter,
			Message: issue.Text,
			File:    issue.Pos.Filename,
			Line:    issue.Pos.Line,
		})
	}

	linterList := make([]string, 0, len(linters))
	for linter := range linters {
		linterList = append(linterList, linter)
	}
	sort.Strings(linterList)

	return len(payload.Issues), linterList, parsed, nil
}

func parseCoverageTotal(raw string) (float64, string, error) {
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "total:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, "", fmt.Errorf("unexpected total line: %q", line)
		}
		percentStr := strings.TrimSuffix(fields[len(fields)-1], "%")
		value, err := strconv.ParseFloat(percentStr, 64)
		if err != nil {
			return 0, "", fmt.Errorf("invalid coverage percentage %q: %w", fields[len(fields)-1], err)
		}
		return value, line, nil
	}
	if err := sc.Err(); err != nil {
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
		// Strip version suffixes (module@v1.2.3 → module) for cleaner graph.
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

func parseTODOOutput(raw string, baseDir string) []TODOHit {
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
