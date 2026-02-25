package matrix

import "testing"

func TestParsePlanningCommandStart(t *testing.T) {
	cmd, matched, err := parsePlanningCommand("/plan start chum /tmp/chum agent=codex tier=premium topk=7")
	if !matched {
		t.Fatal("expected planning command to match")
	}
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if cmd.kind != planningCommandStart {
		t.Fatalf("kind=%v, want planningCommandStart", cmd.kind)
	}
	if cmd.project != "chum" {
		t.Fatalf("project=%q, want chum", cmd.project)
	}
	if cmd.workDir != "/tmp/chum" {
		t.Fatalf("workDir=%q, want /tmp/chum", cmd.workDir)
	}
	if cmd.agent != "codex" {
		t.Fatalf("agent=%q, want codex", cmd.agent)
	}
	if cmd.tier != "premium" {
		t.Fatalf("tier=%q, want premium", cmd.tier)
	}
	if cmd.candidateTopK != 7 {
		t.Fatalf("candidateTopK=%d, want 7", cmd.candidateTopK)
	}
}

func TestParsePlanningCommandAnswerWithSession(t *testing.T) {
	cmd, matched, err := parsePlanningCommand("/plan answer planning-chum-123 We should use option A because of rollout risk")
	if !matched {
		t.Fatal("expected planning command to match")
	}
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if cmd.kind != planningCommandAnswer {
		t.Fatalf("kind=%v, want planningCommandAnswer", cmd.kind)
	}
	if cmd.sessionID != "planning-chum-123" {
		t.Fatalf("sessionID=%q, want planning-chum-123", cmd.sessionID)
	}
	if cmd.value != "We should use option A because of rollout risk" {
		t.Fatalf("value=%q unexpected", cmd.value)
	}
}

func TestParsePlanningCommandSelectWithSessionSuffix(t *testing.T) {
	cmd, matched, err := parsePlanningCommand("plan select morsel-9 planning-chum-555")
	if !matched {
		t.Fatal("expected planning command to match")
	}
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if cmd.kind != planningCommandSelect {
		t.Fatalf("kind=%v, want planningCommandSelect", cmd.kind)
	}
	if cmd.value != "morsel-9" {
		t.Fatalf("value=%q, want morsel-9", cmd.value)
	}
	if cmd.sessionID != "planning-chum-555" {
		t.Fatalf("sessionID=%q, want planning-chum-555", cmd.sessionID)
	}
}

func TestParsePlanningCommandNonPlanningMessage(t *testing.T) {
	_, matched, err := parsePlanningCommand("hello team")
	if matched {
		t.Fatal("expected non-planning message to be ignored")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
