// Package matrix provides Matrix.org chat integration.

package matrix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const defaultOpenClawConfigRelPath = ".openclaw/openclaw.json"

// HTTPSender sends Matrix messages directly through the Matrix client API.
type HTTPSender struct {
	client     *http.Client
	account    string
	configPath string
}

// NewHTTPSender constructs a direct Matrix sender.
func NewHTTPSender(client *http.Client, account string) *HTTPSender {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &HTTPSender{
		client:  client,
		account: strings.TrimSpace(account),
	}
}

// SendMessage sends a message directly to a Matrix room.
func (s *HTTPSender) SendMessage(ctx context.Context, roomID, message string) error {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return fmt.Errorf("room id is required")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("message is required")
	}

	creds, err := s.loadCredentials()
	if err != nil {
		return err
	}

	txnID := fmt.Sprintf("chum-%d", time.Now().UTC().UnixNano())
	endpoint := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/send/m.room.message/%s",
		creds.homeserver,
		neturl.PathEscape(roomID),
		neturl.PathEscape(txnID),
	)

	payload, err := json.Marshal(map[string]string{
		"msgtype": "m.text",
		"body":    message,
	})
	if err != nil {
		return fmt.Errorf("marshal matrix payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build matrix request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+creds.accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("matrix send request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("matrix send failed: status %d (%s)", resp.StatusCode, compactOutput(out))
	}

	return nil
}

type matrixCredentials struct {
	homeserver  string
	accessToken string
}

func (s *HTTPSender) loadCredentials() (matrixCredentials, error) {
	configPath, err := resolveOpenClawConfigPath(s.configPath)
	if err != nil {
		return matrixCredentials{}, err
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		return matrixCredentials{}, fmt.Errorf("read openclaw config %s: %w", configPath, err)
	}

	var cfg openClawConfigFile
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return matrixCredentials{}, fmt.Errorf("parse openclaw config %s: %w", configPath, err)
	}

	channel := cfg.Channels.Matrix
	if len(channel.Accounts) == 0 {
		return matrixCredentials{}, fmt.Errorf("openclaw matrix accounts are not configured in %s", configPath)
	}

	account, err := selectMatrixAccount(channel.Accounts, channel.UserID, s.account)
	if err != nil {
		return matrixCredentials{}, err
	}

	token := strings.TrimSpace(account.AccessToken)
	if token == "" {
		return matrixCredentials{}, fmt.Errorf("matrix account %q has no access token", strings.TrimSpace(account.UserID))
	}

	homeserver := firstNonEmpty(account.Homeserver, account.BaseURL, channel.Homeserver)
	homeserver = strings.TrimSpace(homeserver)
	if homeserver == "" {
		return matrixCredentials{}, fmt.Errorf("matrix homeserver is not configured in %s", configPath)
	}
	if !strings.Contains(homeserver, "://") {
		homeserver = "http://" + homeserver
	}
	parsed, err := neturl.Parse(homeserver)
	if err != nil || strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
		return matrixCredentials{}, fmt.Errorf("invalid matrix homeserver %q", homeserver)
	}

	return matrixCredentials{
		homeserver:  strings.TrimRight(homeserver, "/"),
		accessToken: token,
	}, nil
}

func resolveOpenClawConfigPath(explicitPath string) (string, error) {
	if path := strings.TrimSpace(explicitPath); path != "" {
		return path, nil
	}
	if path := strings.TrimSpace(os.Getenv("OPENCLAW_CONFIG")); path != "" {
		return path, nil
	}
	if openclawHome := strings.TrimSpace(os.Getenv("OPENCLAW_HOME")); openclawHome != "" {
		return filepath.Join(openclawHome, "openclaw.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine home directory for openclaw config: %w", err)
	}
	return filepath.Join(home, defaultOpenClawConfigRelPath), nil
}

type openClawConfigFile struct {
	Channels struct {
		Matrix openClawMatrixChannel `json:"matrix"`
	} `json:"channels"`
}

type openClawMatrixChannel struct {
	Homeserver string                 `json:"homeserver"`
	UserID     string                 `json:"userId"`
	Accounts   openClawMatrixAccounts `json:"accounts"`
}

type openClawMatrixEntry struct {
	ID          string `json:"-"`
	UserID      string `json:"userId"`
	AccessToken string `json:"accessToken"`
	Homeserver  string `json:"homeserver"`
	BaseURL     string `json:"baseUrl"`
}

type openClawMatrixAccounts []openClawMatrixEntry

func (a *openClawMatrixAccounts) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*a = nil
		return nil
	}

	switch data[0] {
	case '[':
		var entries []openClawMatrixEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			return err
		}
		*a = entries
		return nil
	case '{':
		var byID map[string]openClawMatrixEntry
		if err := json.Unmarshal(data, &byID); err != nil {
			return err
		}
		keys := make([]string, 0, len(byID))
		for key := range byID {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		entries := make([]openClawMatrixEntry, 0, len(keys))
		for _, key := range keys {
			entry := byID[key]
			entry.ID = strings.TrimSpace(key)
			if strings.TrimSpace(entry.UserID) == "" && strings.HasPrefix(entry.ID, "@") {
				entry.UserID = entry.ID
			}
			entries = append(entries, entry)
		}
		*a = entries
		return nil
	default:
		return fmt.Errorf("unsupported matrix accounts format")
	}
}

func selectMatrixAccount(accounts []openClawMatrixEntry, defaultUserID string, requested string) (openClawMatrixEntry, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		for _, candidate := range accounts {
			if matrixAccountMatches(candidate, requested) {
				return candidate, nil
			}
		}
		return openClawMatrixEntry{}, fmt.Errorf("matrix account %q not found (available: %s)", requested, strings.Join(availableMatrixAccounts(accounts), ", "))
	}

	defaultUserID = strings.TrimSpace(defaultUserID)
	if defaultUserID != "" {
		for _, candidate := range accounts {
			if matrixAccountMatches(candidate, defaultUserID) {
				return candidate, nil
			}
		}
	}

	for _, candidate := range accounts {
		if strings.TrimSpace(candidate.UserID) != "" || strings.TrimSpace(candidate.ID) != "" {
			return candidate, nil
		}
	}

	return openClawMatrixEntry{}, fmt.Errorf("no matrix accounts configured")
}

func matrixAccountMatches(account openClawMatrixEntry, selector string) bool {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return false
	}

	userID := strings.TrimSpace(account.UserID)
	accountID := strings.TrimSpace(account.ID)

	if userID != "" && strings.EqualFold(userID, selector) {
		return true
	}
	if accountID != "" && strings.EqualFold(accountID, selector) {
		return true
	}
	if strings.HasPrefix(selector, "@") && strings.Contains(selector, ":") {
		return false
	}
	selector = strings.TrimPrefix(selector, "@")
	if selector == "" {
		return false
	}
	if userID != "" && strings.EqualFold(matrixUserLocalpart(userID), selector) {
		return true
	}
	return accountID != "" && strings.EqualFold(strings.TrimPrefix(accountID, "@"), selector)
}

func matrixUserLocalpart(userID string) string {
	userID = strings.TrimSpace(strings.TrimPrefix(userID, "@"))
	if idx := strings.Index(userID, ":"); idx >= 0 {
		return userID[:idx]
	}
	return userID
}

func availableMatrixAccounts(accounts []openClawMatrixEntry) []string {
	out := make([]string, 0, len(accounts))
	seen := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		name := strings.TrimSpace(matrixUserLocalpart(account.UserID))
		if name == "" {
			name = strings.TrimSpace(strings.TrimPrefix(account.ID, "@"))
		}
		if name == "" {
			continue
		}
		lower := strings.ToLower(name)
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		out = append(out, name)
	}
	if len(out) == 0 {
		return []string{"none"}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
