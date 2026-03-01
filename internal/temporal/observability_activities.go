package temporal

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/activity"
)

// RecordHealthEventActivity records a health event to the store from within a
// workflow. This makes crabs, grooming, and other workflows visible to the
// octopus and stingray observability system.
func (a *Activities) RecordHealthEventActivity(ctx context.Context, eventType, details string) error {
	if a.Store == nil {
		return nil
	}
	if err := a.Store.RecordHealthEvent(eventType, details); err != nil {
		return err
	}

	// Threshold escalation: fire exactly once when the count crosses the threshold.
	// Using == (not >) prevents spamming on every subsequent event in the window.
	count, countErr := a.Store.CountRecentHealthEvents(eventType, 1*time.Hour)
	if countErr != nil {
		activity.GetLogger(ctx).Warn("Failed to count recent health events", "error", countErr)
		return nil
	}
	if count == healthEscalationThreshold+1 {
		logger := activity.GetLogger(ctx)
		logger.Error("Health event threshold exceeded",
			"event_type", eventType,
			"count_1h", count,
			"threshold", healthEscalationThreshold)

		if a.Sender != nil && a.DefaultRoom != "" {
			msg := themed("health_escalation", "", map[string]string{
				"event_type": eventType,
				"count":      fmt.Sprintf("%d", count),
			})
			if msg != "" {
				_ = a.Sender.SendMessage(ctx, a.DefaultRoom, msg)
			}
		}
	}
	return nil
}

// RecordOrganismLogActivity persists a structured log entry for any non-shark
// organism. This makes turtles, crabs, learners, groomers, dispatchers, and
// explosions visible to the learner/paleontologist analysis loop.
func (a *Activities) RecordOrganismLogActivity(ctx context.Context, log OrganismLog) error {
	logger := activity.GetLogger(ctx)
	if a.Store == nil {
		logger.Warn(OrcaPrefix + " No store configured, skipping organism log")
		return nil
	}
	if err := a.Store.RecordOrganismLog(
		log.OrganismType, log.WorkflowID, log.TaskID, log.Project,
		log.Status, log.DurationS, log.Details, log.Steps, log.Error,
	); err != nil {
		logger.Error(OrcaPrefix+" Failed to record organism log", "error", err,
			"type", log.OrganismType, "task", log.TaskID)
		return err
	}
	logger.Info(OrcaPrefix+" Organism log recorded",
		"type", log.OrganismType, "task", log.TaskID, "status", log.Status)
	return nil
}
