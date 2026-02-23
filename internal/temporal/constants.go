package temporal

// DefaultTaskQueue is the Temporal task queue used by CHUM workers and workflows.
const DefaultTaskQueue = "chum-task-queue"

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
