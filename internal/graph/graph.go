package graph

import (
	"sort"
	"strings"
)

const stageLabelPrefix = "stage:"

// DepGraph is a directed dependency graph for tasks.
type DepGraph struct {
	nodes   map[string]*Task
	forward map[string][]string // task -> depends on
	reverse map[string][]string // task -> blocks
}

// BuildDepGraph initializes an in-memory dependency graph from tasks.
func BuildDepGraph(tasks []Task) *DepGraph {
	g := &DepGraph{
		nodes:   make(map[string]*Task, len(tasks)),
		forward: make(map[string][]string, len(tasks)),
		reverse: make(map[string][]string, len(tasks)),
	}
	graphTasks := make([]Task, len(tasks))

	for i := range tasks {
		graphTasks[i] = cloneTask(tasks[i])
		g.nodes[graphTasks[i].ID] = &graphTasks[i]
	}

	for i := range tasks {
		task := &tasks[i]
		if _, ok := g.forward[task.ID]; !ok {
			g.forward[task.ID] = make([]string, 0)
		}
		if _, ok := g.reverse[task.ID]; !ok {
			g.reverse[task.ID] = make([]string, 0)
		}

		if len(task.DependsOn) == 0 {
			continue
		}

		seen := make(map[string]struct{}, len(task.DependsOn))
		for _, depID := range task.DependsOn {
			depID = strings.TrimSpace(depID)
			if depID == "" {
				continue
			}
			if _, dup := seen[depID]; dup {
				continue
			}
			seen[depID] = struct{}{}
			g.forward[task.ID] = append(g.forward[task.ID], depID)
			g.reverse[depID] = append(g.reverse[depID], task.ID)
		}
	}

	return g
}

// Nodes returns a shallow copy of the node lookup map. The map itself is a
// copy (deleting keys won't affect the graph), but the *Task pointers are
// shared with the graph's internal state.
//
// CAUTION: Mutating fields on a returned *Task (e.g. Status, Title) will
// modify the graph's internal state. This is intentional for single-owner
// workflows but unsafe under concurrent access. Callers that need isolation
// should copy individual Task values before mutation.
func (g *DepGraph) Nodes() map[string]*Task {
	if g == nil {
		return nil
	}
	cp := make(map[string]*Task, len(g.nodes))
	for k, v := range g.nodes {
		cp[k] = v
	}
	return cp
}

// DependsOnIDs returns all task IDs the task depends on.
func (g *DepGraph) DependsOnIDs(id string) []string {
	if g == nil || g.forward == nil {
		return nil
	}
	dependencies, ok := g.forward[id]
	if !ok {
		return nil
	}
	return cloneStringSlice(dependencies)
}

// BlocksIDs returns all task IDs directly blocked by the task.
func (g *DepGraph) BlocksIDs(id string) []string {
	if g == nil || g.reverse == nil {
		return nil
	}
	blockers, ok := g.reverse[id]
	if !ok {
		return nil
	}
	return cloneStringSlice(blockers)
}

// FilterUnblockedOpen returns ready (groomed), non-epic tasks whose
// dependencies are all closed.
//
// Results are sorted deterministically:
//  1. Stage-labeled tasks first ("stage:" prefix in labels)
//  2. Priority ascending
//  3. EstimateMinutes ascending
//  4. ID ascending
func FilterUnblockedOpen(tasks []Task, graph *DepGraph) []Task {
	result := make([]Task, 0, len(tasks))
	for i := range tasks {
		if !isDispatchableTask(tasks[i]) || isContainerTask(tasks[i]) {
			continue
		}
		if !allDepsClosed(tasks[i], graph) {
			continue
		}
		result = append(result, cloneTask(tasks[i]))
	}

	sort.Slice(result, func(i, j int) bool {
		// 1. Stage-labeled before non-stage.
		iStage := hasStageLabel(result[i])
		jStage := hasStageLabel(result[j])
		if iStage != jStage {
			return iStage
		}
		// 2. Priority ascending.
		if result[i].Priority != result[j].Priority {
			return result[i].Priority < result[j].Priority
		}
		// 3. EstimateMinutes ascending.
		if result[i].EstimateMinutes != result[j].EstimateMinutes {
			return result[i].EstimateMinutes < result[j].EstimateMinutes
		}
		// 4. ID ascending for determinism.
		return result[i].ID < result[j].ID
	})

	return result
}

func allDepsClosed(task Task, graph *DepGraph) bool {
	if graph == nil || graph.nodes == nil {
		// Without a graph, conservatively require no declared dependencies.
		return len(task.DependsOn) == 0
	}

	// Use graph-backed dependencies for tasks in the graph.
	// Tasks missing from the graph fall back to the input dependency list.
	depIDs := task.DependsOn
	if _, inGraph := graph.nodes[task.ID]; inGraph {
		depIDs = graph.DependsOnIDs(task.ID)
	}

	if len(depIDs) == 0 {
		return true
	}

	for _, depID := range depIDs {
		dep, ok := graph.nodes[depID]
		if !ok || dep == nil || !isClosedTask(dep.Status) {
			return false
		}
	}

	return true
}

// isClosedTask returns true for terminal statuses that satisfy dependency resolution.
// A task is terminal if it is closed, completed, or escalated (failed all retries).
// Escalated tasks are terminal because the organism died through all tiers —
// downstream work should not be blocked forever by a dead ancestor.
func isClosedTask(status string) bool {
	s := normalizeTaskStatus(status)
	return s == statusClosed || s == "completed" || s == "escalated" || s == "plan_failed" || s == "canceled" || s == "done"
}

// isDispatchableTask returns true for tasks with status "ready".
// Tasks enter as "open" (ungroomed) and become "ready" after
// decomposition/grooming. Only "ready" tasks are picked up by sharks.
func isDispatchableTask(task Task) bool {
	s := normalizeTaskStatus(task.Status)
	return s == statusReady
}

// isContainerTask returns true for container types (epics, whales) that
// group work but are not directly executable by sharks.
func isContainerTask(task Task) bool {
	t := strings.ToLower(strings.TrimSpace(task.Type))
	return t == taskTypeEpic || t == taskTypeWhale
}

func hasStageLabel(task Task) bool {
	for _, label := range task.Labels {
		if strings.HasPrefix(label, stageLabelPrefix) {
			return true
		}
	}
	return false
}

// cloneTask returns a value copy of t with independently allocated slices.
// Reference-type fields that must be cloned: DependsOn ([]string), Labels ([]string).
// If Task gains new slice or map fields, add them here.
func cloneTask(t Task) Task {
	if len(t.DependsOn) > 0 {
		cp := make([]string, len(t.DependsOn))
		copy(cp, t.DependsOn)
		t.DependsOn = cp
	}
	if len(t.Labels) > 0 {
		cp := make([]string, len(t.Labels))
		copy(cp, t.Labels)
		t.Labels = cp
	}
	return t
}

func cloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return make([]string, 0)
	}
	cp := make([]string, len(values))
	copy(cp, values)
	return cp
}
