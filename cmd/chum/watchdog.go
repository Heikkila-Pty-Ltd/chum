package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/matrix"
	_ "modernc.org/sqlite"
)

// SystemHealthReport is the output of a watchdog health check.
type SystemHealthReport struct {
	DispatchesLast30m  int      `json:"dispatches_last_30m"`
	CompletionsLast30m int      `json:"completions_last_30m"`
	FailuresLast30m    int      `json:"failures_last_30m"`
	EscalationsLast30m int      `json:"escalations_last_30m"`
	RunningWorkflows   int      `json:"running_workflows"`
	PlanningFailures   int      `json:"planning_failures"`
	RepeatedErrors     []string `json:"repeated_errors,omitempty"`
	TokensBurned       int64    `json:"tokens_burned"`
	LogTailErrors      []string `json:"log_tail_errors,omitempty"`
	Stuck              bool     `json:"stuck"`
	Verdict            string   `json:"verdict"` // healthy | degraded | critical
	Summary            string   `json:"summary"`
}

// matrixNotifier wraps a Matrix sender with a target room for watchdog messages.
type matrixNotifier struct {
	sender matrix.Sender
	room   string
}

func (n *matrixNotifier) send(msg string) {
	if n == nil || n.sender == nil || n.room == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	//nolint:errcheck // best-effort notification
	n.sender.SendMessage(ctx, n.room, msg)
}

func runWatchdogCommand(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("watchdog", flag.ExitOnError)
	configPath := fs.String("config", "chum.toml", "path to config file")
	dryRun := fs.Bool("dry-run", false, "check health but do not launch rescue agent")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Set up Matrix notifications.
	var notify *matrixNotifier
	if cfg.Reporter.MatrixBotAccount != "" && cfg.Reporter.AdminRoom != "" {
		sender := matrix.NewHTTPSender(&http.Client{Timeout: 10 * time.Second}, cfg.Reporter.MatrixBotAccount)
		notify = &matrixNotifier{sender: sender, room: cfg.Reporter.AdminRoom}
	} else if cfg.Reporter.MatrixBotAccount != "" && cfg.Reporter.DefaultRoom != "" {
		sender := matrix.NewHTTPSender(&http.Client{Timeout: 10 * time.Second}, cfg.Reporter.MatrixBotAccount)
		notify = &matrixNotifier{sender: sender, room: cfg.Reporter.DefaultRoom}
	}

	dbPath := cfg.General.StateDB
	if dbPath == "" {
		dbPath = config.ExpandHome("~/.local/share/chum/chum.db")
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open db %s: %w", dbPath, err)
	}
	defer db.Close()

	// Find log path from first enabled project workspace.
	var logPath string
	var workDir string
	for name := range cfg.Projects {
		proj := cfg.Projects[name]
		if proj.Enabled && proj.Workspace != "" {
			ws := config.ExpandHome(strings.TrimSpace(proj.Workspace))
			logPath = ws + "/chum.log"
			workDir = ws
			break
		}
	}

	report := runHealthCheck(db, logPath, logger)

	reportJSON, _ := json.MarshalIndent(report, "", "  ") //nolint:errcheck // marshal of known struct
	logger.Info("watchdog health check",
		"verdict", report.Verdict,
		"summary", report.Summary,
		"dispatches", report.DispatchesLast30m,
		"completions", report.CompletionsLast30m,
		"failures", report.FailuresLast30m,
		"tokens_burned", report.TokensBurned,
		"stuck", report.Stuck,
	)

	// Notify Matrix on every non-healthy check.
	switch report.Verdict {
	case "healthy":
		// Silent on healthy — no spam.
	case "degraded":
		notify.send(fmt.Sprintf("🐕 WATCHDOG: %s\n%s", report.Verdict, report.Summary))
	case "critical":
		notify.send(fmt.Sprintf("🚨 WATCHDOG CRITICAL: %s\nDispatches: %d | Completions: %d | Failures: %d | Tokens burned: %d",
			report.Summary, report.DispatchesLast30m, report.CompletionsLast30m, report.FailuresLast30m, report.TokensBurned))
	}

	if report.Verdict != "critical" {
		fmt.Fprintf(os.Stderr, "watchdog: %s — %s\n", report.Verdict, report.Summary)
		return nil
	}

	logger.Warn("CRITICAL verdict — system needs rescue",
		"repeated_errors", len(report.RepeatedErrors),
		"log_errors", len(report.LogTailErrors),
	)

	if *dryRun {
		notify.send("🐕 WATCHDOG: CRITICAL detected (dry-run mode, rescue NOT launched)")
		fmt.Fprintf(os.Stderr, "watchdog: CRITICAL (dry-run, not launching rescue)\n%s\n", reportJSON)
		return nil
	}

	// Check if a rescue agent is already running (simple PID file check).
	pidFile := "/tmp/chum-rescue.pid"
	if data, readErr := os.ReadFile(pidFile); readErr == nil {
		pid := strings.TrimSpace(string(data))
		if _, statErr := os.Stat(fmt.Sprintf("/proc/%s", pid)); statErr == nil {
			logger.Info("rescue agent already running", "pid", pid)
			notify.send(fmt.Sprintf("🐕 WATCHDOG: CRITICAL but rescue agent already running (pid %s)", pid))
			fmt.Fprintf(os.Stderr, "watchdog: CRITICAL but rescue already running (pid %s)\n", pid)
			return nil
		}
		os.Remove(pidFile)
	}

	notify.send("🚑 WATCHDOG: Launching rescue agent (Claude Code Opus 4.6) to investigate and hotfix")

	err = launchRescueAgent(report, reportJSON, workDir, logPath, pidFile, cfg, logger)

	if err != nil {
		notify.send(fmt.Sprintf("🚑 RESCUE FAILED: %v", err))
	} else {
		notify.send("🚑 RESCUE COMPLETE: Agent finished successfully — CHUM should be back online")
	}

	return err
}

func runHealthCheck(db *sql.DB, logPath string, logger *slog.Logger) *SystemHealthReport {
	report := &SystemHealthReport{Verdict: "healthy"}
	cutoff := time.Now().Add(-30 * time.Minute).Format("2006-01-02 15:04:05")

	// Count dispatches by status in the last 30 minutes.
	rows, err := db.Query(
		`SELECT status, COUNT(*) FROM dispatches
		 WHERE dispatched_at > ? GROUP BY status`, cutoff)
	if err != nil {
		logger.Warn("failed to query dispatches", "error", err)
	} else {
		for rows.Next() {
			var status string
			var count int
			if rows.Scan(&status, &count) == nil {
				switch status {
				case "completed":
					report.CompletionsLast30m = count
				case "failed":
					report.FailuresLast30m += count
				case "escalated":
					report.EscalationsLast30m = count
					report.FailuresLast30m += count
				case "running":
					report.DispatchesLast30m += count
				}
				report.DispatchesLast30m += count
			}
		}
		rows.Close()
	}

	// Count tokens burned on failures.
	var tokensBurned sql.NullInt64
	//nolint:errcheck // best-effort metrics
	db.QueryRow(
		`SELECT COALESCE(SUM(output_tokens), 0) FROM dispatches
		 WHERE dispatched_at > ? AND status IN ('failed', 'escalated')`, cutoff).Scan(&tokensBurned)
	if tokensBurned.Valid {
		report.TokensBurned = tokensBurned.Int64
	}

	// Find repeated error patterns (same failure_summary 3+ times).
	errorRows, err := db.Query(
		`SELECT failure_summary, COUNT(*) as cnt FROM dispatches
		 WHERE dispatched_at > ? AND failure_summary != ''
		 GROUP BY failure_summary HAVING cnt >= 3
		 ORDER BY cnt DESC LIMIT 5`, cutoff)
	if err == nil {
		for errorRows.Next() {
			var summary string
			var cnt int
			if errorRows.Scan(&summary, &cnt) == nil {
				if len(summary) > 200 {
					summary = summary[:200]
				}
				report.RepeatedErrors = append(report.RepeatedErrors,
					fmt.Sprintf("[%dx] %s", cnt, summary))
			}
		}
		errorRows.Close()
	}

	// Count running workflows from dispatches table.
	var runningCount int
	//nolint:errcheck // best-effort metrics
	db.QueryRow(`SELECT COUNT(*) FROM dispatches WHERE status = 'running'`).Scan(&runningCount)
	report.RunningWorkflows = runningCount

	// Count planning failures in the last hour.
	var planFails int
	planCutoff := time.Now().Add(-1 * time.Hour).Format("2006-01-02 15:04:05")
	//nolint:errcheck // best-effort metrics
	db.QueryRow(
		`SELECT COUNT(*) FROM organism_logs
		 WHERE organism_type = 'dispatcher' AND created_at > ?
		 AND details LIKE '%dispatched 0%'`, planCutoff).Scan(&planFails)
	report.PlanningFailures = planFails

	// Tail chum.log for error patterns.
	if logPath != "" {
		report.LogTailErrors = tailLogForErrors(logPath, 100)
	}

	// Compute verdict.
	report.Stuck = report.DispatchesLast30m == 0 && report.RunningWorkflows == 0

	switch {
	case report.Stuck && report.PlanningFailures >= 2:
		report.Verdict = "critical"
		report.Summary = fmt.Sprintf("STUCK: 0 dispatches, 0 running, %d planning failures",
			report.PlanningFailures)
	case report.Stuck:
		report.Verdict = "critical"
		report.Summary = "STUCK: 0 dispatches and 0 running workflows in the last 30 minutes"
	case report.CompletionsLast30m == 0 && report.FailuresLast30m >= 5:
		report.Verdict = "critical"
		report.Summary = fmt.Sprintf("BURNING: %d failures, 0 completions, %d tokens wasted",
			report.FailuresLast30m, report.TokensBurned)
	case len(report.RepeatedErrors) > 0 && report.CompletionsLast30m == 0:
		report.Verdict = "critical"
		report.Summary = fmt.Sprintf("REPEATING: %d repeated error patterns, 0 completions",
			len(report.RepeatedErrors))
	case report.FailuresLast30m > 0 && report.CompletionsLast30m == 0 && report.DispatchesLast30m > 0:
		report.Verdict = "degraded"
		report.Summary = fmt.Sprintf("DEGRADED: %d dispatches, %d failures, 0 completions",
			report.DispatchesLast30m, report.FailuresLast30m)
	default:
		report.Verdict = "healthy"
		report.Summary = fmt.Sprintf("OK: %d dispatches, %d completions, %d failures",
			report.DispatchesLast30m, report.CompletionsLast30m, report.FailuresLast30m)
	}

	return report
}

func launchRescueAgent(report *SystemHealthReport, reportJSON []byte, workDir, logPath, pidFile string, cfg *config.Config, logger *slog.Logger) error {
	prompt := buildRescuePromptStandalone(report, reportJSON, workDir, logPath, cfg)

	// Write prompt to temp file.
	promptFile, err := os.CreateTemp("", "chum-rescue-prompt-*.md")
	if err != nil {
		return fmt.Errorf("create prompt file: %w", err)
	}
	if _, err := promptFile.WriteString(prompt); err != nil {
		promptFile.Close()
		return fmt.Errorf("write prompt: %w", err)
	}
	promptFile.Close()

	logger.Warn("launching rescue agent",
		"prompt_file", promptFile.Name(),
		"work_dir", workDir,
	)

	cmd := exec.Command("claude",
		"--dangerously-skip-permissions",
		"--model", "claude-opus-4-6",
		"-p", prompt,
	)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		os.Remove(promptFile.Name())
		return fmt.Errorf("start rescue agent: %w", err)
	}

	_ = os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0o644) //nolint:errcheck // best-effort PID file

	logger.Warn("rescue agent launched", "pid", cmd.Process.Pid)
	fmt.Fprintf(os.Stderr, "watchdog: CRITICAL — rescue agent launched (pid %d)\n", cmd.Process.Pid)

	if err := cmd.Wait(); err != nil {
		logger.Error("rescue agent exited with error", "error", err)
		os.Remove(pidFile)
		os.Remove(promptFile.Name())
		return fmt.Errorf("rescue agent: %w", err)
	}

	os.Remove(pidFile)
	os.Remove(promptFile.Name())
	logger.Info("rescue agent completed successfully")
	return nil
}

func buildRescuePromptStandalone(report *SystemHealthReport, reportJSON []byte, workDir, logPath string, cfg *config.Config) string {
	var sb strings.Builder
	sb.WriteString(`You are the CHUM rescue agent. The automated watchdog has detected a critical failure in the CHUM autonomous coding system.

## System Health Report
` + "```json\n")
	sb.Write(reportJSON)
	sb.WriteString("\n```\n\n")

	if len(report.RepeatedErrors) > 0 {
		sb.WriteString("## Repeated Error Patterns\n")
		for _, e := range report.RepeatedErrors {
			sb.WriteString("- " + e + "\n")
		}
		sb.WriteString("\n")
	}

	if len(report.LogTailErrors) > 0 {
		sb.WriteString("## Error Lines from chum.log\n```\n")
		for _, line := range report.LogTailErrors {
			sb.WriteString(line + "\n")
		}
		sb.WriteString("```\n\n")
	}

	sb.WriteString(fmt.Sprintf(`## Working Directory
%s

## Your Mission

Investigate, diagnose, and fix the issue so CHUM starts flowing work again.

### Step 1: INVESTIGATE
- Read the chum log: %s
- Query the database: sqlite3 ~/.local/share/chum/chum.db "SELECT morsel_id, status, failure_category, failure_summary FROM dispatches WHERE dispatched_at > datetime('now', '-1 hour') ORDER BY dispatched_at DESC LIMIT 20;"
- Check running Temporal workflows: temporal workflow list --query "ExecutionStatus='Running'"
- Read source files based on error patterns

### Step 2: DIAGNOSE
- Identify the root cause
- Determine the minimal fix

### Step 3: HOTFIX
- Write the minimal code change. Do NOT refactor. Do NOT add features.

### Step 4: TEST
- go build ./...
- go test ./internal/temporal/ -count=1

### Step 5: INSTALL
- go build -o /usr/local/bin/chum ./cmd/chum/

### Step 6: RESTART
- chum stop
- sleep 2
- nohup chum --config %s/chum.toml --dev > %s/chum.log 2>&1 &
- sleep 5 && tail -20 %s/chum.log

## Key Source Files
- Config: %s/chum.toml
- Main workflow: %s/internal/temporal/workflow.go
- Dispatcher: %s/internal/temporal/workflow_dispatcher.go
- Planning: %s/internal/temporal/planning_workflow.go
- Worker: %s/internal/temporal/worker.go
- Matrix notifications: %s/internal/temporal/notify.go
- Matrix HTTP sender: %s/internal/matrix/http_sender.go

## Matrix Notifications Debugging

CHUM sends status updates to Matrix via spritzbot. If notifications are broken:

1. Check the OpenClaw config has valid credentials:
   cat ~/.openclaw/openclaw.json | python3 -c "import sys,json; c=json.load(sys.stdin); [print(f'{a[\"name\"]}: token={a[\"access_token\"][:20]}...') for a in c.get('accounts',[])]"

2. Test sending a message directly (replace token/homeserver as needed):
   curl -s -X PUT "https://HOMESERVER/_matrix/client/v3/rooms/%%21adJhqAxLcYqQuYraGf%%3Avmi3041112.contaboserver.net/send/m.room.message/test-$(date +%%s)" \
     -H "Authorization: Bearer ACCESS_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"msgtype":"m.text","body":"RESCUE: test notification"}'

3. Verify spritzbot is in the room:
   curl -s "https://HOMESERVER/_matrix/client/v3/joined_rooms" -H "Authorization: Bearer ACCESS_TOKEN"

4. Check if the worker has a non-nil Sender by looking for "matrix notifications enabled" in startup logs.

5. The DefaultRoom is: %s (from config reporter.default_room)
   The AdminRoom is: %s (from config reporter.admin_room)

6. Common fixes:
   - If Sender is nil: check that reporter.matrix_bot_account and reporter.default_room are set in chum.toml
   - If room send fails with M_FORBIDDEN: spritzbot needs to be invited and joined to the room
   - If credentials expired: re-login via matrix client and update ~/.openclaw/openclaw.json

## CRITICAL RULES
1. MINIMUM change needed to get work flowing
2. Always build and test before installing
3. If you cannot diagnose, write findings to /tmp/chum-rescue-report.txt
4. Do NOT modify the watchdog system itself
`, workDir, logPath,
		workDir, workDir, workDir,
		workDir, workDir, workDir, workDir, workDir, workDir, workDir,
		cfg.Reporter.DefaultRoom, cfg.Reporter.AdminRoom))

	return sb.String()
}

func tailLogForErrors(path string, maxLines int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	errorPatterns := []string{
		"level=error", "level=warn",
		"signal timeout", "planning signal timeout",
		"dispatched 0", "failed", "panic",
		"heartbeat timeout", "activity timeout",
	}

	var errors []string
	for _, line := range lines {
		lower := strings.ToLower(line)
		for _, pat := range errorPatterns {
			if strings.Contains(lower, pat) {
				if len(line) > 300 {
					line = line[:300]
				}
				errors = append(errors, line)
				break
			}
		}
	}

	if len(errors) > 30 {
		errors = errors[len(errors)-30:]
	}
	return errors
}
