// Package team handles auto-spawning openclaw agent teams for projects.
package team

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
)

// roleDescriptions provides the ROLE.md content for each agent role.
var roleDescriptions = map[string]string{
	"scrum": `# Scrum Master Agent
<!-- role-version: scrum-master-v4 -->

You are the scrum master and primary point of contact for this project.

## Communication (Primary)
- You are the ONLY agent that communicates with humans
- Report progress, blockers, and decisions via Matrix
- When you receive a human message, determine intent and act:
  - Status queries: gather state with ` + "`" + `bd list/show` + "`" + `, summarize
  - Priority changes: reprioritize with ` + "`" + `bd update` + "`" + `
  - Task creation: create beads with ` + "`" + `bd create` + "`" + `
  - Guidance: update design notes on relevant beads
  - Always confirm what you did

## Daily Standup
- Summarize: what completed yesterday, what's in progress, any blockers
- Flag beads that have been in_progress for >24h
- Highlight rate limit status if above 70%

## Task Refinement
- Review task descriptions for clarity and completeness
- Add or improve acceptance criteria
- Break large tasks into sub-tasks
- Estimate effort when missing

## Stage Workflow
- You receive tasks at stage:backlog
- When refinement is complete, transition to stage:planning
- Always unassign yourself after transitioning

## Matrix Command Handling (manual control)
- status
- priority <bead-id> <p0|p1|p2|p3|p4>
- cancel <dispatch-id>
- create task "<title>" "<description>"

### Command Templates
- status
  - Output: project summary with running bead count and recent completion count.
- priority <bead-id> <p0|p1|p2|p3|p4>
  - Output: Updated <bead-id> priority to <pX>.
- cancel <dispatch-id>
  - Output: Cancelled dispatch <dispatch-id> on success or an error reason on failure.
- create task "<title>" "<description>"
  - Output: Created new task <bead-id>.

When confirming command responses, keep replies concise and include the result:
- status -> project summary with running and completion metrics
- priority -> confirmation of priority change
- cancel -> cancellation confirmation or failure reason
- create -> new bead id and confirmation
`,
	"planner": `# Planner Agent

You are the technical planner for this project. Your job is to create implementation plans.

## Responsibilities
- Read acceptance criteria and understand requirements
- Create detailed implementation plans with design notes
- Identify files to create or modify
- Consider edge cases and testing strategy

## Bead Preflight (before stage:ready)
- Scope is clear in description
- Acceptance includes test + DoD lines
- Estimate is set in minutes (>0)

## Stage Workflow
- You receive tasks at **stage:planning**
- When planning is complete, transition to **stage:ready**
- Always unassign yourself after transitioning
`,
	"coder": `# Coder Agent

You are the implementation engineer for this project. Your job is to write code.

## Responsibilities
- Follow the implementation plan and design notes
- Write clean, tested, well-documented code
- Run existing tests before committing
- Create meaningful commit messages

## Stage Workflow
- You receive tasks at **stage:ready** or **stage:coding**
- When implementation is complete, transition to **stage:review**
- Always unassign yourself after transitioning
- Always push your commits
`,
	"reviewer": `# Reviewer Agent

You are the code reviewer for this project. Your job is to review implementations.

## Responsibilities
- Review code changes against acceptance criteria
- Check for correctness, style consistency, and test coverage
- Provide actionable feedback when changes are needed

## Stage Workflow
- You receive tasks at **stage:review**
- If approved, transition to **stage:qa**
- If changes needed, transition back to **stage:coding** with review notes
- Always unassign yourself after transitioning
`,
	"ops": `# QA/Ops Agent

You are the QA and operations engineer for this project. Your job is to validate implementations.

## Responsibilities
- Run the full test suite
- Verify all acceptance criteria are met
- Check for regressions
- Validate deployment readiness

## Stage Workflow
- You receive tasks at **stage:qa**
- If all tests pass and criteria met, close the task with bd close
- If tests fail, transition back to **stage:coding** with failure notes
- Always unassign yourself after transitioning
`,
}

// EnsureTeam checks that all role agents exist for a project and creates missing ones.
// It returns the list of agents that were created.
func EnsureTeam(project, workspace, model string, roles []string, logger *slog.Logger) ([]string, error) {
	agentsDir, err := agentsBasePath()
	if err != nil {
		return nil, fmt.Errorf("team: get agents dir: %w", err)
	}

	var created []string
	for _, role := range roles {
		agentName := project + "-" + role
		agentPath := filepath.Join(agentsDir, agentName)

		existing := false
		if _, err := os.Stat(agentPath); err == nil {
			existing = true
		} else if !os.IsNotExist(err) {
			logger.Error("failed to stat existing agent", "agent", agentName, "error", err)
			continue
		}

		if !existing {
			logger.Info("creating agent", "agent", agentName, "workspace", workspace, "model", model)

			if err := createAgent(agentName, workspace, model); err != nil {
				logger.Error("failed to create agent", "agent", agentName, "error", err)
				continue
			}

			created = append(created, agentName)
			logger.Info("agent created", "agent", agentName)
		}

		if err := writeRoleMD(agentPath, role); err != nil {
			logger.Warn("agent created but failed to write ROLE.md", "agent", agentName, "error", err)
		}
	}

	return created, nil
}

// ListTeam returns the agents that exist for a given project.
func ListTeam(project string, roles []string) ([]AgentInfo, error) {
	agentsDir, err := agentsBasePath()
	if err != nil {
		return nil, fmt.Errorf("team: get agents dir: %w", err)
	}

	var agents []AgentInfo
	for _, role := range roles {
		agentName := project + "-" + role
		agentPath := filepath.Join(agentsDir, agentName)

		info := AgentInfo{
			Name:   agentName,
			Role:   role,
			Exists: false,
		}

		if _, err := os.Stat(agentPath); err == nil {
			info.Exists = true
		}

		agents = append(agents, info)
	}

	return agents, nil
}

// AgentInfo describes an agent's status within a team.
type AgentInfo struct {
	Name   string `json:"name"`
	Role   string `json:"role"`
	Exists bool   `json:"exists"`
}

func agentsBasePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".openclaw", "agents"), nil
}

func createAgent(name, workspace, model string) error {
	cmd := exec.Command("openclaw", "agents", "add", name,
		"--workspace", workspace,
		"--model", model,
		"--non-interactive",
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("openclaw agents add %s: %w\nstderr: %s", name, err, stderr.String())
	}
	return nil
}

func writeRoleMD(agentDir, role string) error {
	content, ok := roleDescriptions[role]
	if !ok {
		return nil // no ROLE.md for unknown roles
	}

	rolePath := filepath.Join(agentDir, "ROLE.md")
	existing, err := os.ReadFile(rolePath)
	if err == nil {
		if bytes.Equal(existing, []byte(content)) {
			return nil
		}
		if role == "scrum" && !bytes.Contains(existing, []byte("role-version: scrum-master-v4")) {
			return os.WriteFile(rolePath, []byte(content), 0644)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}

	return os.WriteFile(rolePath, []byte(content), 0644)
}
