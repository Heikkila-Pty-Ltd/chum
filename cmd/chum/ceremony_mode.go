package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	tclient "go.temporal.io/sdk/client"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/store"
	"github.com/antigravity-dev/chum/internal/temporal"
)

const ceremonyTraceLimit = 500

type ceremonyPrompt struct {
	Phase          string
	ExpectedSignal string
	Prompt         string
	Options        []string
	Recommendation string
	Context        string
	Cycle          int
	SelectedID     string
	SelectedTitle  string
}

type ceremonyCandidateOption struct {
	ID          string
	Title       string
	Rank        int
	Shortlisted bool
	Recommended bool
}

func runCeremonyMode(args []string, _ *slog.Logger) error {
	fs := flag.NewFlagSet("ceremony", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	configPath := fs.String("config", "chum.toml", "path to config file")
	sessionIDFlag := fs.String("session", "", "attach to existing planning session id")
	projectFlag := fs.String("project", "", "project key (defaults to first enabled project)")
	workdirFlag := fs.String("workdir", "", "workspace path (defaults to project workspace)")
	agentFlag := fs.String("agent", "claude", "planning agent")
	tierFlag := fs.String("tier", "fast", "planning tier")
	topKFlag := fs.Int("topk", 0, "candidate shortlist size (default from config)")

	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected ceremony arguments: %v", fs.Args())
	}

	cfgMgr, err := config.LoadManager(*configPath)
	if err != nil {
		return err
	}
	cfg := cfgMgr.Get()
	if cfg == nil {
		return fmt.Errorf("failed to load config snapshot")
	}

	stateDB := config.ExpandHome(cfg.General.StateDB)
	st, err := store.Open(stateDB)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	host := strings.TrimSpace(cfg.General.TemporalHostPort)
	if host == "" {
		host = temporal.DefaultTemporalHostPort
	}
	namespace := resolveTemporalNamespace()

	tc, err := tclient.Dial(tclient.Options{
		HostPort:  host,
		Namespace: namespace,
	})
	if err != nil {
		return fmt.Errorf("connect temporal: %w", err)
	}
	defer tc.Close()

	sessionID := strings.TrimSpace(*sessionIDFlag)
	var project string

	if sessionID == "" {
		resolvedProject, resolvedWorkdir, err := resolveCeremonyProjectAndWorkdir(cfg, *projectFlag, *workdirFlag)
		if err != nil {
			return err
		}
		project = resolvedProject

		topK := *topKFlag
		if topK <= 0 {
			topK = cfg.Dispatch.CostControl.PlanningCandidateTopK
		}
		if topK <= 0 {
			topK = 5
		}

		req := temporal.PlanningRequest{
			Project:           resolvedProject,
			Agent:             strings.TrimSpace(*agentFlag),
			Tier:              strings.TrimSpace(*tierFlag),
			WorkDir:           resolvedWorkdir,
			CandidateTopK:     topK,
			SignalTimeout:     cfg.Dispatch.CostControl.PlanningSignalTimeout.Duration,
			SessionTimeout:    cfg.Dispatch.CostControl.PlanningSessionTimeout.Duration,
			SlowStepThreshold: cfg.General.SlowStepThreshold.Duration,
		}
		if req.Agent == "" {
			req.Agent = "claude"
		}
		if req.Tier == "" {
			req.Tier = "fast"
		}
		if req.SlowStepThreshold <= 0 {
			req.SlowStepThreshold = 2 * time.Minute
		}
		if req.SignalTimeout <= 0 {
			req.SignalTimeout = 10 * time.Minute
		}
		if req.SessionTimeout <= 0 {
			req.SessionTimeout = 30 * time.Minute
		}

		workflowID := fmt.Sprintf("planning-%s-%d", strings.TrimSpace(req.Project), time.Now().Unix())
		wo := tclient.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: temporal.DefaultTaskQueue,
		}
		we, err := tc.ExecuteWorkflow(context.Background(), wo, temporal.PlanningCeremonyWorkflow, req)
		if err != nil {
			return fmt.Errorf("start planning ceremony: %w", err)
		}
		sessionID = we.GetID()
		recordCeremonyControlTrace(st, sessionID, project, "control", "control_session_started",
			"planning session started from CLI ceremony",
			fmt.Sprintf(
				"project=%s work_dir=%s agent=%s tier=%s top_k=%d signal_timeout=%s session_timeout=%s",
				req.Project,
				req.WorkDir,
				req.Agent,
				req.Tier,
				req.CandidateTopK,
				req.SignalTimeout,
				req.SessionTimeout,
			),
			map[string]any{
				"source":          "cli-ceremony",
				"project":         req.Project,
				"work_dir":        req.WorkDir,
				"agent":           req.Agent,
				"tier":            req.Tier,
				"candidate_top_k": req.CandidateTopK,
				"signal_timeout":  req.SignalTimeout.String(),
				"session_timeout": req.SessionTimeout.String(),
			},
		)
		fmt.Printf("Started planning session %s (run: %s)\n", sessionID, we.GetRunID())
	} else {
		project = inferCeremonyProject(st, sessionID)
		fmt.Printf("Attached to planning session %s\n", sessionID)
	}

	fmt.Println("CLI ceremony chat ready. Type 'help' for commands.")
	return runCeremonyLoop(tc, st, sessionID, project)
}

func resolveCeremonyProjectAndWorkdir(cfg *config.Config, projectRaw, workdirRaw string) (string, string, error) {
	if cfg == nil {
		return "", "", fmt.Errorf("nil config")
	}
	project := strings.TrimSpace(projectRaw)
	if project == "" {
		enabled := make([]string, 0, len(cfg.Projects))
		for name, p := range cfg.Projects {
			if p.Enabled {
				enabled = append(enabled, name)
			}
		}
		sort.Strings(enabled)
		if len(enabled) == 0 {
			return "", "", fmt.Errorf("no enabled projects configured")
		}
		project = enabled[0]
	}

	projCfg, ok := cfg.Projects[project]
	if !ok {
		return "", "", fmt.Errorf("project %q not found in config", project)
	}
	if !projCfg.Enabled {
		return "", "", fmt.Errorf("project %q is disabled", project)
	}

	workdir := strings.TrimSpace(workdirRaw)
	if workdir == "" {
		workdir = strings.TrimSpace(projCfg.Workspace)
	}
	workdir = config.ExpandHome(workdir)
	if workdir == "" {
		return "", "", fmt.Errorf("workdir is required for project %q", project)
	}
	return project, workdir, nil
}

func runCeremonyLoop(tc tclient.Client, st *store.Store, sessionID, project string) error {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		status, err := describeCeremonySession(tc, sessionID)
		if err != nil {
			return err
		}
		prompt, _ := inferCeremonyPrompt(st, sessionID, status)
		recordCeremonyControlTrace(st, sessionID, project, "control_status", "control_status_requested",
			fmt.Sprintf("status requested: %s", status),
			fmt.Sprintf("status=%s", status),
			map[string]any{"source": "cli-ceremony", "status": status},
		)
		recordCeremonyControlTrace(st, sessionID, project, "control_prompt", "control_prompt_presented",
			fmt.Sprintf("prompt presented phase=%s signal=%s", prompt.Phase, prompt.ExpectedSignal),
			fmt.Sprintf("prompt=%s", prompt.Prompt),
			map[string]any{"source": "cli-ceremony", "phase": prompt.Phase, "expected_signal": prompt.ExpectedSignal, "cycle": prompt.Cycle},
		)

		printCeremonyPrompt(sessionID, status, prompt)
		if !strings.EqualFold(status, "Running") {
			fmt.Println("Session is no longer running; exiting ceremony chat.")
			return nil
		}

		fmt.Print("ceremony> ")
		if !scanner.Scan() {
			return nil
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		cmdLower := strings.ToLower(line)
		switch {
		case cmdLower == "help":
			printCeremonyHelp()
			continue
		case cmdLower == "status" || cmdLower == "next":
			continue
		case cmdLower == "quit" || cmdLower == "exit":
			fmt.Println("Leaving session running. Reattach with: chum ceremony --session", sessionID)
			return nil
		case strings.HasPrefix(cmdLower, "stop"):
			reason := strings.TrimSpace(line[len("stop"):])
			if reason == "" {
				reason = "stopped by cli ceremony"
			}
			if err := tc.TerminateWorkflow(context.Background(), sessionID, "", reason); err != nil {
				fmt.Printf("stop failed: %v\n", err)
				continue
			}
			recordCeremonyControlTrace(st, sessionID, project, "control", "control_session_stopped",
				"planning session stopped from CLI ceremony", "reason="+reason,
				map[string]any{"source": "cli-ceremony", "reason": reason},
			)
			fmt.Println("Session terminated.")
			return nil
		}

		signalName, value, err := parseCeremonyInput(line, prompt)
		if err != nil {
			fmt.Printf("%v\n", err)
			continue
		}
		if signalName == "" {
			continue
		}

		if err := tc.SignalWorkflow(context.Background(), sessionID, "", signalName, value); err != nil {
			fmt.Printf("signal failed: %v\n", err)
			continue
		}
		recordCeremonyControlTrace(st, sessionID, project, ceremonyStageForSignal(signalName), "control_signal_submitted",
			fmt.Sprintf("signal %s submitted", signalName), fmt.Sprintf("signal=%s value=%s", signalName, value),
			map[string]any{"source": "cli-ceremony", "signal": signalName, "value": value},
		)
	}
}

func printCeremonyPrompt(sessionID, status string, prompt ceremonyPrompt) {
	fmt.Println()
	fmt.Printf("Session: %s\n", sessionID)
	fmt.Printf("Status:  %s\n", status)
	if strings.TrimSpace(prompt.Phase) != "" {
		fmt.Printf("Phase:   %s\n", prompt.Phase)
	}
	if strings.TrimSpace(prompt.Prompt) != "" {
		fmt.Println()
		fmt.Println(prompt.Prompt)
	}
	if len(prompt.Options) > 0 {
		fmt.Println()
		fmt.Println("Options:")
		for i := range prompt.Options {
			fmt.Printf("%d. %s\n", i+1, prompt.Options[i])
		}
	}
	if strings.TrimSpace(prompt.Recommendation) != "" {
		fmt.Printf("\nRecommendation: %s\n", prompt.Recommendation)
	}
	if strings.TrimSpace(prompt.Context) != "" {
		fmt.Printf("Context: %s\n", prompt.Context)
	}
	if strings.TrimSpace(prompt.ExpectedSignal) != "" {
		fmt.Printf("Expected: %s\n", prompt.ExpectedSignal)
	}
	fmt.Println()
}

func printCeremonyHelp() {
	fmt.Println("Commands:")
	fmt.Println("- status | next                refresh status/prompt")
	fmt.Println("- select <item-id>            submit item-selected")
	fmt.Println("- answer <text>               submit answer")
	fmt.Println("- go                          submit greenlight GO")
	fmt.Println("- realign                     submit greenlight REALIGN")
	fmt.Println("- stop [reason]               terminate session")
	fmt.Println("- exit | quit                 leave chat (session keeps running)")
	fmt.Println("- help                        show this help")
	fmt.Println("You can also type plain text; it is routed by the expected signal.")
}

func parseCeremonyInput(line string, prompt ceremonyPrompt) (signalName, value string, err error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", nil
	}

	lower := strings.ToLower(line)
	switch {
	case strings.HasPrefix(lower, "select "):
		v := strings.TrimSpace(line[len("select "):])
		if v == "" {
			return "", "", fmt.Errorf("select requires an item id")
		}
		return "item-selected", v, nil
	case strings.HasPrefix(lower, "answer "):
		v := strings.TrimSpace(line[len("answer "):])
		if v == "" {
			return "", "", fmt.Errorf("answer requires text")
		}
		return "answer", v, nil
	case lower == "go":
		return "greenlight", "GO", nil
	case lower == "realign" || lower == "no":
		return "greenlight", "REALIGN", nil
	}

	switch strings.TrimSpace(prompt.ExpectedSignal) {
	case "item-selected":
		return "item-selected", line, nil
	case "answer":
		return "answer", line, nil
	case "greenlight":
		if strings.EqualFold(line, "go") {
			return "greenlight", "GO", nil
		}
		if strings.EqualFold(line, "realign") || strings.EqualFold(line, "no") {
			return "greenlight", "REALIGN", nil
		}
		return "", "", fmt.Errorf("greenlight expects 'go' or 'realign'")
	default:
		return "", "", fmt.Errorf("unknown command %q; type 'help'", line)
	}
}

func describeCeremonySession(tc tclient.Client, sessionID string) (string, error) {
	resp, err := tc.DescribeWorkflowExecution(context.Background(), sessionID, "")
	if err != nil {
		return "", fmt.Errorf("describe workflow %s: %w", sessionID, err)
	}
	return strings.TrimSpace(resp.WorkflowExecutionInfo.Status.String()), nil
}

func inferCeremonyPrompt(st *store.Store, sessionID, status string) (ceremonyPrompt, error) {
	resp := ceremonyPrompt{
		Phase:  "processing",
		Prompt: "Planning workflow is running. Waiting for the next state update.",
	}
	events, err := st.ListPlanningTraceEvents(sessionID, ceremonyTraceLimit)
	if err != nil {
		return resp, err
	}
	if len(events) == 0 {
		return resp, nil
	}

	latestCycle := 0
	for i := range events {
		if events[i].Cycle > latestCycle {
			latestCycle = events[i].Cycle
		}
	}
	resp.Cycle = latestCycle

	if hasCeremonyEventType(events, latestCycle, "plan_agreed") {
		resp.Phase = "agreed"
		resp.ExpectedSignal = ""
		resp.Prompt = "Plan is agreed and execution has been dispatched."
		return resp, nil
	}
	if hasCeremonyEventType(events, latestCycle, "planning_exhausted") {
		resp.Phase = "exhausted"
		resp.ExpectedSignal = ""
		resp.Prompt = "Planning exhausted without agreement."
		return resp, nil
	}

	selectedID, selectedTitle := latestCeremonySelectedItem(events, latestCycle)
	resp.SelectedID = selectedID
	resp.SelectedTitle = selectedTitle

	questions := latestCeremonyQuestions(events, latestCycle, selectedID)
	if len(questions) > 0 {
		answerCount := countCeremonyAnswers(events, latestCycle, selectedID)
		if answerCount < len(questions) {
			q := questions[answerCount]
			resp.Phase = "questioning"
			resp.ExpectedSignal = "answer"
			resp.Prompt = strings.TrimSpace(q.Question)
			resp.Options = append([]string(nil), q.Options...)
			resp.Recommendation = strings.TrimSpace(q.Recommendation)
			resp.Context = strings.TrimSpace(q.Context)
			return resp, nil
		}
		if !hasCeremonyEventType(events, latestCycle, "greenlight_decision") && hasCeremonySummary(events, latestCycle, selectedID) {
			resp.Phase = "greenlight"
			resp.ExpectedSignal = "greenlight"
			resp.Prompt = latestCeremonySummaryPrompt(events, latestCycle, selectedID)
			resp.Options = []string{"GO", "REALIGN"}
			resp.Recommendation = "Use GO to execute this plan; REALIGN to run another planning cycle."
			return resp, nil
		}
	}

	candidates := latestCeremonyCandidates(events, latestCycle)
	if selectedID == "" && len(candidates) > 0 {
		resp.Phase = "selecting"
		resp.ExpectedSignal = "item-selected"
		resp.Prompt = "Select the highest-value slice."
		resp.Options = formatCeremonyCandidateOptions(candidates)
		resp.Recommendation = "Pick the highest-ranked shortlisted option unless you have a strategic reason to override."
		return resp, nil
	}

	if strings.EqualFold(strings.TrimSpace(status), "Running") {
		resp.Phase = "processing"
		resp.ExpectedSignal = ""
		resp.Prompt = "Planning is processing the current step."
		return resp, nil
	}
	resp.Phase = "completed"
	resp.ExpectedSignal = ""
	resp.Prompt = "Planning session is closed."
	return resp, nil
}

func hasCeremonyEventType(events []store.PlanningTraceEvent, cycle int, eventType string) bool {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return false
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Cycle != cycle {
			continue
		}
		if strings.TrimSpace(events[i].EventType) == eventType {
			return true
		}
	}
	return false
}

func latestCeremonySelectedItem(events []store.PlanningTraceEvent, cycle int) (string, string) {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Cycle != cycle || strings.TrimSpace(ev.EventType) != "item_selected" {
			continue
		}
		id := strings.TrimSpace(ev.OptionID)
		if id == "" {
			id = strings.TrimSpace(ev.TaskID)
		}
		title := strings.TrimSpace(ev.SummaryText)
		if title == "" {
			title = id
		}
		return id, title
	}
	return "", ""
}

func latestCeremonyQuestions(events []store.PlanningTraceEvent, cycle int, selectedID string) []temporal.PlanningQuestion {
	selectedID = strings.TrimSpace(selectedID)
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Cycle != cycle || strings.TrimSpace(ev.EventType) != "questions_result" {
			continue
		}
		if selectedID != "" && strings.TrimSpace(ev.TaskID) != selectedID && strings.TrimSpace(ev.OptionID) != selectedID {
			continue
		}
		raw := strings.TrimSpace(ev.FullText)
		if raw == "" {
			continue
		}
		var questions []temporal.PlanningQuestion
		if err := json.Unmarshal([]byte(raw), &questions); err != nil {
			return nil
		}
		return questions
	}
	return nil
}

func countCeremonyAnswers(events []store.PlanningTraceEvent, cycle int, selectedID string) int {
	selectedID = strings.TrimSpace(selectedID)
	count := 0
	for i := range events {
		ev := events[i]
		if ev.Cycle != cycle || strings.TrimSpace(ev.EventType) != "answer_recorded" {
			continue
		}
		if selectedID != "" && strings.TrimSpace(ev.OptionID) != selectedID && strings.TrimSpace(ev.TaskID) != selectedID {
			continue
		}
		count++
	}
	return count
}

func hasCeremonySummary(events []store.PlanningTraceEvent, cycle int, selectedID string) bool {
	selectedID = strings.TrimSpace(selectedID)
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Cycle != cycle {
			continue
		}
		if selectedID != "" && strings.TrimSpace(ev.TaskID) != selectedID && strings.TrimSpace(ev.OptionID) != selectedID {
			continue
		}
		typ := strings.TrimSpace(ev.EventType)
		if typ == "plan_summary_result" || (strings.TrimSpace(ev.Stage) == "summarize_plan" && typ == "tool_result") {
			return true
		}
	}
	return false
}

func latestCeremonySummaryPrompt(events []store.PlanningTraceEvent, cycle int, selectedID string) string {
	selectedID = strings.TrimSpace(selectedID)
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Cycle != cycle {
			continue
		}
		if selectedID != "" && strings.TrimSpace(ev.TaskID) != selectedID && strings.TrimSpace(ev.OptionID) != selectedID {
			continue
		}
		if strings.TrimSpace(ev.EventType) != "plan_summary_result" {
			continue
		}
		what := strings.TrimSpace(ev.SummaryText)
		if what != "" {
			return "Greenlight this plan: " + what
		}
	}
	return "Greenlight this plan now, or realign for another planning cycle."
}

func latestCeremonyCandidates(events []store.PlanningTraceEvent, cycle int) []ceremonyCandidateOption {
	type candidateAccum struct {
		option ceremonyCandidateOption
		seen   bool
	}
	acc := make(map[string]candidateAccum)
	for i := range events {
		ev := events[i]
		if ev.Cycle != cycle {
			continue
		}
		typ := strings.TrimSpace(ev.EventType)
		if typ != "candidate_ranked" && typ != "candidate_pruned" && typ != "candidate_with_implications" {
			continue
		}
		id := strings.TrimSpace(ev.OptionID)
		if id == "" {
			id = strings.TrimSpace(ev.TaskID)
		}
		if id == "" {
			continue
		}

		rec := acc[id]
		rec.option.ID = id
		if title := ceremonyCandidateTitle(strings.TrimSpace(ev.SummaryText)); title != "" {
			rec.option.Title = title
		}
		var meta map[string]any
		if strings.TrimSpace(ev.MetadataJSON) != "" && strings.TrimSpace(ev.MetadataJSON) != "{}" {
			_ = json.Unmarshal([]byte(ev.MetadataJSON), &meta)
		}
		if rank := ceremonyMetaInt(meta, "rank"); rank > 0 {
			rec.option.Rank = rank
		}
		if ceremonyMetaBool(meta, "shortlisted") {
			rec.option.Shortlisted = true
		}
		if ceremonyMetaBool(meta, "recommended") {
			rec.option.Recommended = true
		}
		if rec.option.Rank <= 0 {
			rec.option.Rank = ceremonyExtractRank(strings.TrimSpace(ev.SummaryText))
		}
		if typ == "candidate_ranked" {
			rec.option.Shortlisted = true
		}
		rec.seen = true
		acc[id] = rec
	}

	candidates := make([]ceremonyCandidateOption, 0, len(acc))
	for _, rec := range acc {
		if !rec.seen {
			continue
		}
		if strings.TrimSpace(rec.option.Title) == "" {
			rec.option.Title = rec.option.ID
		}
		candidates = append(candidates, rec.option)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Rank != candidates[j].Rank {
			if candidates[i].Rank == 0 {
				return false
			}
			if candidates[j].Rank == 0 {
				return true
			}
			return candidates[i].Rank < candidates[j].Rank
		}
		return candidates[i].ID < candidates[j].ID
	})
	return candidates
}

func formatCeremonyCandidateOptions(candidates []ceremonyCandidateOption) []string {
	if len(candidates) == 0 {
		return nil
	}
	hasShortlist := false
	for i := range candidates {
		if candidates[i].Shortlisted {
			hasShortlist = true
			break
		}
	}

	out := make([]string, 0, len(candidates))
	for i := range candidates {
		c := candidates[i]
		if hasShortlist && !c.Shortlisted {
			continue
		}
		label := c.Title
		if c.Rank > 0 {
			label = fmt.Sprintf("#%d %s", c.Rank, label)
		}
		option := fmt.Sprintf("%s (%s)", c.ID, label)
		if c.Recommended {
			option += " [recommended]"
		}
		out = append(out, option)
	}
	if len(out) > 0 {
		return out
	}
	for i := range candidates {
		c := candidates[i]
		out = append(out, fmt.Sprintf("%s (%s)", c.ID, c.Title))
	}
	return out
}

func ceremonyCandidateTitle(summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	if strings.HasPrefix(summary, "#") {
		parts := strings.SplitN(summary, " ", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(parts[1])
		}
	}
	if idx := strings.LastIndex(summary, "(rank "); idx > 0 {
		return strings.TrimSpace(summary[:idx])
	}
	return summary
}

func ceremonyExtractRank(summary string) int {
	summary = strings.TrimSpace(summary)
	if !strings.HasPrefix(summary, "#") {
		return 0
	}
	parts := strings.SplitN(strings.TrimPrefix(summary, "#"), " ", 2)
	if len(parts) == 0 {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0
	}
	return n
}

func ceremonyMetaInt(meta map[string]any, key string) int {
	if meta == nil {
		return 0
	}
	raw, ok := meta[key]
	if !ok {
		return 0
	}
	switch v := raw.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	default:
		return 0
	}
}

func ceremonyMetaBool(meta map[string]any, key string) bool {
	if meta == nil {
		return false
	}
	raw, ok := meta[key]
	if !ok {
		return false
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

func ceremonyStageForSignal(signal string) string {
	switch strings.TrimSpace(signal) {
	case "item-selected":
		return "selection"
	case "answer":
		return "question_answer"
	case "greenlight":
		return "greenlight"
	default:
		return "control_signal"
	}
}

func inferCeremonyProject(st *store.Store, sessionID string) string {
	if st == nil || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	events, err := st.ListPlanningTraceEvents(sessionID, 1)
	if err != nil || len(events) == 0 {
		return ""
	}
	return strings.TrimSpace(events[0].Project)
}

func recordCeremonyControlTrace(
	st *store.Store,
	sessionID, project, stage, eventType, summary, fullText string,
	metadata map[string]any,
) {

	if st == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	eventType = strings.TrimSpace(eventType)
	if sessionID == "" || eventType == "" {
		return
	}
	metaJSON := "{}"
	if metadata != nil {
		if b, err := json.Marshal(metadata); err == nil {
			metaJSON = string(b)
		}
	}
	_ = st.RecordPlanningTraceEvent(store.PlanningTraceEvent{
		SessionID:    sessionID,
		Project:      strings.TrimSpace(project),
		Stage:        strings.TrimSpace(stage),
		EventType:    eventType,
		Actor:        "cli-ceremony",
		SummaryText:  strings.TrimSpace(summary),
		FullText:     fullText,
		MetadataJSON: metaJSON,
	})
}
