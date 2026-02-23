package temporal

import "time"

// Stingray timeout and output constants.
const (
	stingrayTimeoutGoVet             = 2 * time.Minute
	stingrayTimeoutGolangCILint      = 2 * time.Minute
	stingrayTimeoutGoTestCoverage    = 4 * time.Minute
	stingrayTimeoutGoCover           = 90 * time.Second
	stingrayTimeoutGoListOutdated    = 2 * time.Minute
	stingrayTimeoutTODOs             = 90 * time.Second
	stingrayTimeoutGoModGraph        = 90 * time.Second
	stingrayCommandOutputTruncateLen = 5000
	stingrayPrefix                   = "\033[33m🦂 STINGRAY\033[0m"
)

// StingrayMetricsRequest is the input for GatherMetricsActivity.
type StingrayMetricsRequest struct {
	Project string `json:"project"`
	WorkDir string `json:"work_dir"`
}

// CommandResult captures the outcome of a single shell command execution.
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

// RawFileMetric holds per-file code metrics gathered by the AST walker.
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

// PackageMetric aggregates file-level metrics into a per-package summary.
type PackageMetric struct {
	Package   string `json:"package"`
	Dir       string `json:"dir"`
	Files     int    `json:"files"`
	LOC       int    `json:"loc"`
	NonBlank  int    `json:"non_blank"`
	Functions int    `json:"functions"`
	Methods   int    `json:"methods"`
	Types     int    `json:"types"`
	Imports   int    `json:"imports"`
	FanIn     int    `json:"fan_in"`
	FanOut    int    `json:"fan_out"`
}

// TODOHit represents a single TODO/HACK/FIXME/WORKAROUND marker found in source.
type TODOHit struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
	Kind string `json:"kind"`
}

// TODOScanResult wraps the grep command output and parsed TODO markers.
type TODOScanResult struct {
	Command  CommandResult `json:"command"`
	Hits     []TODOHit     `json:"hits"`
	HitCount int           `json:"hit_count"`
}

// CoverageResult holds test coverage output and the parsed total percentage.
type CoverageResult struct {
	Test         CommandResult `json:"test"`
	Report       CommandResult `json:"report"`
	TotalPercent float64       `json:"total_percent"`
	TotalLine    string        `json:"total_line"`
	ParseError   string        `json:"parse_error,omitempty"`
}

// OutdatedDependency records a Go module with an available newer version.
type OutdatedDependency struct {
	Module  string `json:"module"`
	Current string `json:"current"`
	Latest  string `json:"latest"`
}

// OutdatedDependenciesResult wraps the go list -m -u output and parsed dependencies.
type OutdatedDependenciesResult struct {
	Command       CommandResult        `json:"command"`
	Dependencies  []OutdatedDependency `json:"dependencies"`
	OutdatedCount int                  `json:"outdated_count"`
	ParseError    string               `json:"parse_error,omitempty"`
}

// GolangCILintIssue is a single lint issue reported by golangci-lint.
type GolangCILintIssue struct {
	Linter  string `json:"linter"`
	Message string `json:"message"`
	File    string `json:"file"`
	Line    int    `json:"line"`
}

// GolangCILintResult wraps the golangci-lint JSON output and parsed issues.
type GolangCILintResult struct {
	Command    CommandResult       `json:"command"`
	IssueCount int                 `json:"issue_count"`
	Linters    []string            `json:"linters"`
	Issues     []GolangCILintIssue `json:"issues"`
	ParseError string              `json:"parse_error,omitempty"`
}

// DepGraphEdge represents a single from->to edge in the go mod graph.
type DepGraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// DepGraphResult wraps the go mod graph output and parsed dependency edges.
type DepGraphResult struct {
	Command   CommandResult  `json:"command"`
	Edges     []DepGraphEdge `json:"edges"`
	EdgeCount int            `json:"edge_count"`
}

// StingrayMetrics is the top-level result returned by GatherMetricsActivity.
type StingrayMetrics struct {
	Project       string                     `json:"project"`
	WorkDir       string                     `json:"work_dir"`
	GatheredAtUTC string                     `json:"gathered_at_utc"`
	GoVet         CommandResult              `json:"go_vet"`
	GolangCILint  GolangCILintResult         `json:"golangci_lint"`
	Coverage      CoverageResult             `json:"coverage"`
	OutdatedDeps  OutdatedDependenciesResult `json:"outdated_deps"`
	ToDos         TODOScanResult             `json:"todos"`
	DepGraph      DepGraphResult             `json:"dep_graph"`
	FileMetrics   []RawFileMetric            `json:"file_metrics"`
	Packages      []PackageMetric            `json:"package_metrics"`
	Errors        []string                   `json:"errors"`
}
