// Package portfolio contains portfolio backlog gathering functions for multi-team sprint planning.
package portfolio

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/graph"
)

// ProjectBacklog represents the backlog for a single project
type ProjectBacklog struct {
	ProjectName     string       `json:"project_name"`
	Workspace       string       `json:"workspace"`
	Priority        int          `json:"priority"`
	UnrefinedBeads  []graph.Task `json:"unrefined_beads"`
	RefinedBeads    []graph.Task `json:"refined_beads"`
	AllBeads        []graph.Task `json:"all_beads"`
	ReadyToWork     []graph.Task `json:"ready_to_work"`
	TotalEstimate   int          `json:"total_estimate_minutes"`
	CapacityPercent int          `json:"capacity_percent"`
}

// CrossProjectDependency represents a dependency between projects
type CrossProjectDependency struct {
	SourceProject string `json:"source_project"`
	SourceBeadID  string `json:"source_bead_id"`
	TargetProject string `json:"target_project"`
	TargetBeadID  string `json:"target_bead_id"`
	BeadTitle     string `json:"bead_title"`
	IsResolved    bool   `json:"is_resolved"`
}

// PortfolioBacklog aggregates backlogs from all projects for multi-team sprint planning
type PortfolioBacklog struct {
	ProjectBacklogs      map[string]ProjectBacklog `json:"project_backlogs"`
	CrossProjectDeps     []CrossProjectDependency  `json:"cross_project_deps"`
	TotalBeadCount       int                       `json:"total_bead_count"`
	TotalEstimateMinutes int                       `json:"total_estimate_minutes"`
	CapacityBudgets      map[string]int            `json:"capacity_budgets"`
	Summary              PortfolioSummary          `json:"summary"`
}

// PortfolioSummary provides high-level statistics about the portfolio
type PortfolioSummary struct {
	ActiveProjects       int      `json:"active_projects"`
	TotalOpenBeads       int      `json:"total_open_beads"`
	TotalRefinedBeads    int      `json:"total_refined_beads"`
	TotalUnrefinedBeads  int      `json:"total_unrefined_beads"`
	TotalReadyToWork     int      `json:"total_ready_to_work"`
	CrossProjectBlockers int      `json:"cross_project_blockers"`
	ProjectsByPriority   []string `json:"projects_by_priority"`
}

// GatherPortfolioBacklogs collects backlog data from all enabled projects for multi-team sprint planning
func GatherPortfolioBacklogs(ctx context.Context, cfg *config.Config, dag *graph.DAG, logger *slog.Logger) (*PortfolioBacklog, error) {
	logger.Info("gathering portfolio backlogs for multi-team sprint planning")

	portfolio := &PortfolioBacklog{
		ProjectBacklogs: make(map[string]ProjectBacklog),
		CapacityBudgets: make(map[string]int),
	}

	// Copy capacity budgets from config
	for project, percent := range cfg.RateLimits.Budget {
		portfolio.CapacityBudgets[project] = percent
	}

	// Build cross-project dependency graph
	projectNames := make(map[string]string)
	var enabledNames []string
	for name, project := range cfg.Projects {
		if !project.Enabled {
			logger.Debug("skipping disabled project", "project", name)
			continue
		}
		projectNames[name] = name
		enabledNames = append(enabledNames, name)
	}

	crossGraph, err := graph.BuildCrossProjectGraph(ctx, dag, projectNames)
	if err != nil {
		return nil, fmt.Errorf("building cross-project graph: %w", err)
	}

	// Sort projects by priority for consistent ordering
	sort.Slice(enabledNames, func(i, j int) bool {
		return cfg.Projects[enabledNames[i]].Priority < cfg.Projects[enabledNames[j]].Priority
	})

	for _, name := range enabledNames {
		project := cfg.Projects[name]

		logger.Debug("gathering backlog", "project", name)

		backlog, err := gatherProjectBacklog(ctx, name, project, dag, crossGraph, logger)
		if err != nil {
			logger.Error("failed to gather project backlog", "project", name, "error", err)
			// Continue with other projects rather than failing completely
			continue
		}

		portfolio.ProjectBacklogs[name] = *backlog
		portfolio.TotalBeadCount += len(backlog.AllBeads)
		portfolio.TotalEstimateMinutes += backlog.TotalEstimate
	}

	// Gather cross-project dependencies
	portfolio.CrossProjectDeps = extractCrossProjectDependencies(crossGraph)

	// Generate summary statistics
	portfolio.Summary = generatePortfolioSummary(portfolio)

	logger.Info("portfolio backlog gathering complete",
		"active_projects", len(portfolio.ProjectBacklogs),
		"total_beads", portfolio.TotalBeadCount,
		"cross_project_deps", len(portfolio.CrossProjectDeps))

	return portfolio, nil
}

// gatherProjectBacklog collects backlog data for a single project
func gatherProjectBacklog(ctx context.Context, projectName string, project config.Project, dag *graph.DAG, crossGraph *graph.CrossProjectGraph, logger *slog.Logger) (*ProjectBacklog, error) {
	// List all tasks for the project
	allTasks, err := dag.ListTasks(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("listing tasks for project %s: %w", projectName, err)
	}

	// Build local dependency graph for this project
	localGraph := graph.BuildDepGraph(allTasks)

	backlog := &ProjectBacklog{
		ProjectName:     projectName,
		Workspace:       config.ExpandHome(project.Workspace),
		Priority:        project.Priority,
		AllBeads:        filterOpenTasks(allTasks),
		CapacityPercent: 0, // Will be set from rate limits budget if available
	}

	// Categorize tasks
	backlog.RefinedBeads = filterRefinedTasks(backlog.AllBeads)
	backlog.UnrefinedBeads = filterUnrefinedTasks(backlog.AllBeads)

	// Find tasks ready to work (unblocked by dependencies)
	backlog.ReadyToWork = graph.FilterUnblockedCrossProject(backlog.AllBeads, localGraph, crossGraph)

	// Calculate total estimate
	for _, task := range backlog.AllBeads {
		backlog.TotalEstimate += task.EstimateMinutes
	}

	logger.Debug("project backlog gathered",
		"project", projectName,
		"total_tasks", len(backlog.AllBeads),
		"refined", len(backlog.RefinedBeads),
		"unrefined", len(backlog.UnrefinedBeads),
		"ready_to_work", len(backlog.ReadyToWork),
		"total_estimate_minutes", backlog.TotalEstimate)

	return backlog, nil
}

// filterOpenTasks returns only open tasks (excludes closed, cancelled, etc.)
func filterOpenTasks(allTasks []graph.Task) []graph.Task {
	var open []graph.Task
	for _, task := range allTasks {
		if task.Status == "open" {
			open = append(open, task)
		}
	}
	return open
}

// filterRefinedTasks returns tasks that have acceptance criteria or design notes (considered refined)
func filterRefinedTasks(openTasks []graph.Task) []graph.Task {
	var refined []graph.Task
	for _, task := range openTasks {
		if task.Acceptance != "" || task.Design != "" || task.EstimateMinutes > 0 {
			refined = append(refined, task)
		}
	}
	return refined
}

// filterUnrefinedTasks returns tasks that lack acceptance criteria and design notes
func filterUnrefinedTasks(openTasks []graph.Task) []graph.Task {
	var unrefined []graph.Task
	for _, task := range openTasks {
		if task.Acceptance == "" && task.Design == "" && task.EstimateMinutes == 0 {
			unrefined = append(unrefined, task)
		}
	}
	return unrefined
}

// extractCrossProjectDependencies finds all cross-project dependencies in the graph
func extractCrossProjectDependencies(crossGraph *graph.CrossProjectGraph) []CrossProjectDependency {
	var deps []CrossProjectDependency

	for projectName, projectTasks := range crossGraph.Projects {
		for _, task := range projectTasks {
			if task.Status != "open" {
				continue // Only consider open tasks
			}

			for _, depID := range task.DependsOn {
				targetProject, targetBeadID, isCross := graph.ParseCrossDep(depID)
				if !isCross {
					continue // Skip local dependencies
				}

				// Get the target task title if possible
				var beadTitle string
				if targetProjectTasks, exists := crossGraph.Projects[targetProject]; exists {
					if targetTask, exists := targetProjectTasks[targetBeadID]; exists {
						beadTitle = targetTask.Title
					}
				}

				dep := CrossProjectDependency{
					SourceProject: projectName,
					SourceBeadID:  task.ID,
					TargetProject: targetProject,
					TargetBeadID:  targetBeadID,
					BeadTitle:     beadTitle,
					IsResolved:    crossGraph.IsCrossDepResolved(targetProject, targetBeadID),
				}

				deps = append(deps, dep)
			}
		}
	}

	// Sort dependencies for consistent ordering
	sort.Slice(deps, func(i, j int) bool {
		if deps[i].SourceProject != deps[j].SourceProject {
			return deps[i].SourceProject < deps[j].SourceProject
		}
		if deps[i].TargetProject != deps[j].TargetProject {
			return deps[i].TargetProject < deps[j].TargetProject
		}
		return deps[i].SourceBeadID < deps[j].SourceBeadID
	})

	return deps
}

// generatePortfolioSummary creates high-level statistics about the portfolio
func generatePortfolioSummary(portfolio *PortfolioBacklog) PortfolioSummary {
	summary := PortfolioSummary{
		ActiveProjects:     len(portfolio.ProjectBacklogs),
		ProjectsByPriority: make([]string, 0, len(portfolio.ProjectBacklogs)),
	}

	// Collect project names sorted by priority
	type projectPrio struct {
		name     string
		priority int
	}
	var projects []projectPrio

	for name, backlog := range portfolio.ProjectBacklogs {
		projects = append(projects, projectPrio{name: name, priority: backlog.Priority})
		summary.TotalOpenBeads += len(backlog.AllBeads)
		summary.TotalRefinedBeads += len(backlog.RefinedBeads)
		summary.TotalUnrefinedBeads += len(backlog.UnrefinedBeads)
		summary.TotalReadyToWork += len(backlog.ReadyToWork)
	}

	// Sort projects by priority
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].priority < projects[j].priority
	})

	for _, p := range projects {
		summary.ProjectsByPriority = append(summary.ProjectsByPriority, p.name)
	}

	// Count unresolved cross-project dependencies
	for _, dep := range portfolio.CrossProjectDeps {
		if !dep.IsResolved {
			summary.CrossProjectBlockers++
		}
	}

	return summary
}

// GetProjectCapacityBudget returns the capacity budget percentage for a project
func GetProjectCapacityBudget(portfolio *PortfolioBacklog, projectName string) int {
	if budget, exists := portfolio.CapacityBudgets[projectName]; exists {
		return budget
	}
	return 0
}

// GetCrossProjectBlockersForProject returns all unresolved cross-project dependencies blocking a project
func GetCrossProjectBlockersForProject(portfolio *PortfolioBacklog, projectName string) []CrossProjectDependency {
	var blockers []CrossProjectDependency

	for _, dep := range portfolio.CrossProjectDeps {
		if dep.SourceProject == projectName && !dep.IsResolved {
			blockers = append(blockers, dep)
		}
	}

	return blockers
}

// GetHighPriorityProjects returns projects ordered by priority (highest priority first)
func GetHighPriorityProjects(portfolio *PortfolioBacklog) []string {
	return portfolio.Summary.ProjectsByPriority
}
