package matrix

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPSenderSendMessageSuccess(t *testing.T) {
	var (
		gotAuth    string
		gotMethod  string
		gotPath    string
		gotEscPath string
		gotPayload map[string]any
	)

	client := &http.Client{
		Transport: fakeRoundTripper(func(req *http.Request) (*http.Response, error) {
			gotAuth = req.Header.Get("Authorization")
			gotMethod = req.Method
			gotPath = req.URL.Path
			gotEscPath = req.URL.EscapedPath()
			defer req.Body.Close()
			_ = json.NewDecoder(req.Body).Decode(&gotPayload)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"event_id":"$evt"}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	cfgPath := writeOpenClawMatrixConfig(t, "http://matrix.local", "@hex:example.org", []openClawMatrixEntry{
		{UserID: "@hex:example.org", AccessToken: "token-hex"},
		{UserID: "@spritzbot:example.org", AccessToken: "token-spritz"},
	})

	sender := NewHTTPSender(client, "spritzbot")
	sender.configPath = cfgPath

	if err := sender.SendMessage(t.Context(), "!room:matrix.org", "hello world"); err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}

	if gotAuth != "Bearer token-spritz" {
		t.Fatalf("authorization header = %q, want Bearer token-spritz", gotAuth)
	}
	if gotMethod != http.MethodPut {
		t.Fatalf("http method = %q, want %q", gotMethod, http.MethodPut)
	}
	if !strings.Contains(gotEscPath, "/_matrix/client/v3/rooms/%21room:matrix.org/send/m.room.message/") {
		t.Fatalf("escaped request path = %q (decoded=%q), want matrix room in send path", gotEscPath, gotPath)
	}
	if gotPayload["msgtype"] != "m.text" {
		t.Fatalf("msgtype = %v, want m.text", gotPayload["msgtype"])
	}
	if gotPayload["body"] != "hello world" {
		t.Fatalf("body = %v, want hello world", gotPayload["body"])
	}
}

func TestHTTPSenderSendMessageUsesDefaultConfiguredAccount(t *testing.T) {
	var gotAuth string
	client := &http.Client{
		Transport: fakeRoundTripper(func(req *http.Request) (*http.Response, error) {
			gotAuth = req.Header.Get("Authorization")
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"event_id":"$evt"}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	cfgPath := writeOpenClawMatrixConfig(t, "http://matrix.local", "@spritzbot:example.org", []openClawMatrixEntry{
		{UserID: "@hex:example.org", AccessToken: "token-hex"},
		{UserID: "@spritzbot:example.org", AccessToken: "token-spritz"},
	})

	sender := NewHTTPSender(client, "")
	sender.configPath = cfgPath

	if err := sender.SendMessage(t.Context(), "!room:matrix.org", "hello"); err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if gotAuth != "Bearer token-spritz" {
		t.Fatalf("authorization header = %q, want Bearer token-spritz", gotAuth)
	}
}

func TestHTTPSenderSendMessageSupportsObjectAccountsFormat(t *testing.T) {
	var gotAuth string
	client := &http.Client{
		Transport: fakeRoundTripper(func(req *http.Request) (*http.Response, error) {
			gotAuth = req.Header.Get("Authorization")
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"event_id":"$evt"}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	raw := `{
  "channels": {
    "matrix": {
      "homeserver": "http://matrix.local",
      "userId": "@spritzbot:example.org",
      "accounts": {
        "spritzbot": {
          "userId": "@spritzbot:example.org",
          "accessToken": "token-spritz"
        }
      }
    }
  }
}`
	cfgPath := writeRawOpenClawConfig(t, raw)

	sender := NewHTTPSender(client, "spritzbot")
	sender.configPath = cfgPath

	if err := sender.SendMessage(t.Context(), "!room:matrix.org", "hello"); err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if gotAuth != "Bearer token-spritz" {
		t.Fatalf("authorization header = %q, want Bearer token-spritz", gotAuth)
	}
}

func TestHTTPSenderSendMessageValidatesInputs(t *testing.T) {
	sender := NewHTTPSender(http.DefaultClient, "spritzbot")

	if err := sender.SendMessage(t.Context(), "", "hello"); err == nil {
		t.Fatal("expected error for empty room")
	}
	if err := sender.SendMessage(t.Context(), "!room:matrix.org", ""); err == nil {
		t.Fatal("expected error for empty message")
	}
}

func TestHTTPSenderSendMessageErrorsWhenAccountMissing(t *testing.T) {
	cfgPath := writeOpenClawMatrixConfig(t, "http://matrix.local", "@hex:example.org", []openClawMatrixEntry{
		{UserID: "@hex:example.org", AccessToken: "token-hex"},
	})

	sender := NewHTTPSender(http.DefaultClient, "spritzbot")
	sender.configPath = cfgPath

	err := sender.SendMessage(t.Context(), "!room:matrix.org", "hello")
	if err == nil {
		t.Fatal("expected account resolution error")
	}
	if !strings.Contains(err.Error(), "matrix account") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHTTPSenderSendMessageHandlesHTTPFailure(t *testing.T) {
	client := &http.Client{
		Transport: fakeRoundTripper(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader("forbidden")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	cfgPath := writeOpenClawMatrixConfig(t, "http://matrix.local", "@spritzbot:example.org", []openClawMatrixEntry{
		{UserID: "@spritzbot:example.org", AccessToken: "token-spritz"},
	})

	sender := NewHTTPSender(client, "spritzbot")
	sender.configPath = cfgPath

	err := sender.SendMessage(t.Context(), "!room:matrix.org", "hello")
	if err == nil {
		t.Fatal("expected HTTP status error")
	}
	if !strings.Contains(err.Error(), "status 403") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func writeOpenClawMatrixConfig(t *testing.T, homeserver string, defaultUserID string, accounts []openClawMatrixEntry) string {
	t.Helper()

	cfg := openClawConfigFile{}
	cfg.Channels.Matrix.Homeserver = homeserver
	cfg.Channels.Matrix.UserID = defaultUserID
	cfg.Channels.Matrix.Accounts = accounts

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	path := filepath.Join(t.TempDir(), "openclaw.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func writeRawOpenClawConfig(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openclaw.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write raw config: %v", err)
	}
	return path
}

type fakeRoundTripper func(req *http.Request) (*http.Response, error)

func (f fakeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
