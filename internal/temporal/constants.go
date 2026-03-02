package temporal

// DefaultTaskQueue is the Temporal task queue used by CHUM workers and workflows.
const DefaultTaskQueue = "chum-task-queue"

// DefaultTemporalHostPort is the default Temporal server address.
const DefaultTemporalHostPort = "127.0.0.1:7233"

// DefaultMaxAgentIterations is the default tool-call iteration budget for agent runs.
// Most CLI agents (Claude, Codex) support ~50 tool calls per session.
const DefaultMaxAgentIterations = 50

// IterationWrapUpMargin is how many iterations before the limit to inject
// the wrap-up instruction. At iteration (max - margin), the agent is told
// to stop calling tools and summarize progress + remaining work.
const IterationWrapUpMargin = 2
