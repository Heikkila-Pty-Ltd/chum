package temporal

// DefaultTaskQueue is the Temporal task queue used by CHUM workers and workflows.
const DefaultTaskQueue = "chum-task-queue"

// ResolvedTaskQueue is the actual task queue name used at runtime.
// Set during StartWorker from config, falls back to DefaultTaskQueue.
var ResolvedTaskQueue = DefaultTaskQueue

// ResolveTaskQueue returns the configured task queue or the default.
func ResolveTaskQueue(configured string) string {
	if configured != "" {
		return configured
	}
	return DefaultTaskQueue
}

// DefaultTemporalHostPort is the default Temporal server address.
const DefaultTemporalHostPort = "127.0.0.1:7233"

// Search attribute names registered with Temporal for workflow visibility.
// Prefixed with "Chum" to avoid collisions with Temporal built-in attributes.
const (
	SAProject      = "ChumProject"      // Keyword — project name from config
	SAPriority     = "ChumPriority"     // Int — task priority (0-4)
	SAAgent        = "ChumAgent"        // Keyword — assigned agent name (claude/codex/gemini)
	SACurrentStage = "ChumCurrentStage" // Keyword — current workflow stage
	SATaskTitle    = "ChumTaskTitle"    // Text — task title for search
)
