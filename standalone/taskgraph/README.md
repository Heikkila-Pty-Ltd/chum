# TaskGraph

TaskGraph is a hardened, production-ready SQLite-backed directed acyclic graph for task dependency management. Extracted and refactored from Cortex for use in Chum v2.

## Features

- **Sophisticated Dependency Management**: Full DAG with cycle detection and cross-project validation
- **Smart Status Transitions**: Automatic unblocking of dependent tasks when prerequisites complete
- **Robust Edge Management**: Prevents self-loops, validates project boundaries, maintains referential integrity
- **Transaction Safety**: WAL mode with foreign key constraints for data consistency
- **Flexible Task Model**: Rich metadata with labels, priorities, estimates, acceptance criteria
- **Auto-Promotion**: Open tasks automatically become ready when all dependencies complete

## Key Improvements over Chum's Basic DAG

1. **Advanced Cycle Detection**: Uses recursive CTEs to prevent circular dependencies
2. **Status Validation**: Validates transitions and blocks invalid state changes  
3. **Auto-Unblocking**: Automatically promotes tasks when dependencies resolve
4. **Project Boundaries**: Prevents cross-project dependencies that could break isolation
5. **Robust Schema**: Proper foreign keys, constraints, and indexes for performance
6. **Rich Metadata**: Full task lifecycle tracking with timestamps and error logs

## Usage

```go
// Create a new task graph
tg, err := taskgraph.Open("tasks.db")
if err != nil {
    log.Fatal(err)
}
defer tg.Close()

// Create a task
id, err := tg.CreateTask(ctx, taskgraph.Task{
    Title:       "Implement feature X",
    Description: "Add the new functionality",
    Project:     "myproject",
    Status:      taskgraph.StatusOpen,
    Priority:    1,
})

// Add dependencies
err = tg.AddEdge(ctx, dependentTaskID, prerequisiteTaskID)

// Get ready tasks (all dependencies complete)
ready, err := tg.GetReadyNodes(ctx, "myproject")

// Mark task complete and auto-unblock dependents
err = tg.UpdateTask(ctx, id, map[string]any{"status": taskgraph.StatusCompleted})
promoted, err := tg.AutoUnblockDependents(ctx, id)
```

## Integration with Chum v2

This module can be imported into Chum v2 to replace the basic DAG implementation:

```go
import "github.com/cortex-standalone/taskgraph"

// Replace chum's dag.New() with:
tg, err := taskgraph.Open(dbPath)
```

The API is designed for seamless integration while providing significantly more functionality and robustness.