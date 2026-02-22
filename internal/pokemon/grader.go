package pokemon

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type Grader struct {
	cfg    *Config
	logger *slog.Logger
	client *http.Client
}

func NewGrader(cfg *Config, logger *slog.Logger) *Grader {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg == nil {
		cfg = &Config{}
	}

	return &Grader{
		cfg:    cfg,
		logger: logger,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (g *Grader) GradeCard(imagePath string) (*ConditionGrade, error) {
	if g == nil {
		return nil, errors.New("grader is nil")
	}
	if g.cfg == nil {
		return nil, errors.New("grader config is nil")
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("ANTHROPIC_API_KEY is empty")
	}

	data, err := os.ReadFile(imagePath)
	if err != nil {
		return nil, err
	}

	mediaType, err := DetectMediaType(data)
	if err != nil {
		return nil, err
	}

	payload := base64.StdEncoding.EncodeToString(data)
	response, err := callAnthropicVisionAPI(context.Background(), g.client, g.cfg.Vision.Model, apiKey, g.buildGradingPrompt(), mediaType, payload)
	if err != nil {
		return nil, err
	}

	var grade ConditionGrade
	if err := parseAnthropicJSONResponse(response.Content, &grade); err != nil {
		return nil, err
	}

	axes := g.defaultAxes()
	if err := validateSubGrades(grade.SubGrades, axes); err != nil {
		return nil, err
	}

	normalizeOverallAndNotes(&grade, g.cfg.Grading.Disclaimer)
	return &grade, nil
}

func (g *Grader) buildGradingPrompt() string {
	axes := g.defaultAxes()
	scale := g.cfg.Grading.Scale
	if strings.TrimSpace(scale) == "" {
		scale = "PSA"
	}

	prompt := fmt.Sprintf(`You are a conservative Pokémon card grader. Grade the card on these axes using the %s scale (1-10): %s.
For each axis, return a numeric score and justification-friendly summary note.
Use a conservative bias when uncertain: prefer the lower end when ambiguous.
Return only strict JSON with this structure:
{
  "overall": 7.0,
  "sub_grades": [
    {"axis": "centering", "score": 7.0}
  ],
  "notes": "brief grading rationale"
}
Include every axis exactly once in sub_grades and use axis names that match: %s.
Do not include any text outside JSON.`, scale, strings.Join(axes, ", "), strings.Join(axes, ", "))

	return prompt
}

func (g *Grader) defaultAxes() []string {
	requiredAxes := []string{"centering", "corners", "edges", "surface"}
	seen := map[string]struct{}{}
	axes := make([]string, 0, len(requiredAxes)+len(g.cfg.Grading.Axes))

	for _, axis := range g.cfg.Grading.Axes {
		n := normalizeAxis(axis)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		axes = append(axes, axis)
	}

	for _, axis := range requiredAxes {
		n := normalizeAxis(axis)
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		axes = append(axes, axis)
	}

	if len(axes) == 0 {
		return requiredAxes
	}
	return axes
}

func normalizeOverallAndNotes(grade *ConditionGrade, disclaimer string) {
	if grade.Overall == 0 {
		grade.Overall = averageSubGradeScore(grade.SubGrades)
	}
	if strings.TrimSpace(disclaimer) == "" {
		return
	}
	disclaimer = strings.TrimSpace(disclaimer)
	if grade.Notes == "" {
		grade.Notes = disclaimer
		return
	}
	if !strings.Contains(grade.Notes, disclaimer) {
		grade.Notes = grade.Notes + "\n" + disclaimer
	}
}

func averageSubGradeScore(subGrades []SubGrade) float64 {
	if len(subGrades) == 0 {
		return 0
	}
	total := 0.0
	for _, sg := range subGrades {
		total += sg.Score
	}
	return total / float64(len(subGrades))
}

func validateSubGrades(subGrades []SubGrade, configuredAxes []string) error {
	required := make(map[string]struct{}, len(configuredAxes))
	for _, axis := range configuredAxes {
		required[normalizeAxis(axis)] = struct{}{}
	}

	for _, sg := range subGrades {
		delete(required, normalizeAxis(sg.Axis))
	}

	if len(required) > 0 {
		missing := make([]string, 0, len(required))
		for axis := range required {
			missing = append(missing, axis)
		}
		sort.Strings(missing)
		return fmt.Errorf("missing required grading axes: %v", missing)
	}

	return nil
}
