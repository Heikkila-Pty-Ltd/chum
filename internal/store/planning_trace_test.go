package store

import "testing"

func TestPlanningTraceTableCreatedOnOpen(t *testing.T) {
	s := tempStore(t)

	var count int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM pragma_table_info('planning_trace_events')`).Scan(&count); err != nil {
		t.Fatalf("pragma_table_info(planning_trace_events) failed: %v", err)
	}
	if count == 0 {
		t.Fatal("planning_trace_events table not created")
	}
}

func TestRecordAndListPlanningTraceEvents(t *testing.T) {
	s := tempStore(t)

	err := s.RecordPlanningTraceEvent(PlanningTraceEvent{
		SessionID:      "planning-chum-1",
		RunID:          "run-abc",
		Project:        "chum",
		TaskID:         "morsel-42",
		Cycle:          1,
		Stage:          "groom_backlog",
		EventType:      "llm_call",
		Actor:          "codex",
		ToolName:       "runAgent",
		ToolInput:      "prompt body",
		ToolOutput:     "raw model output",
		PromptText:     "prompt body",
		ResponseText:   "raw model output",
		SummaryText:    "Selected highest-value slice",
		FullText:       "Full prompt and response text for replay",
		SelectedOption: "option-a",
		Reward:         1.0,
		MetadataJSON:   `{"signal":"greenlight"}`,
	})
	if err != nil {
		t.Fatalf("RecordPlanningTraceEvent failed: %v", err)
	}

	events, err := s.ListPlanningTraceEvents("planning-chum-1", 10)
	if err != nil {
		t.Fatalf("ListPlanningTraceEvents failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}

	got := events[0]
	if got.EventType != "llm_call" {
		t.Fatalf("event_type = %q, want llm_call", got.EventType)
	}
	if got.FullText != "Full prompt and response text for replay" {
		t.Fatalf("full_text = %q, want full fidelity text", got.FullText)
	}
	if got.Reward != 1.0 {
		t.Fatalf("reward = %f, want 1.0", got.Reward)
	}
	if got.SelectedOption != "option-a" {
		t.Fatalf("selected_option = %q, want option-a", got.SelectedOption)
	}
	if got.HumanInteractive {
		t.Fatal("human_interactive = true, want false for llm_call")
	}
}

func TestRecordPlanningTraceRequiresSessionAndEventType(t *testing.T) {
	s := tempStore(t)

	if err := s.RecordPlanningTraceEvent(PlanningTraceEvent{
		EventType: "llm_call",
	}); err == nil {
		t.Fatal("expected error when session_id is missing")
	}

	if err := s.RecordPlanningTraceEvent(PlanningTraceEvent{
		SessionID: "planning-1",
	}); err == nil {
		t.Fatal("expected error when event_type is missing")
	}
}

func TestRecordPlanningTraceAutoClassifiesHumanInteraction(t *testing.T) {
	s := tempStore(t)

	err := s.RecordPlanningTraceEvent(PlanningTraceEvent{
		SessionID:    "planning-chum-2",
		EventType:    "control_signal_submitted",
		Actor:        "matrix-control",
		SummaryText:  "signal answer submitted",
		MetadataJSON: `{"signal":"answer","value":"Option A"}`,
	})
	if err != nil {
		t.Fatalf("RecordPlanningTraceEvent failed: %v", err)
	}

	events, err := s.ListPlanningTraceEvents("planning-chum-2", 10)
	if err != nil {
		t.Fatalf("ListPlanningTraceEvents failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}

	got := events[0]
	if !got.HumanInteractive {
		t.Fatal("human_interactive = false, want true")
	}
	if got.InteractionClass != "human_clarification" {
		t.Fatalf("interaction_class = %q, want human_clarification", got.InteractionClass)
	}
	if got.InteractionType != "answer_question" {
		t.Fatalf("interaction_type = %q, want answer_question", got.InteractionType)
	}
}

func TestRecordPlanningTraceClassifiesControlChecksAsHumanInteractive(t *testing.T) {
	s := tempStore(t)

	inputs := []PlanningTraceEvent{
		{
			SessionID: "planning-chum-3",
			EventType: "control_status_requested",
			Actor:     "matrix-control",
		},
		{
			SessionID: "planning-chum-3",
			EventType: "control_prompt_presented",
			Actor:     "matrix-control",
		},
	}

	for _, event := range inputs {
		if err := s.RecordPlanningTraceEvent(event); err != nil {
			t.Fatalf("RecordPlanningTraceEvent failed: %v", err)
		}
	}

	events, err := s.ListPlanningTraceEvents("planning-chum-3", 10)
	if err != nil {
		t.Fatalf("ListPlanningTraceEvents failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2", len(events))
	}

	if !events[0].HumanInteractive || events[0].InteractionType != "status_check" {
		t.Fatalf("status event classified as (%t,%q), want (true,status_check)", events[0].HumanInteractive, events[0].InteractionType)
	}
	if !events[1].HumanInteractive || events[1].InteractionType != "request_prompt" {
		t.Fatalf("prompt event classified as (%t,%q), want (true,request_prompt)", events[1].HumanInteractive, events[1].InteractionType)
	}
}
