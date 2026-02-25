package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/antigravity-dev/chum/internal/store"
	"github.com/antigravity-dev/chum/internal/temporal"
)

func TestBuildPlanningPromptResponseSelectingPhase(t *testing.T) {
	events := []store.PlanningTraceEvent{
		{
			SessionID:    "planning-test",
			Cycle:        1,
			Stage:        "groom_backlog",
			EventType:    "candidate_ranked",
			TaskID:       "morsel-a",
			OptionID:     "morsel-a",
			SummaryText:  "#1 Top slice",
			MetadataJSON: `{"rank":1,"shortlisted":true,"recommended":true}`,
		},
		{
			SessionID:    "planning-test",
			Cycle:        1,
			Stage:        "groom_backlog",
			EventType:    "candidate_ranked",
			TaskID:       "morsel-b",
			OptionID:     "morsel-b",
			SummaryText:  "#2 Backup slice",
			MetadataJSON: `{"rank":2,"shortlisted":true,"recommended":false}`,
		},
	}

	resp := buildPlanningPromptResponse("planning-test", "Running", events)
	if resp.Phase != "selecting" {
		t.Fatalf("phase=%q, want selecting", resp.Phase)
	}
	if resp.ExpectedSignal != "item-selected" {
		t.Fatalf("expected_signal=%q, want item-selected", resp.ExpectedSignal)
	}
	if len(resp.Options) != 2 {
		t.Fatalf("len(options)=%d, want 2", len(resp.Options))
	}
	if !strings.Contains(resp.Options[0], "morsel-a") {
		t.Fatalf("option[0]=%q, want contains morsel-a", resp.Options[0])
	}
}

func TestBuildPlanningPromptResponseQuestioningPhase(t *testing.T) {
	questions := []temporal.PlanningQuestion{
		{
			Question:       "Question one?",
			Options:        []string{"A", "B"},
			Recommendation: "A",
		},
		{
			Question:       "Question two?",
			Options:        []string{"C", "D"},
			Recommendation: "C",
		},
	}
	rawQuestions, err := json.Marshal(questions)
	if err != nil {
		t.Fatalf("marshal questions: %v", err)
	}

	events := []store.PlanningTraceEvent{
		{
			SessionID:   "planning-test",
			Cycle:       2,
			Stage:       "selection",
			EventType:   "item_selected",
			TaskID:      "morsel-a",
			OptionID:    "morsel-a",
			SummaryText: "Top slice",
		},
		{
			SessionID:   "planning-test",
			Cycle:       2,
			Stage:       "generate_questions",
			EventType:   "questions_result",
			TaskID:      "morsel-a",
			OptionID:    "morsel-a",
			FullText:    string(rawQuestions),
			SummaryText: "generated questions",
		},
		{
			SessionID: "planning-test",
			Cycle:     2,
			Stage:     "question_answer",
			EventType: "answer_recorded",
			TaskID:    "morsel-a",
			OptionID:  "morsel-a",
			FullText:  "answer=A",
		},
	}

	resp := buildPlanningPromptResponse("planning-test", "Running", events)
	if resp.Phase != "questioning" {
		t.Fatalf("phase=%q, want questioning", resp.Phase)
	}
	if resp.ExpectedSignal != "answer" {
		t.Fatalf("expected_signal=%q, want answer", resp.ExpectedSignal)
	}
	if resp.Prompt != "Question two?" {
		t.Fatalf("prompt=%q, want Question two?", resp.Prompt)
	}
	if len(resp.Options) != 2 {
		t.Fatalf("len(options)=%d, want 2", len(resp.Options))
	}
}

func TestBuildPlanningPromptResponseGreenlightPhase(t *testing.T) {
	questions := []temporal.PlanningQuestion{
		{
			Question:       "Question one?",
			Options:        []string{"A", "B"},
			Recommendation: "A",
		},
	}
	rawQuestions, err := json.Marshal(questions)
	if err != nil {
		t.Fatalf("marshal questions: %v", err)
	}

	events := []store.PlanningTraceEvent{
		{
			SessionID:   "planning-test",
			Cycle:       3,
			Stage:       "selection",
			EventType:   "item_selected",
			TaskID:      "morsel-z",
			OptionID:    "morsel-z",
			SummaryText: "Z slice",
		},
		{
			SessionID: "planning-test",
			Cycle:     3,
			Stage:     "generate_questions",
			EventType: "questions_result",
			TaskID:    "morsel-z",
			OptionID:  "morsel-z",
			FullText:  string(rawQuestions),
		},
		{
			SessionID: "planning-test",
			Cycle:     3,
			Stage:     "question_answer",
			EventType: "answer_recorded",
			TaskID:    "morsel-z",
			OptionID:  "morsel-z",
		},
		{
			SessionID:   "planning-test",
			Cycle:       3,
			Stage:       "summarize_plan",
			EventType:   "plan_summary_result",
			TaskID:      "morsel-z",
			OptionID:    "morsel-z",
			SummaryText: "Build the selected slice",
		},
	}

	resp := buildPlanningPromptResponse("planning-test", "Running", events)
	if resp.Phase != "greenlight" {
		t.Fatalf("phase=%q, want greenlight", resp.Phase)
	}
	if resp.ExpectedSignal != "greenlight" {
		t.Fatalf("expected_signal=%q, want greenlight", resp.ExpectedSignal)
	}
	if len(resp.Options) != 2 || resp.Options[0] != "GO" {
		t.Fatalf("greenlight options=%v, want [GO REALIGN]", resp.Options)
	}
}

func TestBuildPlanningPromptResponseTimedOutPhase(t *testing.T) {
	events := []store.PlanningTraceEvent{
		{
			SessionID:   "planning-timeout",
			Cycle:       1,
			Stage:       "selection",
			EventType:   "planning_signal_timeout",
			SummaryText: "timed out waiting for item-selected",
		},
	}

	resp := buildPlanningPromptResponse("planning-timeout", "Failed", events)
	if resp.Phase != "timed_out" {
		t.Fatalf("phase=%q, want timed_out", resp.Phase)
	}
	if resp.ExpectedSignal != "" {
		t.Fatalf("expected_signal=%q, want empty", resp.ExpectedSignal)
	}
}
