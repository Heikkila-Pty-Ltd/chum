package temporal

const (
	SharkPrefix   = "\033[36m🦈 SHARK\033[0m"
	OrcaPrefix    = "\033[33m🐋 ORCA\033[0m"
	OctopusPrefix = "\033[35m🐙 OCTOPUS\033[0m"
	RemoraPrefix  = "\033[32m🐟 REMORA\033[0m"
	CrabPrefix    = "\033[31m🦀 CRAB\033[0m"
	WhalePrefix   = "\033[34m🐳 WHALE\033[0m"

	// healthEscalationThreshold is the number of identical health events in 1 hour
	// that triggers an ERROR log and Matrix notification.
	healthEscalationThreshold = 5
)
