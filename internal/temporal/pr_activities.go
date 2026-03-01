package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"go.temporal.io/sdk/activity"
)

// ReviewPRActivity fetches a PR diff via gh CLI, sends it to a cross-model
// reviewer, and posts the review as a PR comment. Non-fatal throughout.
func (a *Activities) ReviewPRActivity(ctx context.Context, req PRReviewRequest) (*PRReviewResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" PR review starting", "pr", req.PRNumber, "author", req.Author)

	workspace := req.Workspace
	prNum := fmt.Sprintf("%d", req.PRNumber)

	// 1. Fetch PR metadata
	viewCmd := exec.CommandContext(ctx, "gh", "pr", "view", prNum,
		"--json", "title,body,headRefName")
	viewCmd.Dir = workspace
	viewOut, err := viewCmd.CombinedOutput()
	if err != nil {
		logger.Warn(SharkPrefix+" Failed to fetch PR metadata", "error", err, "output", string(viewOut))
		return &PRReviewResult{Approved: true, Issues: []string{"Failed to fetch PR metadata: " + err.Error()}}, nil
	}

	var prMeta struct {
		Title       string `json:"title"`
		Body        string `json:"body"`
		HeadRefName string `json:"headRefName"`
	}
	if jsonErr := json.Unmarshal(viewOut, &prMeta); jsonErr != nil {
		logger.Warn(SharkPrefix+" Failed to parse PR metadata", "error", jsonErr)
	}

	// 2. Fetch PR diff
	diffCmd := exec.CommandContext(ctx, "gh", "pr", "diff", prNum)
	diffCmd.Dir = workspace
	diffOut, err := diffCmd.CombinedOutput()
	if err != nil {
		logger.Warn(SharkPrefix+" Failed to fetch PR diff", "error", err, "output", string(diffOut))
		return &PRReviewResult{Approved: true, Issues: []string{"Failed to fetch PR diff: " + err.Error()}}, nil
	}

	diff := string(diffOut)
	if len(diff) > 12000 {
		diff = diff[:12000] + "\n\n... (diff truncated at 12000 chars)"
	}

	// 3. Select reviewer
	reviewer := req.Reviewer
	if reviewer == "" {
		reviewer = DefaultReviewer(req.Author)
	}
	logger.Info(SharkPrefix+" PR review", "Reviewer", reviewer, "Author", req.Author, "PR", req.PRNumber)

	// 4. Build review prompt
	prompt := fmt.Sprintf(`You are a senior code reviewer performing a cross-model review of PR #%d.

PR TITLE: %s
PR DESCRIPTION: %s

DIFF:
%s

Review the changes carefully. Focus on:
1. Bugs and logic errors
2. Race conditions and concurrency issues
3. Error handling gaps
4. Security vulnerabilities
5. Performance concerns
6. Code style and maintainability

Respond with a JSON object FIRST, then provide detailed explanation:
{
  "approved": true/false,
  "issues": ["critical issues that must be fixed"],
  "suggestions": ["non-blocking improvements"]
}

Be rigorous but fair. Flag real problems, not style preferences.`,
		req.PRNumber,
		prMeta.Title,
		truncate(prMeta.Body, 1000),
		diff,
	)

	// 5. Run cross-model review
	cliResult, err := a.runReviewAgent(ctx, reviewer, prompt, workspace)
	if err != nil {
		logger.Warn(SharkPrefix+" Review agent failed", "error", err)
		return &PRReviewResult{
			Approved:      true,
			Issues:        []string{"Review agent failed: " + err.Error()},
			ReviewerAgent: reviewer,
		}, nil
	}

	// 6. Parse structured result
	result := &PRReviewResult{
		Approved:      true,
		ReviewerAgent: reviewer,
	}
	jsonStr := extractJSON(cliResult.Output)
	if jsonStr != "" {
		if parseErr := robustParseJSON(jsonStr, result); parseErr != nil {
			logger.Warn(SharkPrefix+" Failed to parse review JSON", "error", parseErr)
		}
	}
	result.ReviewerAgent = reviewer

	// 7. Format and post comment
	var comment strings.Builder
	comment.WriteString("## Cross-Model PR Review\n\n")
	comment.WriteString(fmt.Sprintf("**Reviewer:** %s", reviewer))
	if req.Author != "" {
		comment.WriteString(fmt.Sprintf(" (reviewing %s's work)", req.Author))
	}
	comment.WriteString("\n")

	if result.Approved {
		comment.WriteString("**Verdict:** Approved\n\n")
	} else {
		comment.WriteString("**Verdict:** Changes Requested\n\n")
	}

	if len(result.Issues) > 0 {
		comment.WriteString("### Issues\n")
		for _, issue := range result.Issues {
			comment.WriteString(fmt.Sprintf("- %s\n", issue))
		}
		comment.WriteString("\n")
	}

	if len(result.Suggestions) > 0 {
		comment.WriteString("### Suggestions\n")
		for _, s := range result.Suggestions {
			comment.WriteString(fmt.Sprintf("- %s\n", s))
		}
		comment.WriteString("\n")
	}

	// Include the raw review output if it has useful content beyond JSON
	rawReview := strings.TrimSpace(cliResult.Output)
	if jsonStr != "" {
		rawReview = strings.TrimSpace(strings.Replace(rawReview, jsonStr, "", 1))
	}
	if len(rawReview) > 100 {
		comment.WriteString("### Detailed Review\n\n")
		comment.WriteString(truncate(rawReview, 4000))
		comment.WriteString("\n\n")
	}

	comment.WriteString("---\n*Automated review by CHUM*\n")

	// Post comment via gh
	commentCmd := exec.CommandContext(ctx, "gh", "pr", "comment", prNum,
		"--body", comment.String())
	commentCmd.Dir = workspace
	if commentOut, commentErr := commentCmd.CombinedOutput(); commentErr != nil {
		logger.Warn(SharkPrefix+" Failed to post PR comment", "error", commentErr, "output", string(commentOut))
		if a.Store != nil {
			_ = a.Store.RecordHealthEvent("pr_review_comment_failed",
				fmt.Sprintf("PR #%d: %v", req.PRNumber, commentErr))
		}
	} else {
		logger.Info(SharkPrefix+" PR review comment posted", "pr", req.PRNumber, "reviewer", reviewer)
	}

	return result, nil
}

// ScanOpenPRsActivity lists open PRs via gh CLI and returns those that haven't
// been reviewed by CHUM yet (no comment containing "Cross-Model PR Review").
// This enables the poller to catch PRs created by any source — sharks, humans,
// or external tools.
func (a *Activities) ScanOpenPRsActivity(ctx context.Context, req PRReviewPollerRequest) ([]UnreviewedPR, error) {
	logger := activity.GetLogger(ctx)

	// List open PRs as JSON
	listCmd := exec.CommandContext(ctx, "gh", "pr", "list",
		"--state", "open",
		"--json", "number,headRefName,author",
		"--limit", "20",
	)
	listCmd.Dir = req.Workspace
	listOut, err := listCmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh pr list failed: %w (%s)", err, string(listOut))
	}

	var prs []struct {
		Number      int    `json:"number"`
		HeadRefName string `json:"headRefName"`
		Author      struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	if err := json.Unmarshal(listOut, &prs); err != nil {
		return nil, fmt.Errorf("parse PR list: %w", err)
	}

	if len(prs) == 0 {
		return nil, nil
	}

	unreviewed := make([]UnreviewedPR, 0, len(prs))
	for _, pr := range prs {
		// Check if CHUM has already commented on this PR
		commentsCmd := exec.CommandContext(ctx, "gh", "pr", "view",
			fmt.Sprintf("%d", pr.Number),
			"--json", "comments",
		)
		commentsCmd.Dir = req.Workspace
		commentsOut, err := commentsCmd.CombinedOutput()
		if err != nil {
			logger.Warn("Failed to check PR comments", "pr", pr.Number, "error", err)
			continue
		}

		// Look for the CHUM review signature in existing comments
		if strings.Contains(string(commentsOut), "Cross-Model PR Review") {
			continue // already reviewed
		}

		// Map branch prefix to author agent for cross-model selection
		author := inferAuthorAgent(pr.HeadRefName)

		unreviewed = append(unreviewed, UnreviewedPR{
			Number: pr.Number,
			Author: author,
		})
	}

	logger.Info("PR scan complete", "open", len(prs), "unreviewed", len(unreviewed))
	return unreviewed, nil
}

// inferAuthorAgent guesses which CLI agent created a PR based on branch name
// conventions. Falls back to "claude" for unknown patterns.
func inferAuthorAgent(branch string) string {
	lower := strings.ToLower(branch)
	switch {
	case strings.Contains(lower, "codex"):
		return "codex"
	case strings.Contains(lower, "gemini"):
		return "gemini"
	case strings.Contains(lower, "chum/"):
		return "claude" // CHUM sharks default to claude
	default:
		return "claude" // human PRs or unknown — review with codex (via DefaultReviewer)
	}
}

// ExplosionCandidate holds data about a single explosion candidate for senior review.
type ExplosionCandidate struct {
	Provider    string
	ExplosionID string
	Diff        string // git diff output
	ElapsedS    float64
}

// ReviewExplosionCandidatesActivity uses a senior model to compare multiple DoD-passing
// implementations and pick the best one. Returns the index of the winner (0-based).
func (a *Activities) ReviewExplosionCandidatesActivity(ctx context.Context, taskID string, candidates []ExplosionCandidate) (int, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" Senior review of explosion candidates",
		"TaskID", taskID, "Candidates", len(candidates))

	if len(candidates) == 1 {
		return 0, nil // Only one candidate — it wins by default
	}

	// Build a comparison prompt with all diffs
	var promptBuilder strings.Builder
	promptBuilder.WriteString(fmt.Sprintf("You are a senior engineering lead. %d AI agents all attempted the same task and passed their build checks.\n", len(candidates)))
	promptBuilder.WriteString("Compare their implementations and pick the BEST one.\n\n")
	promptBuilder.WriteString("Evaluate on: code quality, simplicity, correctness, maintainability, and efficiency.\n\n")

	for i, c := range candidates {
		promptBuilder.WriteString(fmt.Sprintf("=== CANDIDATE %d: %s (completed in %.0fs) ===\n", i+1, c.Provider, c.ElapsedS))
		diff := c.Diff
		if len(diff) > 4000 {
			diff = diff[:4000] + "\n... [truncated]"
		}
		promptBuilder.WriteString(diff)
		promptBuilder.WriteString("\n\n")
	}

	promptBuilder.WriteString(`Respond with ONLY a JSON object:
{
  "winner": <1-based candidate number>,
  "rationale": "brief explanation of why this implementation is best",
  "patterns": {
    "good": ["pattern 1 from winner", "pattern 2"],
    "bad": ["anti-pattern from losers", "issue found"]
  }
}`)

	// Use gemini as the senior reviewer for explosion comparison
	reviewer := "gemini"
	cliResult, err := a.runReviewAgent(ctx, reviewer, promptBuilder.String(), "")
	if err != nil {
		logger.Warn(SharkPrefix+" Senior review failed — falling back to fastest candidate", "error", err)
		return 0, nil // Fall back to first candidate (fastest)
	}

	jsonStr := extractJSON(cliResult.Output)
	if jsonStr == "" {
		logger.Warn(SharkPrefix + " Senior review output was not valid JSON — falling back to fastest")
		return 0, nil
	}

	// Parse the winner index
	var reviewResult struct {
		Winner    int    `json:"winner"`
		Rationale string `json:"rationale"`
		Patterns  struct {
			Good []string `json:"good"`
			Bad  []string `json:"bad"`
		} `json:"patterns"`
	}
	if err := robustParseJSON(jsonStr, &reviewResult); err != nil {
		logger.Warn(SharkPrefix+" Failed to parse review JSON", "error", err)
		return 0, nil
	}

	winnerIdx := reviewResult.Winner - 1 // convert 1-based to 0-based
	if winnerIdx < 0 || winnerIdx >= len(candidates) {
		logger.Warn(SharkPrefix+" Invalid winner index from reviewer", "winner", reviewResult.Winner)
		return 0, nil
	}

	logger.Info(SharkPrefix+" Senior review complete",
		"Winner", candidates[winnerIdx].Provider,
		"Rationale", reviewResult.Rationale,
		"GoodPatterns", len(reviewResult.Patterns.Good),
		"BadPatterns", len(reviewResult.Patterns.Bad))

	return winnerIdx, nil
}
