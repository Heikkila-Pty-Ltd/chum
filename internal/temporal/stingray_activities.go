package temporal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
)

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
