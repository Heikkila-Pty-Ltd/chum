package team

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoleDescriptionsComplete(t *testing.T) {
	expectedRoles := []string{"scrum", "planner", "coder", "reviewer", "ops"}
	for _, role := range expectedRoles {
		if _, ok := roleDescriptions[role]; !ok {
			t.Errorf("missing role description for %q", role)
		}
	}
}

func TestListTeamNoAgents(t *testing.T) {
	// Using a project name that won't have agents created
	agents, err := ListTeam("nonexistent-test-project-xyz", []string{"scrum", "planner", "coder", "reviewer", "ops"})
	if err != nil {
		t.Fatal(err)
	}

	if len(agents) != 5 {
		t.Fatalf("expected 5 agents, got %d", len(agents))
	}

	for _, a := range agents {
		if a.Exists {
			t.Errorf("agent %q should not exist", a.Name)
		}
	}

	// Verify role assignment
	expectedRoles := map[string]string{
		"nonexistent-test-project-xyz-scrum":    "scrum",
		"nonexistent-test-project-xyz-planner":  "planner",
		"nonexistent-test-project-xyz-coder":    "coder",
		"nonexistent-test-project-xyz-reviewer": "reviewer",
		"nonexistent-test-project-xyz-ops":      "ops",
	}

	for _, a := range agents {
		expected, ok := expectedRoles[a.Name]
		if !ok {
			t.Errorf("unexpected agent name %q", a.Name)
			continue
		}
		if a.Role != expected {
			t.Errorf("agent %q has role %q, want %q", a.Name, a.Role, expected)
		}
	}
}

func TestWriteRoleMDCreatesScrumRole(t *testing.T) {
	agentDir := t.TempDir()

	if err := writeRoleMD(agentDir, "scrum"); err != nil {
		t.Fatalf("writeRoleMD: unexpected error: %v", err)
	}

	rolePath := filepath.Join(agentDir, "ROLE.md")
	got, err := os.ReadFile(rolePath)
	if err != nil {
		t.Fatalf("expected ROLE.md to exist: %v", err)
	}

	if string(got) != roleDescriptions["scrum"] {
		t.Fatalf("unexpected scrum role content\nexpected:\n%q\ngot:\n%q", roleDescriptions["scrum"], string(got))
	}
}

func TestWriteRoleMDRefreshesOldScrumRole(t *testing.T) {
	agentDir := t.TempDir()
	rolePath := filepath.Join(agentDir, "ROLE.md")

	legacyRole := `# Scrum Master Agent

You are the scrum master for this project. Your job is to refine incoming tasks.

## Responsibilities
- Review task descriptions for clarity and completeness
- Add or improve acceptance criteria
- Break large tasks into smaller, actionable sub-tasks
- Estimate effort when missing

## Bead Spec Minimum (before handoff)
- Description has clear scope (what is in/out)
- Acceptance includes a concrete test line
- Acceptance includes a DoD line
- Estimate is set in minutes (>0)

## Stage Workflow
- You receive tasks at **stage:backlog**
- When refinement is complete, transition to **stage:planning**
- Always unassign yourself after transitioning
`

	if err := os.WriteFile(rolePath, []byte(legacyRole), 0644); err != nil {
		t.Fatalf("seed legacy role: %v", err)
	}

	if err := writeRoleMD(agentDir, "scrum"); err != nil {
		t.Fatalf("writeRoleMD: unexpected error: %v", err)
	}

	got, err := os.ReadFile(rolePath)
	if err != nil {
		t.Fatalf("expected ROLE.md to exist: %v", err)
	}
	if string(got) != roleDescriptions["scrum"] {
		t.Fatalf("expected legacy role to be refreshed\nexpected:\n%q\ngot:\n%q", roleDescriptions["scrum"], string(got))
	}
}
