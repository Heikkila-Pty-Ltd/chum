package temporal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	maxPlanningCycles             = 5
	defaultPlanningCandidateTopK  = 5
	maxPlanningCandidateTopK      = 20
	defaultPlanningSignalTimeout  = 10 * time.Minute
	defaultPlanningSessionTimeout = 30 * time.Minute
	planningSelectedPenaltyDelta  = -12.0
	planningAgreedRewardDelta     = 10.0
	maxPlanningNovelPathways      = 4
)

type planningGateResult struct {
	Passed  bool
	Code    string
	Message string
}

type planningGraphTracker struct {
	nextID  int
	current string
}

func newPlanningGraphTracker() *planningGraphTracker {
	return &planningGraphTracker{current: "root"}
}

func (g *planningGraphTracker) nextNode(prefix string) string {
	g.nextID++
	return fmt.Sprintf("%s-%03d", prefix, g.nextID)
}

// PlanningCeremonyWorkflow implements interactive sprint planning.
//
// Planning happens BEFORE any code is written. The chief grooms the backlog,
// presents options one at a time, asks sequential clarifying questions (each
// depending on the previous answer), then summarizes what/why/effort.
// Only after greenlight does it produce a TaskRequest for the execution workflow.
//
// Supports up to 5 planning cycles — iterate, improve, explore ideas, find best options.
// Nothing goes to the sharks until it's chum.
func PlanningCeremonyWorkflow(ctx workflow.Context, req PlanningRequest) (*TaskRequest, error) {
	logger := workflow.GetLogger(ctx)

	if req.Agent == "" {
		req.Agent = "claude"
	}
	if req.Tier == "" {
		req.Tier = "fast"
	}
	if req.SlowStepThreshold <= 0 {
		req.SlowStepThreshold = defaultSlowStepThreshold
	}
	if strings.TrimSpace(req.TraceSessionID) == "" {
		req.TraceSessionID = workflow.GetInfo(ctx).WorkflowExecution.ID
	}
	req.CandidateTopK = normalizePlanningCandidateTopK(req.CandidateTopK)
	req.SignalTimeout = normalizePlanningSignalTimeout(req.SignalTimeout)
	req.SessionTimeout = normalizePlanningSessionTimeout(req.SessionTimeout)
	sessionStartedAt := workflow.Now(ctx)

	graph := newPlanningGraphTracker()
	candidateScoreAdjustments := make(map[string]float64)
	var lastRankedCandidates []planningCandidate
	lastSelectedID := ""
	lastBranchID := "planning-final"
	recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
		Stage:       "planning",
		NodeID:      "root",
		EventType:   "planning_started",
		SummaryText: "Planning ceremony started",
		FullText:    fmt.Sprintf("project=%s agent=%s tier=%s", req.Project, req.Agent, req.Tier),
	})

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var a *Activities

	for cycle := 0; cycle < maxPlanningCycles; cycle++ {
		req.TraceCycle = cycle + 1
		branchID := fmt.Sprintf("cycle-%d", req.TraceCycle)
		lastBranchID = branchID
		if hasPlanningSessionTimedOut(req, sessionStartedAt, workflow.Now(ctx)) {
			return nil, failPlanningSignalTimeout(
				ctx,
				req,
				graph,
				branchID,
				"planning",
				"session-timeout",
				"",
				"",
				nil,
				fmt.Sprintf("session exceeded %s before cycle %d started", req.SessionTimeout, req.TraceCycle),
			)
		}
		logger.Info("Planning cycle", "Cycle", req.TraceCycle, "MaxCycles", maxPlanningCycles)

		// ===== PHASE 1: GROOM BACKLOG =====
		logger.Info("Planning: grooming backlog", "Project", req.Project)
		var backlog BacklogPresentation
		if seeded, ok := seedPlanningBacklogFromRequest(req); ok {
			backlog = seeded
			seedNode := graph.nextNode("seed")
			recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
				TaskID:         strings.TrimSpace(req.SeedTaskID),
				Stage:          "groom_backlog",
				NodeID:         seedNode,
				ParentNodeID:   graph.current,
				BranchID:       branchID,
				OptionID:       strings.TrimSpace(req.SeedTaskID),
				EventType:      "seed_backlog_loaded",
				SummaryText:    fmt.Sprintf("seeded planning backlog with task %s", strings.TrimSpace(req.SeedTaskID)),
				FullText:       fmt.Sprintf("seed_task_id=%s\nauto_mode=%t", strings.TrimSpace(req.SeedTaskID), req.AutoMode),
				SelectedOption: strings.TrimSpace(req.SeedTaskID),
			})
		} else {
			callNode := graph.nextNode("tool")
			recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
				Stage:        "groom_backlog",
				NodeID:       callNode,
				ParentNodeID: graph.current,
				BranchID:     branchID,
				EventType:    "tool_call",
				ToolName:     "GroomBacklogActivity",
				SummaryText:  "Invoking backlog grooming activity",
				FullText:     "tool=GroomBacklogActivity",
			})
			if err := workflow.ExecuteActivity(ctx, a.GroomBacklogActivity, req).Get(ctx, &backlog); err != nil {
				reviewPlanningCeremonyOutcome(ctx, req, graph, branchID, "error_backlog_grooming", "", nil)
				return nil, fmt.Errorf("backlog grooming failed: %w", err)
			}

			recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
				Stage:        "groom_backlog",
				NodeID:       graph.nextNode("tool"),
				ParentNodeID: callNode,
				BranchID:     branchID,
				EventType:    "tool_result",
				ToolName:     "GroomBacklogActivity",
				SummaryText:  fmt.Sprintf("Backlog items returned: %d", len(backlog.Items)),
				FullText:     fmt.Sprintf("items=%d", len(backlog.Items)),
			})
		}

		if gate := validateBacklogGate(backlog); !gate.Passed {
			handlePlanningGateFailure(ctx, req, graph, branchID, "groom_backlog", gate, "", "", "")
			continue
		}

		backlogNode := graph.nextNode("backlog")
		recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
			Stage:        "groom_backlog",
			NodeID:       backlogNode,
			ParentNodeID: graph.current,
			BranchID:     branchID,
			EventType:    "gate_pass",
			SummaryText:  "Backlog gate passed",
			FullText:     "gate=backlog",
		})

		hydratePlanningCandidateScoreAdjustments(ctx, req, backlog.Items, candidateScoreAdjustments)

		goalSummary, goalFullText := interpretPlanningGoal(backlog)
		recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
			Stage:        "groom_backlog",
			NodeID:       graph.nextNode("goal"),
			ParentNodeID: backlogNode,
			BranchID:     branchID,
			EventType:    "goal_interpreted",
			SummaryText:  goalSummary,
			FullText:     goalFullText,
		})

		rankedCandidates := rankPlanningCandidates(backlog.Items, req.CandidateTopK, candidateScoreAdjustments)
		lastRankedCandidates = rankedCandidates
		shortlistedIDs := make([]string, 0, len(rankedCandidates))
		for i := range rankedCandidates {
			if rankedCandidates[i].Shortlisted {
				shortlistedIDs = append(shortlistedIDs, rankedCandidates[i].Item.ID)
			}
		}

		bestCandidateID := ""
		if len(rankedCandidates) > 0 {
			bestCandidateID = rankedCandidates[0].Item.ID
		}
		recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
			Stage:        "groom_backlog",
			NodeID:       graph.nextNode("candidate"),
			ParentNodeID: backlogNode,
			BranchID:     branchID,
			EventType:    "candidate_set_evaluated",
			SummaryText:  fmt.Sprintf("ranked %d candidates, shortlisted %d", len(rankedCandidates), len(shortlistedIDs)),
			FullText:     fmt.Sprintf("best_candidate=%s shortlisted=%s", bestCandidateID, strings.Join(shortlistedIDs, ",")),
			MetadataJSON: mustJSON(map[string]any{
				"top_k":          req.CandidateTopK,
				"best_candidate": bestCandidateID,
				"shortlisted":    shortlistedIDs,
			}),
		})

		for i := range rankedCandidates {
			candidate := rankedCandidates[i]
			item := candidate.Item

			eventType := "candidate_ranked"
			if !candidate.Shortlisted {
				eventType = "candidate_pruned"
			}

			recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
				TaskID:         item.ID,
				Stage:          "groom_backlog",
				NodeID:         graph.nextNode("option"),
				ParentNodeID:   backlogNode,
				BranchID:       fmt.Sprintf("%s-option-%s", branchID, item.ID),
				OptionID:       item.ID,
				EventType:      eventType,
				SummaryText:    fmt.Sprintf("#%d %s", candidate.Rank, item.Title),
				FullText:       fmt.Sprintf("impact=%s\neffort=%s\nrecommended=%t\nscore=%.2f\nshortlisted=%t\nrationale=%s", item.Impact, item.Effort, item.Recommended, candidate.Score, candidate.Shortlisted, item.Rationale),
				MetadataJSON:   candidateMetadataJSON(candidate),
				SelectedOption: "",
			})

			recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
				TaskID:         item.ID,
				Stage:          "groom_backlog",
				NodeID:         graph.nextNode("option"),
				ParentNodeID:   backlogNode,
				BranchID:       fmt.Sprintf("%s-option-%s", branchID, item.ID),
				OptionID:       item.ID,
				EventType:      "candidate_with_implications",
				SummaryText:    fmt.Sprintf("%s (rank %d)", item.Title, candidate.Rank),
				FullText:       candidateImplicationsText(candidate),
				MetadataJSON:   candidateStatusMetadataJSON(candidate, "implications"),
				SelectedOption: "",
			})
		}

		if stateJSON, stateHash := encodePlanningState(PlanningSnapshotState{
			Cycle: req.TraceCycle, Stage: "groom_backlog",
		}); stateHash != "" {
			recordPlanningSnapshot(ctx, req, PlanningSnapshotRecord{
				Project:   req.Project,
				Cycle:     req.TraceCycle,
				Stage:     "groom_backlog",
				StateHash: stateHash,
				StateJSON: stateJSON,
				Stable:    true,
				Reason:    "backlog_gate_passed",
			})
		}
		graph.current = backlogNode

		// ===== PHASE 2: ITEM SELECTION =====
		logger.Info("Planning: waiting for item selection")
		var selectedID string
		if req.AutoMode {
			selectedID = chooseAutoPlanningSelection(req, rankedCandidates)
			recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
				TaskID:       selectedID,
				Stage:        "selection",
				NodeID:       graph.nextNode("select"),
				ParentNodeID: graph.current,
				BranchID:     branchID,
				EventType:    "auto_selection_applied",
				SummaryText:  "auto selection applied",
				FullText:     fmt.Sprintf("selected_id=%s", selectedID),
				Reward:       0,
			})
		} else {
			waitFor := planningWaitTimeout(req, sessionStartedAt, workflow.Now(ctx))
			selection, ok := waitForPlanningSignal(ctx, "item-selected", waitFor)
			if !ok {
				return nil, failPlanningSignalTimeout(
					ctx,
					req,
					graph,
					branchID,
					"selection",
					"item-selected",
					"",
					"",
					rankedCandidates,
					fmt.Sprintf("timed out after %s waiting for item-selected signal", waitFor),
				)
			}
			selectedID = selection
		}
		selectedID = strings.TrimSpace(selectedID)

		selectionStateJSON, selectionStateHash := encodePlanningState(PlanningSnapshotState{
			Cycle:          req.TraceCycle,
			Stage:          "selection",
			SelectedOption: selectedID,
		})
		selectionActionHash := hashPlanningAction(selectedID)

		if isPlanningActionBlacklisted(ctx, req, selectionStateHash, selectionActionHash) {
			recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
				Stage:        "selection",
				NodeID:       graph.nextNode("select"),
				ParentNodeID: graph.current,
				BranchID:     branchID,
				EventType:    "blacklist_blocked",
				SummaryText:  "Selection blocked by blacklist",
				FullText:     fmt.Sprintf("selected_id=%s", selectedID),
				Reward:       0,
			})
			recordPlanningSnapshot(ctx, req, PlanningSnapshotRecord{
				Project:   req.Project,
				Cycle:     req.TraceCycle,
				Stage:     "selection",
				StateHash: selectionStateHash,
				StateJSON: selectionStateJSON,
				Stable:    false,
				Reason:    "selection_blacklisted",
			})
			reviewAndProposeAlternatives(ctx, req, graph, branchID, "selection_blacklisted", selectedID, rankedCandidates, candidateScoreAdjustments)
			rollbackPlanningState(ctx, req, graph, branchID, "selection_blacklisted")
			continue
		}

		selectedItem, found := findBacklogItem(backlog, selectedID)
		if gate := validateSelectionGate(selectedID, found); !gate.Passed {
			addPlanningBlacklistEntry(ctx, req, PlanningBlacklistEntryRecord{
				Project:    req.Project,
				TaskID:     selectedID,
				Cycle:      req.TraceCycle,
				Stage:      "selection",
				StateHash:  selectionStateHash,
				ActionHash: selectionActionHash,
				Reason:     gate.Code,
				Metadata:   fmt.Sprintf(`{"message":"%s"}`, gate.Message),
			})
			reviewAndProposeAlternatives(ctx, req, graph, branchID, "selection_"+gate.Code, selectedID, rankedCandidates, candidateScoreAdjustments)
			handlePlanningGateFailure(ctx, req, graph, branchID, "selection", gate, selectedID, selectionStateHash, selectionActionHash)
			continue
		}

		selectedNode := graph.nextNode("select")
		recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
			TaskID:         selectedItem.ID,
			Stage:          "selection",
			NodeID:         selectedNode,
			ParentNodeID:   graph.current,
			BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
			OptionID:       selectedItem.ID,
			EventType:      "item_selected",
			SummaryText:    selectedItem.Title,
			FullText:       fmt.Sprintf("selected_id=%s title=%s", selectedID, selectedItem.Title),
			SelectedOption: selectedID,
		})
		lastSelectedID = selectedID
		graph.current = selectedNode

		for i := range rankedCandidates {
			candidate := rankedCandidates[i]
			if candidate.Item.ID == selectedItem.ID {
				status := "selected"
				selectionFullText := fmt.Sprintf("selected candidate score=%.2f shortlisted=%t", candidate.Score, candidate.Shortlisted)
				if !candidate.Shortlisted {
					status = "selected_non_shortlisted"
					selectionFullText = fmt.Sprintf("selected non-shortlisted candidate score=%.2f", candidate.Score)
				}
				recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
					TaskID:         candidate.Item.ID,
					Stage:          "selection",
					NodeID:         graph.nextNode("select"),
					ParentNodeID:   selectedNode,
					BranchID:       fmt.Sprintf("%s-option-%s", branchID, candidate.Item.ID),
					OptionID:       candidate.Item.ID,
					EventType:      "candidate_selected",
					SummaryText:    fmt.Sprintf("#%d %s", candidate.Rank, candidate.Item.Title),
					FullText:       selectionFullText,
					MetadataJSON:   candidateStatusMetadataJSON(candidate, status),
					SelectedOption: selectedID,
				})
				continue
			}
			if !candidate.Shortlisted {
				continue
			}
			recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
				TaskID:         candidate.Item.ID,
				Stage:          "selection",
				NodeID:         graph.nextNode("select"),
				ParentNodeID:   selectedNode,
				BranchID:       fmt.Sprintf("%s-option-%s", branchID, candidate.Item.ID),
				OptionID:       candidate.Item.ID,
				EventType:      "candidate_deferred",
				SummaryText:    fmt.Sprintf("#%d %s", candidate.Rank, candidate.Item.Title),
				FullText:       fmt.Sprintf("deferred candidate score=%.2f shortlisted=%t", candidate.Score, candidate.Shortlisted),
				MetadataJSON:   candidateStatusMetadataJSON(candidate, "deferred"),
				SelectedOption: selectedID,
			})
		}

		if stateJSON, stateHash := encodePlanningState(PlanningSnapshotState{
			Cycle:          req.TraceCycle,
			Stage:          "selection",
			SelectedID:     selectedItem.ID,
			SelectedTitle:  selectedItem.Title,
			SelectedOption: selectedID,
		}); stateHash != "" {
			recordPlanningSnapshot(ctx, req, PlanningSnapshotRecord{
				Project:   req.Project,
				TaskID:    selectedItem.ID,
				Cycle:     req.TraceCycle,
				Stage:     "selection",
				StateHash: stateHash,
				StateJSON: stateJSON,
				Stable:    true,
				Reason:    "selection_gate_passed",
			})
		}

		// ===== PHASE 3: SEQUENTIAL QUESTIONS =====
		questionsStateJSON, questionsStateHash := encodePlanningState(PlanningSnapshotState{
			Cycle:          req.TraceCycle,
			Stage:          "generate_questions",
			SelectedID:     selectedItem.ID,
			SelectedTitle:  selectedItem.Title,
			SelectedOption: selectedID,
		})
		questionsActionHash := hashPlanningAction("generate_questions")
		if isPlanningActionBlacklisted(ctx, req, questionsStateHash, questionsActionHash) {
			recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
				TaskID:         selectedItem.ID,
				Stage:          "generate_questions",
				NodeID:         graph.nextNode("tool"),
				ParentNodeID:   graph.current,
				BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
				OptionID:       selectedItem.ID,
				EventType:      "blacklist_blocked",
				SummaryText:    "Question generation blocked by blacklist",
				FullText:       fmt.Sprintf("state_hash=%s action_hash=%s", questionsStateHash, questionsActionHash),
				Reward:         0,
				SelectedOption: selectedID,
			})
			recordPlanningSnapshot(ctx, req, PlanningSnapshotRecord{
				Project:   req.Project,
				TaskID:    selectedItem.ID,
				Cycle:     req.TraceCycle,
				Stage:     "generate_questions",
				StateHash: questionsStateHash,
				StateJSON: questionsStateJSON,
				Stable:    false,
				Reason:    "questions_blacklisted",
			})
			reviewAndProposeAlternatives(ctx, req, graph, branchID, "questions_blacklisted", selectedItem.ID, rankedCandidates, candidateScoreAdjustments)
			rollbackPlanningState(ctx, req, graph, branchID, "questions_blacklisted")
			continue
		}

		callNode := graph.nextNode("tool")
		recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
			TaskID:         selectedItem.ID,
			Stage:          "generate_questions",
			NodeID:         callNode,
			ParentNodeID:   graph.current,
			BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
			OptionID:       selectedItem.ID,
			EventType:      "tool_call",
			ToolName:       "GenerateQuestionsActivity",
			SummaryText:    "Invoking question generation activity",
			SelectedOption: selectedID,
		})

		var questions []PlanningQuestion
		if err := workflow.ExecuteActivity(ctx, a.GenerateQuestionsActivity, req, *selectedItem).Get(ctx, &questions); err != nil {
			reviewPlanningCeremonyOutcome(ctx, req, graph, branchID, "error_generate_questions", selectedID, rankedCandidates)
			return nil, fmt.Errorf("question generation failed: %w", err)
		}

		recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
			TaskID:         selectedItem.ID,
			Stage:          "generate_questions",
			NodeID:         graph.nextNode("tool"),
			ParentNodeID:   callNode,
			BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
			OptionID:       selectedItem.ID,
			EventType:      "tool_result",
			ToolName:       "GenerateQuestionsActivity",
			SummaryText:    fmt.Sprintf("Questions generated: %d", len(questions)),
			FullText:       fmt.Sprintf("count=%d", len(questions)),
			SelectedOption: selectedID,
		})

		if gate := validateQuestionsGate(questions); !gate.Passed {
			addPlanningBlacklistEntry(ctx, req, PlanningBlacklistEntryRecord{
				Project:    req.Project,
				TaskID:     selectedItem.ID,
				Cycle:      req.TraceCycle,
				Stage:      "generate_questions",
				StateHash:  questionsStateHash,
				ActionHash: questionsActionHash,
				Reason:     gate.Code,
				Metadata:   fmt.Sprintf(`{"message":"%s"}`, gate.Message),
			})
			recordPlanningSnapshot(ctx, req, PlanningSnapshotRecord{
				Project:   req.Project,
				TaskID:    selectedItem.ID,
				Cycle:     req.TraceCycle,
				Stage:     "generate_questions",
				StateHash: questionsStateHash,
				StateJSON: questionsStateJSON,
				Stable:    false,
				Reason:    gate.Code,
			})
			reviewAndProposeAlternatives(ctx, req, graph, branchID, "questions_"+gate.Code, selectedItem.ID, rankedCandidates, candidateScoreAdjustments)
			handlePlanningGateFailure(ctx, req, graph, branchID, "generate_questions", gate, selectedItem.ID, questionsStateHash, questionsActionHash)
			continue
		}

		answers := make(map[string]string)

		for i := range questions {
			q := &questions[i]
			q.Number = i + 1
			q.Total = len(questions)

			if i > 0 {
				prevA := answers[strconv.Itoa(i)]
				q.Context = fmt.Sprintf("Based on Q%d answer: %s", i, prevA)
			}

			logger.Info("Planning: question", "N", q.Number, "Of", q.Total, "Q", q.Question)

			var answer string
			if req.AutoMode {
				answer = autoPlanningAnswer(*q)
				recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
					TaskID:         selectedItem.ID,
					Stage:          "question_answer",
					NodeID:         graph.nextNode("answer"),
					ParentNodeID:   graph.current,
					BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
					OptionID:       selectedItem.ID,
					EventType:      "auto_answer_applied",
					SummaryText:    q.Question,
					FullText:       fmt.Sprintf("question=%s\nauto_answer=%s", q.Question, answer),
					SelectedOption: selectedID,
				})
			} else {
				waitFor := planningWaitTimeout(req, sessionStartedAt, workflow.Now(ctx))
				reply, ok := waitForPlanningSignal(ctx, "answer", waitFor)
				if !ok {
					return nil, failPlanningSignalTimeout(
						ctx,
						req,
						graph,
						branchID,
						"question_answer",
						"answer",
						selectedItem.ID,
						selectedID,
						rankedCandidates,
						fmt.Sprintf("timed out after %s waiting for answer signal for question %d", waitFor, q.Number),
					)
				}
				answer = reply
			}
			answers[strconv.Itoa(i+1)] = answer

			recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
				TaskID:         selectedItem.ID,
				Stage:          "question_answer",
				NodeID:         graph.nextNode("answer"),
				ParentNodeID:   graph.current,
				BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
				OptionID:       selectedItem.ID,
				EventType:      "answer_recorded",
				SummaryText:    q.Question,
				FullText:       fmt.Sprintf("question=%s\nanswer=%s", q.Question, answer),
				SelectedOption: selectedID,
			})
		}

		if stateJSON, stateHash := encodePlanningState(PlanningSnapshotState{
			Cycle:          req.TraceCycle,
			Stage:          "question_answer",
			SelectedID:     selectedItem.ID,
			SelectedTitle:  selectedItem.Title,
			SelectedOption: selectedID,
			Answers:        answers,
		}); stateHash != "" {
			recordPlanningSnapshot(ctx, req, PlanningSnapshotRecord{
				Project:   req.Project,
				TaskID:    selectedItem.ID,
				Cycle:     req.TraceCycle,
				Stage:     "question_answer",
				StateHash: stateHash,
				StateJSON: stateJSON,
				Stable:    true,
				Reason:    "questions_gate_passed",
			})
		}

		// ===== PHASE 4: SUMMARY =====
		callNode = graph.nextNode("tool")
		recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
			TaskID:         selectedItem.ID,
			Stage:          "summarize_plan",
			NodeID:         callNode,
			ParentNodeID:   graph.current,
			BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
			OptionID:       selectedItem.ID,
			EventType:      "tool_call",
			ToolName:       "SummarizePlanActivity",
			SummaryText:    "Invoking plan summarization activity",
			SelectedOption: selectedID,
		})

		var summary PlanSummary
		if err := workflow.ExecuteActivity(ctx, a.SummarizePlanActivity, req, *selectedItem, answers).Get(ctx, &summary); err != nil {
			reviewPlanningCeremonyOutcome(ctx, req, graph, branchID, "error_summarize_plan", selectedID, rankedCandidates)
			return nil, fmt.Errorf("plan summary failed: %w", err)
		}

		recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
			TaskID:         selectedItem.ID,
			Stage:          "summarize_plan",
			NodeID:         graph.nextNode("tool"),
			ParentNodeID:   callNode,
			BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
			OptionID:       selectedItem.ID,
			EventType:      "tool_result",
			ToolName:       "SummarizePlanActivity",
			SummaryText:    summary.What,
			FullText:       fmt.Sprintf("why=%s\neffort=%s", summary.Why, summary.Effort),
			SelectedOption: selectedID,
		})

		if gate := validateSummaryGate(summary); !gate.Passed {
			stateJSON, stateHash := encodePlanningState(PlanningSnapshotState{
				Cycle:          req.TraceCycle,
				Stage:          "summarize_plan",
				SelectedID:     selectedItem.ID,
				SelectedTitle:  selectedItem.Title,
				SelectedOption: selectedID,
				Answers:        answers,
			})
			actionHash := hashPlanningAction(summary.What + "|" + strings.Join(summary.DoDChecks, "|"))
			addPlanningBlacklistEntry(ctx, req, PlanningBlacklistEntryRecord{
				Project:    req.Project,
				TaskID:     selectedItem.ID,
				Cycle:      req.TraceCycle,
				Stage:      "summarize_plan",
				StateHash:  stateHash,
				ActionHash: actionHash,
				Reason:     gate.Code,
				Metadata:   fmt.Sprintf(`{"message":"%s"}`, gate.Message),
			})
			recordPlanningSnapshot(ctx, req, PlanningSnapshotRecord{
				Project:   req.Project,
				TaskID:    selectedItem.ID,
				Cycle:     req.TraceCycle,
				Stage:     "summarize_plan",
				StateHash: stateHash,
				StateJSON: stateJSON,
				Stable:    false,
				Reason:    gate.Code,
			})
			reviewAndProposeAlternatives(ctx, req, graph, branchID, "summary_"+gate.Code, selectedItem.ID, rankedCandidates, candidateScoreAdjustments)
			handlePlanningGateFailure(ctx, req, graph, branchID, "summarize_plan", gate, selectedItem.ID, stateHash, actionHash)
			continue
		}

		if stateJSON, stateHash := encodePlanningState(PlanningSnapshotState{
			Cycle:          req.TraceCycle,
			Stage:          "summary",
			SelectedID:     selectedItem.ID,
			SelectedTitle:  selectedItem.Title,
			SelectedOption: selectedID,
			Answers:        answers,
			SummaryWhat:    summary.What,
			SummaryWhy:     summary.Why,
			SummaryEffort:  summary.Effort,
		}); stateHash != "" {
			recordPlanningSnapshot(ctx, req, PlanningSnapshotRecord{
				Project:   req.Project,
				TaskID:    selectedItem.ID,
				Cycle:     req.TraceCycle,
				Stage:     "summary",
				StateHash: stateHash,
				StateJSON: stateJSON,
				Stable:    true,
				Reason:    "summary_gate_passed",
			})
		}

		behaviorContract := buildPlanningBehaviorContract(*selectedItem, summary, answers)
		recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
			TaskID:         selectedItem.ID,
			Stage:          "summary",
			NodeID:         graph.nextNode("contract"),
			ParentNodeID:   graph.current,
			BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
			OptionID:       selectedItem.ID,
			EventType:      "behavior_contract",
			SummaryText:    behaviorContract.OptimalSlice,
			FullText:       behaviorContractToText(behaviorContract),
			MetadataJSON:   mustJSON(behaviorContract),
			SelectedOption: selectedID,
		})

		loopIterate, loopReason := shouldIteratePlanningLoop(req.TraceCycle, *selectedItem, summary)
		loopDecision := "proceed"
		if loopIterate {
			loopDecision = "iterate"
		}
		recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
			TaskID:         selectedItem.ID,
			Stage:          "summary",
			NodeID:         graph.nextNode("loop"),
			ParentNodeID:   graph.current,
			BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
			OptionID:       selectedItem.ID,
			EventType:      "loop_decision",
			SummaryText:    loopDecision,
			FullText:       fmt.Sprintf("decision=%s reason=%s cycle=%d", loopDecision, loopReason, req.TraceCycle),
			MetadataJSON:   mustJSON(map[string]any{"decision": loopDecision, "reason": loopReason, "cycle": req.TraceCycle}),
			SelectedOption: selectedID,
		})
		if loopIterate {
			if req.AutoMode {
				recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
					TaskID:         selectedItem.ID,
					Stage:          "summary",
					NodeID:         graph.nextNode("loop"),
					ParentNodeID:   graph.current,
					BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
					OptionID:       selectedItem.ID,
					EventType:      "loop_iteration_skipped_auto",
					SummaryText:    "auto mode bypassed iterative recycle",
					FullText:       fmt.Sprintf("reason=%s", loopReason),
					Reward:         0,
					SelectedOption: selectedID,
				})
			} else {
				recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
					TaskID:         selectedItem.ID,
					Stage:          "summary",
					NodeID:         graph.nextNode("loop"),
					ParentNodeID:   graph.current,
					BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
					OptionID:       selectedItem.ID,
					EventType:      "plan_recycle",
					SummaryText:    "iterating planning loop for further refinement",
					FullText:       fmt.Sprintf("reason=%s", loopReason),
					Reward:         0,
					SelectedOption: selectedID,
				})
				reviewAndProposeAlternatives(ctx, req, graph, branchID, "loop_iterate_"+loopReason, selectedItem.ID, rankedCandidates, candidateScoreAdjustments)
				continue
			}
		}

		// ===== PHASE 5: GREENLIGHT =====
		logger.Info("Planning: waiting for greenlight", "Cycle", req.TraceCycle)
		var decision string
		if req.AutoMode {
			decision = "GO"
			recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
				TaskID:         selectedItem.ID,
				Stage:          "greenlight",
				NodeID:         graph.nextNode("decision"),
				ParentNodeID:   graph.current,
				BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
				OptionID:       selectedItem.ID,
				EventType:      "auto_greenlight_applied",
				SummaryText:    decision,
				FullText:       "decision=GO",
				Reward:         1.0,
				SelectedOption: selectedID,
			})
		} else {
			waitFor := planningWaitTimeout(req, sessionStartedAt, workflow.Now(ctx))
			reply, ok := waitForPlanningSignal(ctx, "greenlight", waitFor)
			if !ok {
				return nil, failPlanningSignalTimeout(
					ctx,
					req,
					graph,
					branchID,
					"greenlight",
					"greenlight",
					selectedItem.ID,
					selectedID,
					rankedCandidates,
					fmt.Sprintf("timed out after %s waiting for greenlight signal", waitFor),
				)
			}
			decision = reply
		}

		reward := 0.0
		if strings.EqualFold(strings.TrimSpace(decision), "GO") {
			reward = 1.0
		}
		recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
			TaskID:         selectedItem.ID,
			Stage:          "greenlight",
			NodeID:         graph.nextNode("decision"),
			ParentNodeID:   graph.current,
			BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
			OptionID:       selectedItem.ID,
			EventType:      "greenlight_decision",
			SummaryText:    decision,
			FullText:       fmt.Sprintf("decision=%s", decision),
			Reward:         reward,
			SelectedOption: selectedID,
		})

		if strings.EqualFold(strings.TrimSpace(decision), "GO") {
			taskReq := &TaskRequest{
				TaskID:            selectedItem.ID,
				Project:           req.Project,
				Prompt:            summary.What,
				TaskTitle:         selectedItem.Title,
				Agent:             req.Agent,
				Reviewer:          DefaultReviewer(req.Agent),
				WorkDir:           req.WorkDir,
				Priority:          2,
				DoDChecks:         summary.DoDChecks,
				SlowStepThreshold: req.SlowStepThreshold,
			}

			recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
				TaskID:         selectedItem.ID,
				Stage:          "greenlight",
				NodeID:         graph.nextNode("agreed"),
				ParentNodeID:   graph.current,
				BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
				OptionID:       selectedItem.ID,
				EventType:      "plan_agreed",
				SummaryText:    summary.What,
				FullText:       fmt.Sprintf("agreed_task=%s effort=%s", selectedItem.Title, summary.Effort),
				Reward:         1.0,
				SelectedOption: selectedID,
			})
			applyPlanningScoreAdjustment(candidateScoreAdjustments, selectedID, planningAgreedRewardDelta)
			persistPlanningCandidateScoreAdjustment(ctx, req, PlanningCandidateScoreDelta{
				OptionID: selectedID,
				Delta:    planningAgreedRewardDelta,
				Outcome:  "success",
				Reason:   "plan_agreed",
			})
			reviewPlanningCeremonyOutcome(ctx, req, graph, branchID, "plan_agreed", selectedID, rankedCandidates)

			planMarkdown := buildPlanningArtifactMarkdown(req, *selectedItem, summary, answers)
			recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
				TaskID:         selectedItem.ID,
				Stage:          "greenlight",
				NodeID:         graph.nextNode("artifact"),
				ParentNodeID:   graph.current,
				BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
				OptionID:       selectedItem.ID,
				EventType:      "planning_artifact_emitted",
				SummaryText:    selectedItem.Title,
				FullText:       planMarkdown,
				SelectedOption: selectedID,
			})

			crabReq := CrabDecompositionRequest{
				PlanID:                  selectedItem.ID,
				Project:                 req.Project,
				WorkDir:                 req.WorkDir,
				PlanMarkdown:            planMarkdown,
				Tier:                    req.Tier,
				RequireHumanReview:      false,
				DisableTurtleEscalation: false, // Allow crab failures to rebound into planning.
			}
			childOpts := workflow.ChildWorkflowOptions{
				WorkflowID: fmt.Sprintf("crab-from-planning-%s-%d", taskReq.TaskID, workflow.Now(ctx).Unix()),
				TaskQueue:  DefaultTaskQueue,
			}
			childCtx := workflow.WithChildOptions(ctx, childOpts)
			var crabResult CrabDecompositionResult
			if err := workflow.ExecuteChildWorkflow(childCtx, CrabDecompositionWorkflow, crabReq).Get(ctx, &crabResult); err != nil {
				return taskReq, fmt.Errorf("planned and greenlit but crab decomposition failed: %w", err)
			}
			recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
				TaskID:         selectedItem.ID,
				Stage:          "greenlight",
				NodeID:         graph.nextNode("handoff"),
				ParentNodeID:   graph.current,
				BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
				OptionID:       selectedItem.ID,
				EventType:      "crab_handoff_completed",
				SummaryText:    fmt.Sprintf("crab status=%s", crabResult.Status),
				FullText:       fmt.Sprintf("whales=%d morsels=%d", len(crabResult.WhalesEmitted), len(crabResult.MorselsEmitted)),
				SelectedOption: selectedID,
			})
			switch strings.ToLower(strings.TrimSpace(crabResult.Status)) {
			case "completed":
				// Continue below to close the original seed task.
			case "escalated":
				// Crab already bounced back into a fresh planning ceremony. Treat this as
				// a valid loop transition so traces can be analyzed without hard-failing.
				recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
					TaskID:         selectedItem.ID,
					Stage:          "greenlight",
					NodeID:         graph.nextNode("handoff"),
					ParentNodeID:   graph.current,
					BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
					OptionID:       selectedItem.ID,
					EventType:      "crab_rebound_to_planning",
					SummaryText:    "crab decomposition rebounded to planning ceremony",
					FullText:       fmt.Sprintf("status=%s", crabResult.Status),
					SelectedOption: selectedID,
				})
				return taskReq, nil
			default:
				return taskReq, fmt.Errorf("planned and greenlit but crab returned status %q", crabResult.Status)
			}

			// The original selected task has been replanned/decomposed; close it so
			// dispatcher routing follows emitted morsels instead of re-running this seed.
			if err := workflow.ExecuteActivity(ctx, a.CloseTaskActivity, selectedItem.ID, "closed").Get(ctx, nil); err != nil {
				logger.Warn("planning close seed task failed (best-effort)", "task_id", selectedItem.ID, "error", err)
			}
			return taskReq, nil
		}

		logger.Info("Planning: realigning", "Cycle", req.TraceCycle, "Remaining", maxPlanningCycles-cycle-1)
		recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
			TaskID:         selectedItem.ID,
			Stage:          "greenlight",
			NodeID:         graph.nextNode("realign"),
			ParentNodeID:   graph.current,
			BranchID:       fmt.Sprintf("%s-option-%s", branchID, selectedItem.ID),
			OptionID:       selectedItem.ID,
			EventType:      "plan_realign",
			SummaryText:    "plan not agreed; retrying planning cycle",
			FullText:       fmt.Sprintf("decision=%s", decision),
			Reward:         0,
			SelectedOption: selectedID,
		})
		reviewAndProposeAlternatives(ctx, req, graph, branchID, "greenlight_not_go", selectedItem.ID, rankedCandidates, candidateScoreAdjustments)
	}

	reviewPlanningCeremonyOutcome(ctx, req, graph, lastBranchID, "planning_exhausted", lastSelectedID, lastRankedCandidates)
	recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
		Stage:        "planning",
		NodeID:       graph.nextNode("done"),
		ParentNodeID: graph.current,
		EventType:    "planning_exhausted",
		SummaryText:  "planning exhausted without agreement",
		FullText:     fmt.Sprintf("max_cycles=%d", maxPlanningCycles),
		Reward:       0,
	})
	return nil, fmt.Errorf("planning exhausted %d cycles without greenlight", maxPlanningCycles)
}

func seedPlanningBacklogFromRequest(req PlanningRequest) (BacklogPresentation, bool) {
	seedID := strings.TrimSpace(req.SeedTaskID)
	if seedID == "" {
		return BacklogPresentation{}, false
	}
	seedTitle := strings.TrimSpace(req.SeedTaskTitle)
	if seedTitle == "" {
		seedTitle = seedID
	}
	seedPrompt := strings.TrimSpace(req.SeedTaskPrompt)
	if seedPrompt == "" {
		seedPrompt = "Dispatcher-seeded task entering planning ceremony."
	}
	item := BacklogItem{
		ID:          seedID,
		Title:       seedTitle,
		Impact:      truncate(seedPrompt, 240),
		Effort:      "unknown",
		Recommended: true,
		Rationale:   "Seeded by routing policy for planning-first decomposition.",
	}
	return BacklogPresentation{
		Items:     []BacklogItem{item},
		Rationale: "Seeded planning ceremony for dispatcher/escalation handoff.",
	}, true
}

func chooseAutoPlanningSelection(req PlanningRequest, ranked []planningCandidate) string {
	seedID := strings.TrimSpace(req.SeedTaskID)
	for i := range ranked {
		if strings.TrimSpace(ranked[i].Item.ID) == seedID {
			return seedID
		}
	}
	if len(ranked) > 0 {
		return strings.TrimSpace(ranked[0].Item.ID)
	}
	return seedID
}

func autoPlanningAnswer(q PlanningQuestion) string {
	rec := strings.TrimSpace(q.Recommendation)
	if rec != "" {
		return rec
	}
	if len(q.Options) > 0 {
		return strings.TrimSpace(q.Options[0])
	}
	return "No answer provided."
}

func buildPlanningArtifactMarkdown(req PlanningRequest, item BacklogItem, summary PlanSummary, answers map[string]string) string {
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = strings.TrimSpace(item.ID)
	}
	if title == "" {
		title = "Planning Artifact"
	}

	var context strings.Builder
	context.WriteString(fmt.Sprintf("Selected item `%s` in project `%s`.\n", strings.TrimSpace(item.ID), strings.TrimSpace(req.Project)))
	if why := strings.TrimSpace(summary.Why); why != "" {
		context.WriteString("Why now: ")
		context.WriteString(why)
		context.WriteString("\n")
	}
	if effort := strings.TrimSpace(summary.Effort); effort != "" {
		context.WriteString("Estimated effort: ")
		context.WriteString(effort)
		context.WriteString("\n")
	}
	if seedPrompt := strings.TrimSpace(req.SeedTaskPrompt); seedPrompt != "" {
		context.WriteString("\nSeed context:\n")
		context.WriteString(truncate(seedPrompt, 1200))
		context.WriteString("\n")
	}
	if len(answers) > 0 {
		keys := make([]string, 0, len(answers))
		for k := range answers {
			keys = append(keys, strings.TrimSpace(k))
		}
		sort.Strings(keys)
		context.WriteString("\nPlanning answers:\n")
		for _, k := range keys {
			if k == "" {
				continue
			}
			v := strings.TrimSpace(answers[k])
			if v == "" {
				continue
			}
			context.WriteString("- Q")
			context.WriteString(k)
			context.WriteString(": ")
			context.WriteString(v)
			context.WriteString("\n")
		}
	}

	scopePrimary := strings.TrimSpace(summary.What)
	if scopePrimary == "" {
		scopePrimary = fmt.Sprintf("Implement the selected slice: %s", title)
	}

	scopeSecondary := strings.TrimSpace(item.Rationale)
	if scopeSecondary == "" {
		scopeSecondary = "Define dependencies and sequence for safe incremental delivery."
	}

	acceptance := make([]string, 0, len(summary.DoDChecks)+1)
	for _, check := range summary.DoDChecks {
		check = strings.TrimSpace(check)
		if check == "" {
			continue
		}
		acceptance = append(acceptance, check)
	}
	if len(acceptance) == 0 {
		acceptance = append(acceptance, "At least one deterministic verification check is defined.")
	}

	var b strings.Builder
	b.WriteString("# Plan: ")
	b.WriteString(title)
	b.WriteString("\n## Context\n")
	b.WriteString(strings.TrimSpace(context.String()))
	b.WriteString("\n\n## Scope\n")
	b.WriteString("- [ ] ")
	b.WriteString(scopePrimary)
	b.WriteString("\n- [ ] ")
	b.WriteString(scopeSecondary)
	b.WriteString("\n\n## Acceptance Criteria\n")
	for _, ac := range acceptance {
		b.WriteString("- ")
		b.WriteString(ac)
		b.WriteString("\n")
	}
	b.WriteString("\n## Out of Scope\n")
	b.WriteString("- Direct implementation in planning ceremony.\n")
	b.WriteString("- Unbounded changes outside the selected slice.\n")
	return b.String()
}

func validateBacklogGate(backlog BacklogPresentation) planningGateResult {
	if len(backlog.Items) == 0 {
		return planningGateResult{Code: "empty_backlog", Message: "backlog contains no items"}
	}
	if len(backlog.Items) > 20 {
		return planningGateResult{Code: "oversized_backlog", Message: "backlog exceeds max 20 items"}
	}
	seen := make(map[string]struct{}, len(backlog.Items))
	for _, item := range backlog.Items {
		id := strings.TrimSpace(item.ID)
		title := strings.TrimSpace(item.Title)
		if id == "" || title == "" {
			return planningGateResult{Code: "invalid_backlog_item", Message: "backlog item missing id or title"}
		}
		if _, ok := seen[id]; ok {
			return planningGateResult{Code: "duplicate_backlog_item", Message: "backlog has duplicate item ids"}
		}
		seen[id] = struct{}{}
	}
	return planningGateResult{Passed: true, Code: "ok", Message: "backlog gate passed"}
}

func validateSelectionGate(selectedID string, found bool) planningGateResult {
	if strings.TrimSpace(selectedID) == "" {
		return planningGateResult{Code: "selection_empty", Message: "selected item id is empty"}
	}
	if !found {
		return planningGateResult{Code: "selection_not_in_backlog", Message: "selected item id not in backlog"}
	}
	return planningGateResult{Passed: true, Code: "ok", Message: "selection gate passed"}
}

func validateQuestionsGate(questions []PlanningQuestion) planningGateResult {
	if len(questions) == 0 {
		return planningGateResult{Code: "questions_empty", Message: "no clarifying questions generated"}
	}
	if len(questions) > 5 {
		return planningGateResult{Code: "questions_oversized", Message: "too many questions generated"}
	}
	for _, q := range questions {
		if strings.TrimSpace(q.Question) == "" {
			return planningGateResult{Code: "question_empty", Message: "question text is empty"}
		}
		if len(q.Options) < 2 {
			return planningGateResult{Code: "question_options_insufficient", Message: "question has fewer than 2 options"}
		}
		if strings.TrimSpace(q.Recommendation) == "" {
			return planningGateResult{Code: "question_missing_recommendation", Message: "question missing recommendation"}
		}
	}
	return planningGateResult{Passed: true, Code: "ok", Message: "questions gate passed"}
}

func validateSummaryGate(summary PlanSummary) planningGateResult {
	if strings.TrimSpace(summary.What) == "" {
		return planningGateResult{Code: "summary_missing_what", Message: "summary.what is required"}
	}
	if strings.TrimSpace(summary.Why) == "" {
		return planningGateResult{Code: "summary_missing_why", Message: "summary.why is required"}
	}
	if strings.TrimSpace(summary.Effort) == "" {
		return planningGateResult{Code: "summary_missing_effort", Message: "summary.effort is required"}
	}
	if len(summary.DoDChecks) == 0 {
		return planningGateResult{Code: "summary_missing_dod_checks", Message: "summary must include at least one DoD check"}
	}
	return planningGateResult{Passed: true, Code: "ok", Message: "summary gate passed"}
}

func findBacklogItem(backlog BacklogPresentation, selectedID string) (*BacklogItem, bool) {
	for i := range backlog.Items {
		if backlog.Items[i].ID == selectedID {
			item := backlog.Items[i]
			return &item, true
		}
	}
	return nil, false
}

func handlePlanningGateFailure(
	ctx workflow.Context,
	req PlanningRequest,
	graph *planningGraphTracker,
	branchID string,
	stage string,
	gate planningGateResult,
	taskID string,
	stateHash string,
	actionHash string,
) {
	recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
		TaskID:         taskID,
		Stage:          stage,
		NodeID:         graph.nextNode("gate"),
		ParentNodeID:   graph.current,
		BranchID:       branchID,
		EventType:      "gate_fail",
		SummaryText:    gate.Code,
		FullText:       gate.Message,
		Reward:         0,
		MetadataJSON:   fmt.Sprintf(`{"code":"%s","message":"%s"}`, gate.Code, gate.Message),
		SelectedOption: "",
	})
	rollbackPlanningState(ctx, req, graph, branchID, gate.Code)

	// stateHash/actionHash are optional depending on failure site.
	if strings.TrimSpace(stateHash) != "" || strings.TrimSpace(actionHash) != "" {
		recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
			TaskID:       taskID,
			Stage:        stage,
			NodeID:       graph.nextNode("gate"),
			ParentNodeID: graph.current,
			BranchID:     branchID,
			EventType:    "gate_failure_signature",
			SummaryText:  "state-action failure signature recorded",
			FullText:     fmt.Sprintf("state_hash=%s action_hash=%s", stateHash, actionHash),
			Reward:       0,
		})
	}
}

func rollbackPlanningState(ctx workflow.Context, req PlanningRequest, graph *planningGraphTracker, branchID, reason string) {
	latest := getLatestStablePlanningSnapshot(ctx, req)
	if latest == nil {
		recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
			Stage:        "rollback",
			NodeID:       graph.nextNode("rollback"),
			ParentNodeID: graph.current,
			BranchID:     branchID,
			EventType:    "rollback_skipped",
			SummaryText:  "No stable snapshot found for rollback",
			FullText:     fmt.Sprintf("reason=%s", reason),
		})
		return
	}

	recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
		TaskID:         latest.TaskID,
		Stage:          "rollback",
		NodeID:         graph.nextNode("rollback"),
		ParentNodeID:   graph.current,
		BranchID:       branchID,
		EventType:      "rollback_applied",
		SummaryText:    "Rolled back to latest stable planning snapshot",
		FullText:       fmt.Sprintf("reason=%s restore_stage=%s state_hash=%s", reason, latest.Stage, latest.StateHash),
		SelectedOption: "",
	})
}

func encodePlanningState(state PlanningSnapshotState) (string, string) {
	normalized := PlanningSnapshotState{
		Cycle:          state.Cycle,
		Stage:          strings.TrimSpace(state.Stage),
		SelectedID:     strings.TrimSpace(state.SelectedID),
		SelectedTitle:  strings.TrimSpace(state.SelectedTitle),
		SelectedOption: strings.TrimSpace(state.SelectedOption),
		SummaryWhat:    strings.TrimSpace(state.SummaryWhat),
		SummaryWhy:     strings.TrimSpace(state.SummaryWhy),
		SummaryEffort:  strings.TrimSpace(state.SummaryEffort),
	}
	if len(state.Answers) > 0 {
		normalized.Answers = make(map[string]string, len(state.Answers))
		// Ensure deterministic trimming; encoding/json handles key ordering.
		for k, v := range state.Answers {
			normalized.Answers[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}

	data, err := json.Marshal(normalized)
	if err != nil {
		return "", ""
	}
	sum := sha256.Sum256(data)
	return string(data), hex.EncodeToString(sum[:])
}

func hashPlanningAction(action string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(action)))
	return hex.EncodeToString(sum[:])
}

func interpretPlanningGoal(backlog BacklogPresentation) (string, string) {
	rationale := strings.TrimSpace(backlog.Rationale)
	if rationale == "" {
		rationale = "Planning goal inferred from ranked backlog candidates."
	}
	top := make([]string, 0, 3)
	for i := range backlog.Items {
		if len(top) == 3 {
			break
		}
		item := backlog.Items[i]
		top = append(top, fmt.Sprintf("%s:%s", strings.TrimSpace(item.ID), strings.TrimSpace(item.Title)))
	}
	return rationale, fmt.Sprintf("top_candidates=%s", strings.Join(top, " | "))
}

func normalizePlanningCandidateTopK(topK int) int {
	if topK <= 0 {
		return defaultPlanningCandidateTopK
	}
	if topK > maxPlanningCandidateTopK {
		return maxPlanningCandidateTopK
	}
	return topK
}

func normalizePlanningSignalTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultPlanningSignalTimeout
	}
	return timeout
}

func normalizePlanningSessionTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultPlanningSessionTimeout
	}
	return timeout
}

func hasPlanningSessionTimedOut(req PlanningRequest, startedAt, now time.Time) bool {
	return now.Sub(startedAt) >= req.SessionTimeout
}

func planningWaitTimeout(req PlanningRequest, startedAt, now time.Time) time.Duration {
	remaining := req.SessionTimeout - now.Sub(startedAt)
	if remaining <= 0 {
		return 0
	}
	if req.SignalTimeout > 0 && req.SignalTimeout < remaining {
		return req.SignalTimeout
	}
	return remaining
}

func waitForPlanningSignal(ctx workflow.Context, signalName string, timeout time.Duration) (string, bool) {
	if timeout <= 0 {
		return "", false
	}
	signalChan := workflow.GetSignalChannel(ctx, signalName)

	var value string
	received := false
	timerCtx, cancelTimer := workflow.WithCancel(ctx)
	defer cancelTimer()

	timer := workflow.NewTimer(timerCtx, timeout)
	selector := workflow.NewSelector(ctx)
	selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, &value)
		received = true
		cancelTimer()
	})
	selector.AddFuture(timer, func(workflow.Future) {})
	selector.Select(ctx)

	if !received {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func failPlanningSignalTimeout(
	ctx workflow.Context,
	req PlanningRequest,
	graph *planningGraphTracker,
	branchID string,
	stage string,
	signalName string,
	taskID string,
	selectedOption string,
	rankedCandidates []planningCandidate,
	reason string,
) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = fmt.Sprintf("timed out waiting for %s", signalName)
	}
	timeoutNode := graph.nextNode("timeout")
	recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
		TaskID:         strings.TrimSpace(taskID),
		Stage:          strings.TrimSpace(stage),
		NodeID:         timeoutNode,
		ParentNodeID:   graph.current,
		BranchID:       branchID,
		OptionID:       strings.TrimSpace(selectedOption),
		EventType:      "planning_signal_timeout",
		SummaryText:    fmt.Sprintf("timed out waiting for %s", signalName),
		FullText:       fmt.Sprintf("stage=%s signal=%s reason=%s", stage, signalName, reason),
		SelectedOption: strings.TrimSpace(selectedOption),
		Reward:         0,
	})
	if stateJSON, stateHash := encodePlanningState(PlanningSnapshotState{
		Cycle:          req.TraceCycle,
		Stage:          strings.TrimSpace(stage),
		SelectedID:     strings.TrimSpace(taskID),
		SelectedOption: strings.TrimSpace(selectedOption),
	}); stateHash != "" {
		recordPlanningSnapshot(ctx, req, PlanningSnapshotRecord{
			Project:   req.Project,
			TaskID:    strings.TrimSpace(taskID),
			Cycle:     req.TraceCycle,
			Stage:     strings.TrimSpace(stage),
			StateHash: stateHash,
			StateJSON: stateJSON,
			Stable:    false,
			Reason:    fmt.Sprintf("signal_timeout_%s", signalName),
		})
	}
	reviewPlanningCeremonyOutcome(ctx, req, graph, branchID, fmt.Sprintf("timeout_%s_%s", stage, signalName), selectedOption, rankedCandidates)
	return fmt.Errorf("planning signal timeout at stage %q waiting for %q: %s", stage, signalName, reason)
}

type planningBehaviorContract struct {
	OptimalSlice string            `json:"optimal_slice"`
	LooksLike    string            `json:"looks_like"`
	Does         string            `json:"does"`
	WhyNow       string            `json:"why_now"`
	AnswerHints  map[string]string `json:"answer_hints,omitempty"`
}

func buildPlanningBehaviorContract(item BacklogItem, summary PlanSummary, answers map[string]string) planningBehaviorContract {
	optimalSlice := strings.TrimSpace(item.Title)
	if rationale := strings.TrimSpace(item.Rationale); rationale != "" {
		optimalSlice = fmt.Sprintf("%s (%s)", strings.TrimSpace(item.Title), rationale)
	}

	looksLike := strings.TrimSpace(summary.What)
	if looksLike == "" {
		looksLike = fmt.Sprintf("Implement %s with clear acceptance checks.", strings.TrimSpace(item.Title))
	}

	does := strings.Join(summary.DoDChecks, " ; ")
	if strings.TrimSpace(does) == "" {
		does = "No explicit DoD checks provided yet."
	}

	hints := make(map[string]string, 0)
	for k, v := range answers {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		hints[k] = v
	}
	if len(hints) == 0 {
		hints = nil
	}

	return planningBehaviorContract{
		OptimalSlice: optimalSlice,
		LooksLike:    looksLike,
		Does:         does,
		WhyNow:       strings.TrimSpace(summary.Why),
		AnswerHints:  hints,
	}
}

func behaviorContractToText(contract planningBehaviorContract) string {
	lines := []string{
		fmt.Sprintf("optimal_slice=%s", contract.OptimalSlice),
		fmt.Sprintf("looks_like=%s", contract.LooksLike),
		fmt.Sprintf("does=%s", contract.Does),
		fmt.Sprintf("why_now=%s", contract.WhyNow),
	}
	if len(contract.AnswerHints) > 0 {
		hints := make([]string, 0, len(contract.AnswerHints))
		for k, v := range contract.AnswerHints {
			hints = append(hints, fmt.Sprintf("%s:%s", k, v))
		}
		sort.Strings(hints)
		lines = append(lines, fmt.Sprintf("answer_hints=%s", strings.Join(hints, " | ")))
	}
	return strings.Join(lines, "\n")
}

func shouldIteratePlanningLoop(cycle int, selectedItem BacklogItem, summary PlanSummary) (bool, string) {
	if cycle >= maxPlanningCycles {
		return false, "max_cycles_reached"
	}
	haystack := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		selectedItem.Title,
		selectedItem.Impact,
		selectedItem.Effort,
		selectedItem.Rationale,
		summary.What,
		summary.Why,
		summary.Effort,
	}, " ")))
	switch {
	case strings.Contains(haystack, "mega-epic"),
		strings.Contains(haystack, "mega epic"),
		strings.Contains(haystack, "megaepic"):
		return true, "scope_is_mega_epic"
	case strings.Contains(haystack, " epic "),
		strings.HasPrefix(haystack, "epic "),
		strings.HasSuffix(haystack, " epic"),
		strings.Contains(haystack, "epic:"),
		strings.Contains(haystack, "epic-"):
		return true, "scope_is_epic"
	case strings.Contains(haystack, "xl"),
		strings.Contains(haystack, "large"),
		strings.Contains(haystack, "weeks"),
		strings.Contains(haystack, "month"),
		strings.Contains(haystack, "multi-sprint"),
		strings.Contains(haystack, "multi sprint"):
		return true, "effort_is_large"
	default:
		return false, "scope_is_sliceable"
	}
}

type planningAlternativeCandidate struct {
	Candidate  planningCandidate
	ScoreDelta float64
	Reason     string
}

type planningNovelPathway struct {
	OptionID     string
	Title        string
	Strategy     string
	Reason       string
	SuggestedKey string
	NoveltyScore float64
}

func reviewAndProposeAlternatives(
	ctx workflow.Context,
	req PlanningRequest,
	graph *planningGraphTracker,
	branchID string,
	failureReason string,
	selectedID string,
	candidates []planningCandidate,
	adjustments map[string]float64,
) {
	diagnosis := diagnosePlanningFailure(failureReason)
	alternatives := proposeAlternativeCandidates(selectedID, candidates, adjustments)

	if strings.TrimSpace(selectedID) != "" {
		persistPlanningCandidateScoreAdjustment(ctx, req, PlanningCandidateScoreDelta{
			OptionID: strings.TrimSpace(selectedID),
			Delta:    planningSelectedPenaltyDelta,
			Outcome:  "failure",
			Reason:   failureReason,
		})
	}

	alternativeIDs := make([]string, 0, len(alternatives))
	for i := range alternatives {
		alternativeIDs = append(alternativeIDs, alternatives[i].Candidate.Item.ID)
	}
	recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
		TaskID:         selectedID,
		Stage:          "review",
		NodeID:         graph.nextNode("review"),
		ParentNodeID:   graph.current,
		BranchID:       branchID,
		EventType:      "trace_review",
		SummaryText:    fmt.Sprintf("reviewed poor outcome; %d alternatives proposed", len(alternativeIDs)),
		FullText:       fmt.Sprintf("reason=%s\ndiagnosis=%s\nselected=%s\nalternatives=%s", failureReason, diagnosis, selectedID, strings.Join(alternativeIDs, ",")),
		MetadataJSON:   mustJSON(map[string]any{"reason": failureReason, "diagnosis": diagnosis, "selected": selectedID, "alternatives": alternativeIDs}),
		SelectedOption: selectedID,
	})

	for i := range alternatives {
		alt := alternatives[i]
		updated := 0.0
		if adjustments != nil {
			updated = adjustments[alt.Candidate.Item.ID]
		}
		persistPlanningCandidateScoreAdjustment(ctx, req, PlanningCandidateScoreDelta{
			OptionID: alt.Candidate.Item.ID,
			Delta:    alt.ScoreDelta,
			Outcome:  "alternative",
			Reason:   alt.Reason + "; " + failureReason,
		})
		recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
			TaskID:         alt.Candidate.Item.ID,
			Stage:          "review",
			NodeID:         graph.nextNode("review"),
			ParentNodeID:   graph.current,
			BranchID:       fmt.Sprintf("%s-option-%s", branchID, alt.Candidate.Item.ID),
			OptionID:       alt.Candidate.Item.ID,
			EventType:      "alternative_trace_candidate",
			SummaryText:    fmt.Sprintf("rank=%d delta=%.1f", alt.Candidate.Rank, alt.ScoreDelta),
			FullText:       fmt.Sprintf("title=%s\nreason=%s\nscore_delta=%.1f\nupdated_adjustment=%.1f", alt.Candidate.Item.Title, alt.Reason, alt.ScoreDelta, updated),
			MetadataJSON:   mustJSON(map[string]any{"reason": alt.Reason, "score_delta": alt.ScoreDelta, "updated_adjustment": updated, "rank": alt.Candidate.Rank, "diagnosis": diagnosis}),
			SelectedOption: selectedID,
		})
	}
}

func diagnosePlanningFailure(reason string) string {
	r := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(r, "selection_blacklisted"):
		return "Selection repeated a previously blocked state-action path."
	case strings.Contains(r, "selection_"):
		return "Selected backlog item was invalid for current cycle context."
	case strings.Contains(r, "questions_blacklisted"), strings.Contains(r, "questions_"):
		return "Question-generation path lacked actionable clarifications."
	case strings.Contains(r, "summary_"):
		return "Plan summary was incomplete or unverifiable for execution."
	case strings.Contains(r, "greenlight_not_go"):
		return "Human feedback did not agree with proposed behavior contract."
	case strings.Contains(r, "loop_iterate_"):
		return "Scope appears too large for a single slice; requires iterative decomposition."
	default:
		return "Planning path underperformed and requires alternative branch exploration."
	}
}

func reviewPlanningCeremonyOutcome(
	ctx workflow.Context,
	req PlanningRequest,
	graph *planningGraphTracker,
	branchID string,
	outcome string,
	selectedID string,
	candidates []planningCandidate,
) {
	outcome = strings.TrimSpace(outcome)
	if outcome == "" {
		outcome = "unknown"
	}
	selectedID = strings.TrimSpace(selectedID)
	branchID = strings.TrimSpace(branchID)
	if branchID == "" {
		branchID = fmt.Sprintf("cycle-%d", req.TraceCycle)
	}

	consideredIDs := make([]string, 0, len(candidates))
	shortlistedIDs := make([]string, 0, len(candidates))
	for i := range candidates {
		id := strings.TrimSpace(candidates[i].Item.ID)
		if id == "" {
			continue
		}
		consideredIDs = append(consideredIDs, id)
		if candidates[i].Shortlisted {
			shortlistedIDs = append(shortlistedIDs, id)
		}
	}

	proposals := proposeNovelPathways(selectedID, candidates, outcome)
	recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
		TaskID:         selectedID,
		Stage:          "review",
		NodeID:         graph.nextNode("review"),
		ParentNodeID:   graph.current,
		BranchID:       branchID,
		EventType:      "ceremony_review",
		SummaryText:    fmt.Sprintf("post-ceremony review outcome=%s proposals=%d", outcome, len(proposals)),
		FullText:       fmt.Sprintf("outcome=%s\nselected=%s\nconsidered=%s\nshortlisted=%s", outcome, selectedID, strings.Join(consideredIDs, ","), strings.Join(shortlistedIDs, ",")),
		MetadataJSON:   mustJSON(map[string]any{"outcome": outcome, "selected": selectedID, "considered": consideredIDs, "shortlisted": shortlistedIDs, "proposal_count": len(proposals)}),
		SelectedOption: selectedID,
	})

	for i := range proposals {
		proposal := proposals[i]
		proposalBranchID := branchID
		if strings.TrimSpace(proposal.OptionID) != "" {
			proposalBranchID = fmt.Sprintf("%s-option-%s", branchID, strings.TrimSpace(proposal.OptionID))
		}
		recordPlanningWorkflowTrace(ctx, req, PlanningTraceRecord{
			TaskID:         proposal.OptionID,
			Stage:          "review",
			NodeID:         graph.nextNode("novel"),
			ParentNodeID:   graph.current,
			BranchID:       proposalBranchID,
			OptionID:       proposal.OptionID,
			EventType:      "novel_pathway_candidate",
			SummaryText:    proposal.Title,
			FullText:       fmt.Sprintf("strategy=%s\nreason=%s\nsuggested_key=%s\nnovelty_score=%.2f", proposal.Strategy, proposal.Reason, proposal.SuggestedKey, proposal.NoveltyScore),
			MetadataJSON:   mustJSON(map[string]any{"strategy": proposal.Strategy, "reason": proposal.Reason, "suggested_key": proposal.SuggestedKey, "novelty_score": proposal.NoveltyScore, "outcome": outcome}),
			SelectedOption: selectedID,
		})
	}
}

func proposeNovelPathways(selectedID string, candidates []planningCandidate, outcome string) []planningNovelPathway {
	selectedID = strings.TrimSpace(selectedID)
	outcome = strings.ToLower(strings.TrimSpace(outcome))

	proposals := make([]planningNovelPathway, 0, maxPlanningNovelPathways)
	seenOptionIDs := make(map[string]struct{}, maxPlanningNovelPathways)
	appendProposal := func(p planningNovelPathway) {
		if len(proposals) >= maxPlanningNovelPathways {
			return
		}
		p.OptionID = strings.TrimSpace(p.OptionID)
		if p.OptionID != "" {
			if _, exists := seenOptionIDs[p.OptionID]; exists {
				return
			}
			seenOptionIDs[p.OptionID] = struct{}{}
		}
		proposals = append(proposals, p)
	}

	outcomeHint := "parallel experiment to compare assumptions"
	if strings.Contains(outcome, "error") || strings.Contains(outcome, "exhausted") {
		outcomeHint = "recovery branch to escape repeated failure mode"
	}
	if strings.Contains(outcome, "agreed") {
		outcomeHint = "post-agreement hedge to protect delivery quality"
	}

	for i := range candidates {
		candidate := candidates[i]
		if len(proposals) >= maxPlanningNovelPathways {
			break
		}
		id := strings.TrimSpace(candidate.Item.ID)
		if id == "" || id == selectedID || !candidate.Shortlisted {
			continue
		}
		appendProposal(planningNovelPathway{
			OptionID:     id,
			Title:        fmt.Sprintf("Alternative slice: %s", candidate.Item.Title),
			Strategy:     "alternate_shortlisted_branch",
			Reason:       fmt.Sprintf("High-ranked but unselected option retained as %s.", outcomeHint),
			SuggestedKey: fmt.Sprintf("novel/%s/alternate-shortlisted", id),
			NoveltyScore: 0.62,
		})
	}

	for i := range candidates {
		candidate := candidates[i]
		if len(proposals) >= maxPlanningNovelPathways {
			break
		}
		id := strings.TrimSpace(candidate.Item.ID)
		if id == "" || id == selectedID || candidate.Shortlisted {
			continue
		}
		appendProposal(planningNovelPathway{
			OptionID:     id,
			Title:        fmt.Sprintf("Re-open pruned branch: %s", candidate.Item.Title),
			Strategy:     "reopen_pruned_candidate",
			Reason:       "Candidate was previously pruned; re-open as novelty probe for hidden value.",
			SuggestedKey: fmt.Sprintf("novel/%s/reopen-pruned", id),
			NoveltyScore: 0.88,
		})
	}

	if selectedID != "" {
		appendProposal(planningNovelPathway{
			OptionID:     selectedID,
			Title:        "Stress-test selected behavior contract",
			Strategy:     "selected_path_stress_test",
			Reason:       "Generate adversarial checks against the agreed slice before implementation drifts.",
			SuggestedKey: fmt.Sprintf("novel/%s/stress-test-contract", selectedID),
			NoveltyScore: 0.71,
		})
	}

	if len(proposals) == 0 {
		appendProposal(planningNovelPathway{
			OptionID:     "",
			Title:        "Assumption-audit branch",
			Strategy:     "meta_assumption_audit",
			Reason:       "No viable alternatives detected; create a meta-branch that challenges planning assumptions.",
			SuggestedKey: "novel/meta/assumption-audit",
			NoveltyScore: 0.93,
		})
	}

	return proposals
}

func proposeAlternativeCandidates(
	selectedID string,
	candidates []planningCandidate,
	adjustments map[string]float64,
) []planningAlternativeCandidate {
	selectedID = strings.TrimSpace(selectedID)
	if selectedID != "" {
		applyPlanningScoreAdjustment(adjustments, selectedID, planningSelectedPenaltyDelta)
	}
	if len(candidates) == 0 {
		return nil
	}

	deltas := []float64{12, 8, 5}
	proposals := make([]planningAlternativeCandidate, 0, len(deltas))
	for i := range candidates {
		candidate := candidates[i]
		if candidate.Item.ID == selectedID || !candidate.Shortlisted {
			continue
		}
		if len(proposals) == len(deltas) {
			break
		}
		delta := deltas[len(proposals)]
		applyPlanningScoreAdjustment(adjustments, candidate.Item.ID, delta)
		proposals = append(proposals, planningAlternativeCandidate{
			Candidate:  candidate,
			ScoreDelta: delta,
			Reason:     "shortlisted alternative promoted after poor outcome",
		})
	}

	if len(proposals) > 0 {
		return proposals
	}

	for i := range candidates {
		candidate := candidates[i]
		if candidate.Item.ID == selectedID {
			continue
		}
		delta := 10.0
		applyPlanningScoreAdjustment(adjustments, candidate.Item.ID, delta)
		return []planningAlternativeCandidate{
			{
				Candidate:  candidate,
				ScoreDelta: delta,
				Reason:     "fallback alternative promoted because no shortlisted alternatives were available",
			},
		}
	}

	return nil
}

func hydratePlanningCandidateScoreAdjustments(
	ctx workflow.Context,
	req PlanningRequest,
	items []BacklogItem,
	adjustments map[string]float64,
) {
	logger := workflow.GetLogger(ctx)
	if adjustments == nil || len(items) == 0 {
		return
	}

	optionIDs := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for i := range items {
		id := strings.TrimSpace(items[i].ID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		optionIDs = append(optionIDs, id)
	}
	if len(optionIDs) == 0 {
		return
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	actCtx := workflow.WithActivityOptions(ctx, ao)

	var a *Activities
	var persisted []PlanningCandidateScoreRecord
	if err := workflow.ExecuteActivity(actCtx, a.LoadPlanningCandidateScoresActivity, PlanningCandidateScoreQuery{
		Project:   req.Project,
		OptionIDs: optionIDs,
	}).Get(ctx, &persisted); err != nil {
		logger.Debug("Planning candidate score hydration skipped (non-fatal)", "error", err)
		return
	}
	for i := range persisted {
		rec := persisted[i]
		id := strings.TrimSpace(rec.OptionID)
		if id == "" {
			continue
		}
		adjustments[id] = rec.ScoreAdjustment
	}
}

func persistPlanningCandidateScoreAdjustment(ctx workflow.Context, req PlanningRequest, delta PlanningCandidateScoreDelta) {
	logger := workflow.GetLogger(ctx)
	delta.OptionID = strings.TrimSpace(delta.OptionID)
	if delta.OptionID == "" {
		return
	}
	if strings.TrimSpace(delta.Project) == "" {
		delta.Project = req.Project
	}
	if strings.TrimSpace(delta.Project) == "" {
		return
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	actCtx := workflow.WithActivityOptions(ctx, ao)
	var a *Activities
	if err := workflow.ExecuteActivity(actCtx, a.AdjustPlanningCandidateScoreActivity, delta).Get(ctx, nil); err != nil {
		logger.Debug("Planning candidate score persist skipped (non-fatal)", "error", err)
	}
}

func applyPlanningScoreAdjustment(adjustments map[string]float64, candidateID string, delta float64) float64 {
	if adjustments == nil {
		return 0
	}
	candidateID = strings.TrimSpace(candidateID)
	if candidateID == "" {
		return 0
	}

	current := adjustments[candidateID] + delta
	switch {
	case current > 50:
		current = 50
	case current < -50:
		current = -50
	}
	adjustments[candidateID] = current
	return current
}

type planningCandidate struct {
	Item        BacklogItem
	Score       float64
	Rank        int
	Shortlisted bool
}

func rankPlanningCandidates(items []BacklogItem, topK int, adjustments ...map[string]float64) []planningCandidate {
	if len(items) == 0 {
		return nil
	}

	var scoreAdjustments map[string]float64
	if len(adjustments) > 0 {
		scoreAdjustments = adjustments[0]
	}

	candidates := make([]planningCandidate, 0, len(items))
	for i := range items {
		item := items[i]
		score := planningCandidateScore(item)
		if scoreAdjustments != nil {
			score += scoreAdjustments[strings.TrimSpace(item.ID)]
		}
		candidates = append(candidates, planningCandidate{
			Item:  item,
			Score: score,
		})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if candidates[i].Item.Recommended != candidates[j].Item.Recommended {
			return candidates[i].Item.Recommended
		}
		if candidates[i].Item.ID != candidates[j].Item.ID {
			return candidates[i].Item.ID < candidates[j].Item.ID
		}
		return candidates[i].Item.Title < candidates[j].Item.Title
	})

	if topK <= 0 || topK > len(candidates) {
		topK = len(candidates)
	}
	for i := range candidates {
		candidates[i].Rank = i + 1
		candidates[i].Shortlisted = i < topK
	}

	return candidates
}

func planningCandidateScore(item BacklogItem) float64 {
	score := 0.0
	if item.Recommended {
		score += 100
	}
	score += planningImpactScore(item.Impact)
	score += planningEffortScore(item.Effort)
	if strings.TrimSpace(item.Rationale) != "" {
		score += 1
	}
	return score
}

func planningImpactScore(raw string) float64 {
	impact := strings.ToLower(strings.TrimSpace(raw))
	switch impact {
	case "critical", "high":
		return 30
	case "medium", "med":
		return 20
	case "low":
		return 10
	}
	switch {
	case strings.Contains(impact, "critical"), strings.Contains(impact, "high"):
		return 30
	case strings.Contains(impact, "medium"), strings.Contains(impact, "med"):
		return 20
	case strings.Contains(impact, "low"):
		return 10
	default:
		return 15
	}
}

func planningEffortScore(raw string) float64 {
	effort := strings.ToLower(strings.TrimSpace(raw))
	switch effort {
	case "xs", "extra-small", "extra small", "tiny":
		return 20
	case "s", "small":
		return 16
	case "m", "medium", "med":
		return 10
	case "l", "large":
		return 5
	case "xl", "extra-large", "extra large":
		return 1
	}
	switch {
	case strings.Contains(effort, "tiny"), strings.Contains(effort, "extra small"), strings.Contains(effort, "extra-small"), strings.Contains(effort, "xs"):
		return 20
	case strings.Contains(effort, "small"):
		return 16
	case strings.Contains(effort, "medium"), strings.Contains(effort, "med"):
		return 10
	case strings.Contains(effort, "large"):
		return 5
	default:
		return 8
	}
}

func candidateMetadataJSON(candidate planningCandidate) string {
	return candidateStatusMetadataJSON(candidate, "ranked")
}

func candidateImplicationsText(candidate planningCandidate) string {
	item := candidate.Item
	implications := []string{
		fmt.Sprintf("impact=%s", strings.TrimSpace(item.Impact)),
		fmt.Sprintf("effort=%s", strings.TrimSpace(item.Effort)),
		fmt.Sprintf("recommended=%t", item.Recommended),
		fmt.Sprintf("rank=%d", candidate.Rank),
		fmt.Sprintf("shortlisted=%t", candidate.Shortlisted),
		fmt.Sprintf("score=%.2f", candidate.Score),
	}
	if strings.TrimSpace(item.Rationale) != "" {
		implications = append(implications, fmt.Sprintf("rationale=%s", strings.TrimSpace(item.Rationale)))
	}

	if candidate.Shortlisted {
		implications = append(implications, "implication=Candidate remains in active set for immediate selection/fallback.")
	} else {
		implications = append(implications, "implication=Candidate is retained for traceability but pruned from immediate shortlist.")
	}

	return strings.Join(implications, "\n")
}

func candidateStatusMetadataJSON(candidate planningCandidate, status string) string {
	return mustJSON(map[string]any{
		"status":      status,
		"rank":        candidate.Rank,
		"score":       candidate.Score,
		"shortlisted": candidate.Shortlisted,
		"recommended": candidate.Item.Recommended,
		"impact":      candidate.Item.Impact,
		"effort":      candidate.Item.Effort,
	})
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func recordPlanningWorkflowTrace(ctx workflow.Context, req PlanningRequest, record PlanningTraceRecord) {
	logger := workflow.GetLogger(ctx)
	if strings.TrimSpace(record.Project) == "" {
		record.Project = req.Project
	}
	if strings.TrimSpace(record.SessionID) == "" {
		record.SessionID = req.TraceSessionID
	}
	if record.Cycle == 0 {
		record.Cycle = req.TraceCycle
	}
	if strings.TrimSpace(record.BranchID) == "" && req.TraceCycle > 0 {
		record.BranchID = fmt.Sprintf("cycle-%d", req.TraceCycle)
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	traceCtx := workflow.WithActivityOptions(ctx, ao)

	var a *Activities
	if err := workflow.ExecuteActivity(traceCtx, a.RecordPlanningTraceActivity, record).Get(ctx, nil); err != nil {
		logger.Debug("Planning trace skipped (non-fatal)", "error", err)
	}
}

func recordPlanningSnapshot(ctx workflow.Context, req PlanningRequest, snapshot PlanningSnapshotRecord) {
	logger := workflow.GetLogger(ctx)
	if strings.TrimSpace(snapshot.SessionID) == "" {
		snapshot.SessionID = req.TraceSessionID
	}
	if snapshot.Cycle == 0 {
		snapshot.Cycle = req.TraceCycle
	}
	if strings.TrimSpace(snapshot.Project) == "" {
		snapshot.Project = req.Project
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	actCtx := workflow.WithActivityOptions(ctx, ao)

	var a *Activities
	if err := workflow.ExecuteActivity(actCtx, a.RecordPlanningSnapshotActivity, snapshot).Get(ctx, nil); err != nil {
		logger.Debug("Planning snapshot skipped (non-fatal)", "error", err)
	}
}

func getLatestStablePlanningSnapshot(ctx workflow.Context, req PlanningRequest) *PlanningSnapshotRecord {
	logger := workflow.GetLogger(ctx)
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	actCtx := workflow.WithActivityOptions(ctx, ao)

	var a *Activities
	var snapshot *PlanningSnapshotRecord
	if err := workflow.ExecuteActivity(actCtx, a.GetLatestStablePlanningSnapshotActivity, req.TraceSessionID).Get(ctx, &snapshot); err != nil {
		logger.Debug("Planning snapshot lookup skipped (non-fatal)", "error", err)
		return nil
	}
	return snapshot
}

func addPlanningBlacklistEntry(ctx workflow.Context, req PlanningRequest, entry PlanningBlacklistEntryRecord) {
	logger := workflow.GetLogger(ctx)
	if strings.TrimSpace(entry.SessionID) == "" {
		entry.SessionID = req.TraceSessionID
	}
	if entry.Cycle == 0 {
		entry.Cycle = req.TraceCycle
	}
	if strings.TrimSpace(entry.Project) == "" {
		entry.Project = req.Project
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	actCtx := workflow.WithActivityOptions(ctx, ao)

	var a *Activities
	if err := workflow.ExecuteActivity(actCtx, a.AddPlanningBlacklistEntryActivity, entry).Get(ctx, nil); err != nil {
		logger.Debug("Planning blacklist entry skipped (non-fatal)", "error", err)
	}
}

func isPlanningActionBlacklisted(ctx workflow.Context, req PlanningRequest, stateHash, actionHash string) bool {
	logger := workflow.GetLogger(ctx)
	if strings.TrimSpace(stateHash) == "" || strings.TrimSpace(actionHash) == "" {
		return false
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	actCtx := workflow.WithActivityOptions(ctx, ao)

	var a *Activities
	var blocked bool
	if err := workflow.ExecuteActivity(actCtx, a.IsPlanningActionBlacklistedActivity, PlanningBlacklistCheck{
		SessionID:  req.TraceSessionID,
		StateHash:  stateHash,
		ActionHash: actionHash,
	}).Get(ctx, &blocked); err != nil {
		logger.Debug("Planning blacklist lookup skipped (non-fatal)", "error", err)
		return false
	}
	return blocked
}
