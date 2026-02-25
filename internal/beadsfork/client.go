package beadsfork

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const (
	// DefaultBinary is the default Beads CLI binary.
	DefaultBinary = "bd"
	// DefaultPinnedVersion is the target upstream release for the fork scaffold.
	DefaultPinnedVersion = "0.56.1"
)

var versionPattern = regexp.MustCompile(`bd version ([^\s]+)`)

// Runner executes external commands.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) ([]byte, error)
}

// ExecRunner uses os/exec to run commands.
type ExecRunner struct{}

// Run executes a command and returns combined output.
func (ExecRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

// Options configures Client construction.
type Options struct {
	Binary              string
	WorkDir             string
	PinnedVersion       string
	Runner              Runner
	DisableNoDaemon     bool
	DisableNoAutoImport bool
	DisableNoAutoFlush  bool
}

// Client is a local wrapper around bd with fixed safety/isolation defaults.
type Client struct {
	binary      string
	workDir     string
	pinned      string
	runner      Runner
	globalFlags []string
}

// VersionInfo represents `bd version --json` output.
type VersionInfo struct {
	Branch  string `json:"branch"`
	Build   string `json:"build"`
	Commit  string `json:"commit"`
	Version string `json:"version"`
}

// Dependency represents an issue dependency edge in bd JSON.
type Dependency struct {
	IssueID     string `json:"issue_id"`
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type"`
}

// Issue is the minimal issue payload CHUM needs for scaffold evaluation.
type Issue struct {
	ID                 string       `json:"id"`
	Title              string       `json:"title"`
	Description        string       `json:"description,omitempty"`
	Status             string       `json:"status"`
	Priority           int          `json:"priority"`
	IssueType          string       `json:"issue_type"`
	Labels             []string     `json:"labels,omitempty"`
	CreatedAt          string       `json:"created_at,omitempty"`
	UpdatedAt          string       `json:"updated_at,omitempty"`
	DependencyCount    int          `json:"dependency_count,omitempty"`
	DependentCount     int          `json:"dependent_count,omitempty"`
	CommentCount       int          `json:"comment_count,omitempty"`
	BlockedByCount     int          `json:"blocked_by_count,omitempty"`
	BlockedBy          []string     `json:"blocked_by,omitempty"`
	Dependencies       []Dependency `json:"dependencies,omitempty"`
	AcceptanceCriteria string       `json:"acceptance_criteria,omitempty"`
	Design             string       `json:"design,omitempty"`
}

// CreateRequest captures a scoped create surface for the scaffold.
type CreateRequest struct {
	Description string
	Priority    int
	IssueType   string
	Labels      []string
}

// UpdateRequest captures a scoped update surface for the scaffold.
type UpdateRequest struct {
	Status   string
	Priority *int
	Title    string
}

// NewClient creates an isolated bd client for local CHUM fork evaluation.
func NewClient(opts Options) (*Client, error) {
	workDir := strings.TrimSpace(opts.WorkDir)
	if workDir == "" {
		return nil, errors.New("workdir is required")
	}

	binary := strings.TrimSpace(opts.Binary)
	if binary == "" {
		binary = DefaultBinary
	}

	pinned := strings.TrimSpace(opts.PinnedVersion)
	if pinned == "" {
		pinned = DefaultPinnedVersion
	}

	runner := opts.Runner
	if runner == nil {
		runner = ExecRunner{}
	}

	globalFlags := []string{}
	if !opts.DisableNoDaemon {
		globalFlags = append(globalFlags, "--no-daemon")
	}
	if !opts.DisableNoAutoImport {
		globalFlags = append(globalFlags, "--no-auto-import")
	}
	if !opts.DisableNoAutoFlush {
		globalFlags = append(globalFlags, "--no-auto-flush")
	}

	return &Client{
		binary:      binary,
		workDir:     workDir,
		pinned:      normalizeVersion(pinned),
		runner:      runner,
		globalFlags: globalFlags,
	}, nil
}

// Version returns the active bd binary version.
func (c *Client) Version(ctx context.Context) (VersionInfo, error) {
	out, err := c.runRaw(ctx, "version", "--json")
	if err != nil {
		return VersionInfo{}, err
	}

	var info VersionInfo
	if payload := extractJSONPayload(out); payload != nil {
		if parseErr := json.Unmarshal(payload, &info); parseErr == nil && strings.TrimSpace(info.Version) != "" {
			info.Version = normalizeVersion(info.Version)
			return info, nil
		}
	}

	plain := strings.TrimSpace(string(out))
	match := versionPattern.FindStringSubmatch(plain)
	if len(match) < 2 {
		return VersionInfo{}, fmt.Errorf("parse bd version from output: %s", compactOutput(out))
	}
	info.Version = normalizeVersion(match[1])
	return info, nil
}

// CheckPinnedVersion validates the active bd binary against the pinned version.
func (c *Client) CheckPinnedVersion(ctx context.Context) error {
	info, err := c.Version(ctx)
	if err != nil {
		return err
	}
	if normalizeVersion(info.Version) != c.pinned {
		return fmt.Errorf("bd version mismatch: expected %s, got %s", c.pinned, info.Version)
	}
	return nil
}

// Create creates a new issue.
func (c *Client) Create(ctx context.Context, title string, req CreateRequest) (Issue, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Issue{}, errors.New("title is required")
	}

	args := []string{"create", title, "--json"}
	if desc := strings.TrimSpace(req.Description); desc != "" {
		args = append(args, "--description", desc)
	}
	if req.Priority >= 0 {
		args = append(args, "--priority", strconv.Itoa(req.Priority))
	}
	if typ := strings.TrimSpace(req.IssueType); typ != "" {
		args = append(args, "--type", typ)
	}
	if len(req.Labels) > 0 {
		args = append(args, "--labels", strings.Join(req.Labels, ","))
	}

	out, err := c.runRaw(ctx, args...)
	if err != nil {
		return Issue{}, err
	}
	return decodeSingleIssue(out)
}

// List returns issues from `bd list`.
func (c *Client) List(ctx context.Context, limit int) ([]Issue, error) {
	args := []string{"list", "--json"}
	if limit > 0 {
		args = append(args, "--limit", strconv.Itoa(limit))
	}
	out, err := c.runRaw(ctx, args...)
	if err != nil {
		return nil, err
	}
	return decodeIssueList(out)
}

// Show returns details for one issue.
func (c *Client) Show(ctx context.Context, issueID string) (Issue, error) {
	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return Issue{}, errors.New("issue id is required")
	}
	out, err := c.runRaw(ctx, "show", "--json", issueID)
	if err != nil {
		return Issue{}, err
	}
	return decodeSingleIssue(out)
}

// Update updates scoped fields on an issue.
func (c *Client) Update(ctx context.Context, issueID string, req UpdateRequest) (Issue, error) {
	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return Issue{}, errors.New("issue id is required")
	}

	args := []string{"update", issueID, "--json"}
	if status := strings.TrimSpace(req.Status); status != "" {
		args = append(args, "--status", status)
	}
	if req.Priority != nil {
		args = append(args, "--priority", strconv.Itoa(*req.Priority))
	}
	if title := strings.TrimSpace(req.Title); title != "" {
		args = append(args, "--title", title)
	}

	out, err := c.runRaw(ctx, args...)
	if err != nil {
		return Issue{}, err
	}
	return decodeSingleIssue(out)
}

// AddDependency links issueID -> dependsOnID with dependency type.
func (c *Client) AddDependency(ctx context.Context, issueID, dependsOnID, depType string) error {
	issueID = strings.TrimSpace(issueID)
	dependsOnID = strings.TrimSpace(dependsOnID)
	depType = strings.TrimSpace(depType)
	if issueID == "" || dependsOnID == "" {
		return errors.New("issue id and depends-on id are required")
	}
	if depType == "" {
		depType = "blocks"
	}

	_, err := c.runRaw(ctx, "dep", "add", issueID, dependsOnID, "--type", depType, "--json")
	return err
}

// Ready returns unblocked ready issues.
func (c *Client) Ready(ctx context.Context, limit int) ([]Issue, error) {
	args := []string{"ready", "--json"}
	if limit > 0 {
		args = append(args, "--limit", strconv.Itoa(limit))
	}
	out, err := c.runRaw(ctx, args...)
	if err != nil {
		return nil, err
	}
	return decodeIssueList(out)
}

// Blocked returns blocked issues.
func (c *Client) Blocked(ctx context.Context) ([]Issue, error) {
	out, err := c.runRaw(ctx, "blocked", "--json")
	if err != nil {
		return nil, err
	}
	return decodeIssueList(out)
}

// SyncFlushOnly exports DB state to JSONL without git operations.
func (c *Client) SyncFlushOnly(ctx context.Context) error {
	_, err := c.runRaw(ctx, "sync", "--flush-only")
	return err
}

func (c *Client) runRaw(ctx context.Context, args ...string) ([]byte, error) {
	fullArgs := make([]string, 0, len(c.globalFlags)+len(args))
	fullArgs = append(fullArgs, c.globalFlags...)
	fullArgs = append(fullArgs, args...)

	out, err := c.runner.Run(ctx, c.workDir, c.binary, fullArgs...)
	if err != nil {
		return out, fmt.Errorf("%s %s failed: %w (%s)", c.binary, strings.Join(args, " "), err, compactOutput(out))
	}
	return out, nil
}

func decodeSingleIssue(out []byte) (Issue, error) {
	payload := extractJSONPayload(out)
	if payload == nil {
		return Issue{}, fmt.Errorf("missing JSON payload: %s", compactOutput(out))
	}

	payload = []byte(strings.TrimSpace(string(payload)))
	if len(payload) == 0 {
		return Issue{}, fmt.Errorf("empty JSON payload: %s", compactOutput(out))
	}

	switch payload[0] {
	case '{':
		var one Issue
		if err := json.Unmarshal(payload, &one); err != nil {
			return Issue{}, fmt.Errorf("decode issue object: %w", err)
		}
		return one, nil
	case '[':
		var many []Issue
		if err := json.Unmarshal(payload, &many); err != nil {
			return Issue{}, fmt.Errorf("decode issue list: %w", err)
		}
		if len(many) == 0 {
			return Issue{}, errors.New("issue list is empty")
		}
		return many[0], nil
	default:
		return Issue{}, fmt.Errorf("unexpected JSON payload: %s", compactOutput(payload))
	}
}

func decodeIssueList(out []byte) ([]Issue, error) {
	payload := extractJSONPayload(out)
	if payload == nil {
		return nil, fmt.Errorf("missing JSON payload: %s", compactOutput(out))
	}

	var list []Issue
	if err := json.Unmarshal(payload, &list); err != nil {
		return nil, fmt.Errorf("decode issue list: %w", err)
	}
	return list, nil
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	return v
}

func compactOutput(out []byte) string {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "no output"
	}
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}

func extractJSONPayload(out []byte) []byte {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil
	}
	if json.Valid([]byte(s)) {
		return []byte(s)
	}

	for i := 0; i < len(s); i++ {
		if s[i] != '{' && s[i] != '[' {
			continue
		}

		candidate := strings.TrimSpace(s[i:])
		if candidate == "" {
			continue
		}
		if json.Valid([]byte(candidate)) {
			return []byte(candidate)
		}

		dec := json.NewDecoder(strings.NewReader(candidate))
		var v any
		if err := dec.Decode(&v); err == nil {
			offset := dec.InputOffset()
			if offset > 0 && int(offset) <= len(candidate) {
				prefix := strings.TrimSpace(candidate[:offset])
				if json.Valid([]byte(prefix)) {
					return []byte(prefix)
				}
			}
		}
	}

	return nil
}
