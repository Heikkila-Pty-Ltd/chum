// Package graph manages the Morsels dependency DAG.

package graph

import (
	"context"
	"sort"
	"strings"
)

// ParseCrossDep splits a dependency ID on the first colon. If a colon is
// present the left side is the project name and the right side is the task ID.
// When no colon is found the dependency is local.
func ParseCrossDep(depID string) (project, taskID string, isCross bool) {
	idx := strings.IndexByte(depID, ':')
	if idx < 0 {
		return "", depID, false
	}
	return depID[:idx], depID[idx+1:], true
}

// CrossDep is already defined in task.go — re-exported here for convenience
// in function signatures that reference it alongside CrossProjectGraph.

// CrossProjectGraph holds task lookups for multiple projects so that
// cross-project dependency resolution can check whether a remote task is
// closed.
type CrossProjectGraph struct {
	Projects map[string]map[string]*Task
}

// BuildCrossProjectGraph loads tasks from the DAG for each project in the
// provided name mapping and indexes them by project and task ID.
func BuildCrossProjectGraph(ctx context.Context, dag *DAG, projects map[string]string) (*CrossProjectGraph, error) {
	cpg := &CrossProjectGraph{
		Projects: make(map[string]map[string]*Task, len(projects)),
	}

	for name := range projects {
		tasks, err := dag.ListTasks(ctx, name)
		if err != nil {
			return nil, err
		}

		index := make(map[string]*Task, len(tasks))
		for i := range tasks {
			t := cloneTask(tasks[i])
			index[t.ID] = &t
		}
		cpg.Projects[name] = index
	}

	return cpg, nil
}

// IsCrossDepResolved returns true when the referenced task exists in the cross
// project graph and has a closed status. Missing projects or tasks are treated
// as unresolved.
func (cpg *CrossProjectGraph) IsCrossDepResolved(project, taskID string) bool {
	if cpg == nil || cpg.Projects == nil {
		return false
	}
	tasks, ok := cpg.Projects[project]
	if !ok {
		return false
	}
	t, ok := tasks[taskID]
	if !ok || t == nil {
		return false
	}
	return isClosedTask(t.Status)
}

// GetCrossProjectBlockers returns all cross-project dependencies declared in
// the task's DependsOn list.
func GetCrossProjectBlockers(t Task) []CrossDep {
	var deps []CrossDep
	for _, depID := range t.DependsOn {
		project, taskID, isCross := ParseCrossDep(depID)
		if isCross {
			deps = append(deps, CrossDep{Project: project, TaskID: taskID})
		}
	}
	return deps
}

// FilterUnblockedCrossProject returns open, non-epic tasks whose local and
// cross-project dependencies are all resolved.
//
// Local dependencies are checked against localGraph (same logic as
// FilterUnblockedOpen). Cross-project dependencies (containing ":") are
// checked against crossGraph.
//
// Results are sorted identically to FilterUnblockedOpen:
//  1. Stage-labeled tasks first
//  2. Priority ascending
//  3. EstimateMinutes ascending
//  4. ID ascending
func FilterUnblockedCrossProject(tasks []Task, localGraph *DepGraph, crossGraph *CrossProjectGraph) []Task {
	result := make([]Task, 0, len(tasks))
	for i := range tasks {
		if !isDispatchableTask(tasks[i]) || isContainerTask(tasks[i]) {
			continue
		}
		if !allDepsClosedCross(tasks[i], localGraph, crossGraph) {
			continue
		}
		result = append(result, cloneTask(tasks[i]))
	}

	sort.Slice(result, func(i, j int) bool {
		iStage := hasStageLabel(result[i])
		jStage := hasStageLabel(result[j])
		if iStage != jStage {
			return iStage
		}
		if result[i].Priority != result[j].Priority {
			return result[i].Priority < result[j].Priority
		}
		if result[i].EstimateMinutes != result[j].EstimateMinutes {
			return result[i].EstimateMinutes < result[j].EstimateMinutes
		}
		return result[i].ID < result[j].ID
	})

	return result
}

// allDepsClosedCross checks both local and cross-project dependencies for a
// task. Local deps are resolved via the DepGraph; cross deps via the
// CrossProjectGraph.
func allDepsClosedCross(task Task, localGraph *DepGraph, crossGraph *CrossProjectGraph) bool {
	// Determine the canonical dependency list: prefer graph-backed if the
	// task is present in the local graph.
	depIDs := task.DependsOn
	if localGraph != nil && localGraph.nodes != nil {
		if _, inGraph := localGraph.nodes[task.ID]; inGraph {
			depIDs = localGraph.DependsOnIDs(task.ID)
		}
	}

	if len(depIDs) == 0 {
		return true
	}

	for _, depID := range depIDs {
		project, taskID, isCross := ParseCrossDep(depID)
		if isCross {
			if crossGraph == nil || !crossGraph.IsCrossDepResolved(project, taskID) {
				return false
			}
			continue
		}

		// Local dependency — resolve via graph nodes.
		if localGraph == nil || localGraph.nodes == nil {
			return false
		}
		dep, ok := localGraph.nodes[depID]
		if !ok || dep == nil || !isClosedTask(dep.Status) {
			return false
		}
	}

	return true
}
