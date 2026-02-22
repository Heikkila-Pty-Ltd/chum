package pokemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	anthropicEndpoint      = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion    = "2023-06-01"
	anthropicMaxTokens     = 4096
	anthropicTemperature   = 0.0
	maxAnthropicResponseKB = 512
)

type Config struct {
	Vision  VisionConfig  `toml:"vision" json:"vision"`
	Grading GradingConfig `toml:"grading" json:"grading"`
}

type VisionConfig struct {
	APIKey string `toml:"api_key" json:"api_key"`
	Model  string `toml:"model" json:"model"`
}

type GradingConfig struct {
	Scale      string   `toml:"scale" json:"scale"`
	Axes       []string `toml:"axes" json:"axes"`
	Disclaimer string   `toml:"disclaimer" json:"disclaimer"`
}

type CardIdentification struct {
	CardName   string `json:"card_name"`
	SetName    string `json:"set_name"`
	SetNumber  string `json:"set_number"`
	Year       string `json:"year"`
	Rarity     string `json:"rarity"`
	Variant    string `json:"variant"`
}

type SubGrade struct {
	Axis  string  `json:"axis"`
	Score float64 `json:"score"`
}

type ConditionGrade struct {
	Overall   float64    `json:"overall"`
	SubGrades []SubGrade `json:"sub_grades"`
	Notes     string     `json:"notes"`
}

type Identifier struct {
	cfg    *Config
	logger *slog.Logger
	client *http.Client
}

func NewIdentifier(cfg *Config, logger *slog.Logger) *Identifier {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg == nil {
		cfg = &Config{}
	}

	return &Identifier{
		cfg:    cfg,
		logger: logger,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (i *Identifier) IdentifyCard(imagePath string) (*CardIdentification, error) {
	if i == nil {
		return nil, errors.New("identifier is nil")
	}
	if i.cfg == nil {
		return nil, errors.New("identifier config is nil")
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("ANTHROPIC_API_KEY is empty")
	}

	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return nil, err
	}

	mediaType, err := DetectMediaType(imageData)
	if err != nil {
		return nil, err
	}

	payload := base64.StdEncoding.EncodeToString(imageData)
	response, err := i.callAnthropicVisionAPI(context.Background(), apiKey, i.buildIdentifyPrompt(), mediaType, payload)
	if err != nil {
		return nil, err
	}

	var id CardIdentification
	if err := parseAnthropicJSONResponse(response.Content, &id); err != nil {
		return nil, err
	}

	return &id, nil
}

func (i *Identifier) buildIdentifyPrompt() string {
	return `You are a strict Pokémon card recognition model.
Return ONLY JSON for the following structure:
{
  "card_name": "full card name",
  "set_name": "official card set name",
  "set_number": "set collection number, including suffixes when present",
  "year": "release year if known",
  "rarity": "rarity",
  "variant": "variant if present"
}
Use null only if a value cannot be determined.
Do not include any text outside JSON.`
}

func (i *Identifier) callAnthropicVisionAPI(ctx context.Context, apiKey, prompt, mediaType, imageData string) (*anthropicResponse, error) {
	return callAnthropicVisionAPI(ctx, i.client, i.cfg.Vision.Model, apiKey, prompt, mediaType, imageData)
}

func callAnthropicVisionAPI(
	ctx context.Context,
	client *http.Client,
	model string,
	apiKey string,
	prompt string,
	mediaType string,
	imageData string,
) (*anthropicResponse, error) {
	payload := anthropicRequest{
		Model:       model,
		MaxTokens:   anthropicMaxTokens,
		Temperature: anthropicTemperature,
		Messages: []anthropicMessage{
			{
				Role: "user",
				Content: []anthropicContent{
					{
						Type: "image",
						Source: &anthropicSource{
							Type:      "base64",
							MediaType: mediaType,
							Data:      imageData,
						},
					},
					{
						Type: "text",
						Text: prompt,
					},
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)
	req.Header.Set("Content-Type", "application/json")

	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnthropicResponseKB*1024+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxAnthropicResponseKB*1024 {
		return nil, fmt.Errorf("anthropic response too large")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("anthropic request failed: %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed anthropicResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("anthropic error: %s", parsed.Error.Message)
	}

	return &parsed, nil
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature"`
	Messages    []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicContent  `json:"content"`
}

type anthropicContent struct {
	Type   string          `json:"type"`
	Text   string          `json:"text,omitempty"`
	Source *anthropicSource `json:"source,omitempty"`
}

type anthropicSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicResponse struct {
	Content []anthropicContent `json:"content"`
	Error   *anthropicErr      `json:"error,omitempty"`
}

type anthropicErr struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func DetectMediaType(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("empty image")
	}

	mediaType := http.DetectContentType(data)
	switch mediaType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return mediaType, nil
	}

	if len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")) {
		return "image/webp", nil
	}

	return "", fmt.Errorf("unsupported image type: %s", mediaType)
}

func parseAnthropicJSONResponse(content []anthropicContent, out interface{}) error {
	var parseErr error
	for _, block := range content {
		if block.Type != "text" || strings.TrimSpace(block.Text) == "" {
			continue
		}
		candidate := extractJSONObject(block.Text)
		if candidate == "" {
			continue
		}
		if err := json.Unmarshal([]byte(candidate), out); err == nil {
			return nil
		} else {
			parseErr = err
		}
	}
	if parseErr != nil {
		return parseErr
	}
	return errors.New("no valid JSON text content in anthropic response")
}

func extractJSONObject(text string) string {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimSpace(trimmed)
		if end := strings.LastIndex(trimmed, "```"); end > 3 {
			trimmed = strings.TrimSpace(trimmed[3:end])
			if firstNL := strings.Index(trimmed, "\n"); firstNL >= 0 {
				trimmed = strings.TrimSpace(trimmed[firstNL+1:])
			}
		}
	}

	start := -1
	depth := 0
	inString := false
	escape := false
	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]
		if start == -1 {
			if ch == '{' {
				start = i
				depth = 1
			}
			continue
		}

		if inString {
			if escape {
				escape = false
				continue
			}
			switch ch {
			case '\\':
				escape = true
			case '"':
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(trimmed[start : i+1])
			}
		}
	}

	return ""
}

func normalizeAxis(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
