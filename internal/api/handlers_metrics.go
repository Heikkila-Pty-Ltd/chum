package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// GET /metrics - Prometheus-compatible text format
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	running, _ := s.store.GetRunningDispatches()

	var b strings.Builder
	db := s.store.DB()

	// --- Dispatch counters ---
	var totalDispatches, totalFailed int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dispatches`).Scan(&totalDispatches); err != nil {
		s.logger.Warn("failed to query total dispatches", "error", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM dispatches WHERE status='failed'`).Scan(&totalFailed); err != nil {
		s.logger.Warn("failed to query failed dispatches", "error", err)
	}

	fmt.Fprintf(&b, "# HELP chum_dispatches_total Total number of dispatches\n")
	fmt.Fprintf(&b, "# TYPE chum_dispatches_total counter\n")
	fmt.Fprintf(&b, "chum_dispatches_total %d\n", totalDispatches)

	fmt.Fprintf(&b, "# HELP chum_dispatches_failed_total Total number of failed dispatches\n")
	fmt.Fprintf(&b, "# TYPE chum_dispatches_failed_total counter\n")
	fmt.Fprintf(&b, "chum_dispatches_failed_total %d\n", totalFailed)

	fmt.Fprintf(&b, "# HELP chum_dispatches_running Current running dispatches\n")
	fmt.Fprintf(&b, "# TYPE chum_dispatches_running gauge\n")
	fmt.Fprintf(&b, "chum_dispatches_running %d\n", len(running))

	// Running dispatches by stage
	runningByStage, err := s.store.GetRunningDispatchStageCounts()
	if err != nil {
		s.logger.Warn("failed to get dispatch stage counts", "error", err)
	} else {
		fmt.Fprintf(&b, "# HELP chum_dispatches_running_by_stage Current number of running dispatches by stage\n")
		fmt.Fprintf(&b, "# TYPE chum_dispatches_running_by_stage gauge\n")

		stages := make([]string, 0, len(runningByStage))
		for stage := range runningByStage {
			stages = append(stages, stage)
		}
		sort.Strings(stages)
		for _, stage := range stages {
			fmt.Fprintf(&b, "chum_dispatches_running_by_stage{stage=%q} %d\n", stage, runningByStage[stage])
		}
	}

	// --- Token burn by project × agent × type ---
	fmt.Fprintf(&b, "# HELP chum_tokens_total Total tokens consumed by project, agent, and type\n")
	fmt.Fprintf(&b, "# TYPE chum_tokens_total counter\n")

	tokenRows, err := db.Query(`
		SELECT project, agent, 
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(cache_read_tokens), 0),
			COALESCE(SUM(cache_creation_tokens), 0)
		FROM token_usage GROUP BY project, agent`)
	if err == nil {
		defer tokenRows.Close()
		for tokenRows.Next() {
			var proj, agent string
			var input, output, cacheRead, cacheCreate int64
			if tokenRows.Scan(&proj, &agent, &input, &output, &cacheRead, &cacheCreate) == nil {
				fmt.Fprintf(&b, "chum_tokens_total{project=%q,agent=%q,type=\"input\"} %d\n", proj, agent, input)
				fmt.Fprintf(&b, "chum_tokens_total{project=%q,agent=%q,type=\"output\"} %d\n", proj, agent, output)
				fmt.Fprintf(&b, "chum_tokens_total{project=%q,agent=%q,type=\"cache_read\"} %d\n", proj, agent, cacheRead)
				fmt.Fprintf(&b, "chum_tokens_total{project=%q,agent=%q,type=\"cache_creation\"} %d\n", proj, agent, cacheCreate)
			}
		}
	}

	// --- Cost USD by project × agent ---
	fmt.Fprintf(&b, "# HELP chum_cost_usd_total Estimated USD cost by project and agent\n")
	fmt.Fprintf(&b, "# TYPE chum_cost_usd_total counter\n")

	costRows, err := db.Query(`
		SELECT project, agent, COALESCE(SUM(cost_usd), 0)
		FROM token_usage GROUP BY project, agent`)
	if err == nil {
		defer costRows.Close()
		for costRows.Next() {
			var proj, agent string
			var cost float64
			if costRows.Scan(&proj, &agent, &cost) == nil {
				fmt.Fprintf(&b, "chum_cost_usd_total{project=%q,agent=%q} %.6f\n", proj, agent, cost)
			}
		}
	}

	// --- Token burn by activity (execute, review, plan) ---
	fmt.Fprintf(&b, "# HELP chum_activity_tokens_total Tokens consumed by activity type\n")
	fmt.Fprintf(&b, "# TYPE chum_activity_tokens_total counter\n")

	actRows, err := db.Query(`
		SELECT activity_name,
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0)
		FROM token_usage GROUP BY activity_name`)
	if err == nil {
		defer actRows.Close()
		for actRows.Next() {
			var act string
			var input, output int64
			if actRows.Scan(&act, &input, &output) == nil {
				fmt.Fprintf(&b, "chum_activity_tokens_total{activity=%q,type=\"input\"} %d\n", act, input)
				fmt.Fprintf(&b, "chum_activity_tokens_total{activity=%q,type=\"output\"} %d\n", act, output)
			}
		}
	}

	// --- Per-morsel cost (top 20 spenders) ---
	fmt.Fprintf(&b, "# HELP chum_morsel_cost_usd Per-morsel estimated USD cost (top spenders)\n")
	fmt.Fprintf(&b, "# TYPE chum_morsel_cost_usd gauge\n")

	morselCostRows, err := db.Query(`
		SELECT morsel_id, project, COALESCE(SUM(cost_usd), 0) as total_cost,
			COALESCE(SUM(input_tokens + output_tokens), 0) as total_tokens
		FROM token_usage GROUP BY morsel_id ORDER BY total_cost DESC LIMIT 20`)
	if err == nil {
		defer morselCostRows.Close()
		for morselCostRows.Next() {
			var morselID, proj string
			var cost float64
			var tokens int64
			if morselCostRows.Scan(&morselID, &proj, &cost, &tokens) == nil {
				fmt.Fprintf(&b, "chum_morsel_cost_usd{morsel_id=%q,project=%q} %.6f\n", morselID, proj, cost)
				fmt.Fprintf(&b, "chum_morsel_tokens_total{morsel_id=%q,project=%q} %d\n", morselID, proj, tokens)
			}
		}
	}

	// --- Workflow health: DoD pass/fail/escalate ---
	fmt.Fprintf(&b, "# HELP chum_dod_results_total DoD check results by outcome\n")
	fmt.Fprintf(&b, "# TYPE chum_dod_results_total counter\n")

	var dodPassed, dodFailed, dodTotal int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dod_results WHERE passed = 1`).Scan(&dodPassed); err != nil {
		s.logger.Warn("failed to query dod passed count", "error", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM dod_results WHERE passed = 0`).Scan(&dodFailed); err != nil {
		s.logger.Warn("failed to query dod failed count", "error", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM dod_results`).Scan(&dodTotal); err != nil {
		s.logger.Warn("failed to query dod total count", "error", err)
	}

	fmt.Fprintf(&b, "chum_dod_results_total{result=\"passed\"} %d\n", dodPassed)
	fmt.Fprintf(&b, "chum_dod_results_total{result=\"failed\"} %d\n", dodFailed)

	if dodTotal > 0 {
		fmt.Fprintf(&b, "# HELP chum_dod_pass_rate DoD pass rate (0-1)\n")
		fmt.Fprintf(&b, "# TYPE chum_dod_pass_rate gauge\n")
		fmt.Fprintf(&b, "chum_dod_pass_rate %.4f\n", float64(dodPassed)/float64(dodTotal))
	}

	// --- Dispatch outcomes by status ---
	fmt.Fprintf(&b, "# HELP chum_dispatch_outcomes_total Dispatch outcomes by status\n")
	fmt.Fprintf(&b, "# TYPE chum_dispatch_outcomes_total counter\n")

	statusRows, err := db.Query(`
		SELECT COALESCE(status, 'unknown'), COUNT(*) FROM dispatches GROUP BY status`)
	if err == nil {
		defer statusRows.Close()
		for statusRows.Next() {
			var status string
			var count int
			if statusRows.Scan(&status, &count) == nil {
				fmt.Fprintf(&b, "chum_dispatch_outcomes_total{status=%q} %d\n", status, count)
			}
		}
	}

	// --- Workflow duration (average by status) ---
	fmt.Fprintf(&b, "# HELP chum_dispatch_duration_seconds_avg Average dispatch duration by status\n")
	fmt.Fprintf(&b, "# TYPE chum_dispatch_duration_seconds_avg gauge\n")

	durRows, err := db.Query(`
		SELECT COALESCE(status, 'unknown'), AVG(duration_s), COUNT(*)
		FROM dispatches WHERE duration_s > 0 GROUP BY status`)
	if err == nil {
		defer durRows.Close()
		for durRows.Next() {
			var status string
			var avgDur float64
			var count int
			if durRows.Scan(&status, &avgDur, &count) == nil {
				fmt.Fprintf(&b, "chum_dispatch_duration_seconds_avg{status=%q} %.2f\n", status, avgDur)
				fmt.Fprintf(&b, "chum_dispatch_duration_seconds_count{status=%q} %d\n", status, count)
			}
		}
	}

	// --- Retry / handoff overhead (morsels with multiple dispatches) ---
	fmt.Fprintf(&b, "# HELP chum_morsel_retry_overhead Morsels with highest dispatch attempts (inefficiency indicator)\n")
	fmt.Fprintf(&b, "# TYPE chum_morsel_retry_overhead gauge\n")

	retryRows, err := db.Query(`
		SELECT morsel_id, COUNT(*) as attempts FROM dispatches
		GROUP BY morsel_id HAVING attempts > 1
		ORDER BY attempts DESC LIMIT 10`)
	if err == nil {
		defer retryRows.Close()
		for retryRows.Next() {
			var morselID string
			var attempts int
			if retryRows.Scan(&morselID, &attempts) == nil {
				fmt.Fprintf(&b, "chum_morsel_retry_overhead{morsel_id=%q} %d\n", morselID, attempts)
			}
		}
	}

	// --- Safety block metrics ---
	blockCounts, err := s.store.GetBlockCountsByType()
	if err != nil {
		s.logger.Warn("failed to get safety block counts", "error", err)
	} else {
		fmt.Fprintf(&b, "# HELP chum_safety_blocks_active Active safety blocks by type\n")
		fmt.Fprintf(&b, "# TYPE chum_safety_blocks_active gauge\n")

		types := make([]string, 0, len(blockCounts))
		for bt := range blockCounts {
			types = append(types, bt)
		}
		sort.Strings(types)
		for _, bt := range types {
			fmt.Fprintf(&b, "chum_safety_blocks_active{block_type=%q} %d\n", bt, blockCounts[bt])
		}

		var total int
		for _, c := range blockCounts {
			total += c
		}
		fmt.Fprintf(&b, "# HELP chum_safety_blocks_total Total active safety blocks\n")
		fmt.Fprintf(&b, "# TYPE chum_safety_blocks_total gauge\n")
		fmt.Fprintf(&b, "chum_safety_blocks_total %d\n", total)
	}

	// --- Uptime ---
	fmt.Fprintf(&b, "# HELP chum_uptime_seconds Uptime in seconds\n")
	fmt.Fprintf(&b, "# TYPE chum_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "chum_uptime_seconds %.0f\n", time.Since(s.startTime).Seconds())

	w.Write([]byte(b.String()))
}
