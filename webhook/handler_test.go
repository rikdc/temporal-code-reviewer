package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rikdc/temporal-code-reviewer/config"
	"go.uber.org/zap/zaptest"
)

func TestVerifySignature(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"action":"opened","number":1}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !verifySignature(body, sig, secret) {
		t.Error("valid signature should verify")
	}
	if verifySignature(body, "sha256=0000000000000000000000000000000000000000000000000000000000000000", secret) {
		t.Error("wrong signature should not verify")
	}
	if verifySignature(body, "invalid-sig", secret) {
		t.Error("malformed signature should not verify")
	}
	if verifySignature(body, "", secret) {
		t.Error("empty signature should not verify")
	}
	if verifySignature(body, "sha256="+hex.EncodeToString([]byte("wrong")), secret) {
		t.Error("wrong-length signature should not verify")
	}
}

func TestVerifySignature_ShortBody(t *testing.T) {
	secret := "test"
	body := []byte("x")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !verifySignature(body, sig, secret) {
		t.Error("short body with valid sig should verify")
	}
}

func TestWebhookHandler_SignatureRequired(t *testing.T) {
	cfg := &config.Config{
		Webhook: config.WebhookConfig{
			Enabled:       true,
			Secret:        "test-secret",
			MaxBodyBytes:  2 * 1024 * 1024,
			AllowedActions: []string{"opened"},
		},
	}
	h := NewHandler(nil, zaptest.NewLogger(t), cfg)

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/pr", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	// No signature header
	w := httptest.NewRecorder()
	h.HandlePR(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("missing signature should return 401, got %d", w.Code)
	}
}

func TestWebhookHandler_InvalidSignature(t *testing.T) {
	cfg := &config.Config{
		Webhook: config.WebhookConfig{
			Enabled:       true,
			Secret:        "test-secret",
			MaxBodyBytes:  2 * 1024 * 1024,
			AllowedActions: []string{"opened"},
		},
	}
	h := NewHandler(nil, zaptest.NewLogger(t), cfg)

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/pr", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", "sha256=0000000000000000000000000000000000000000000000000000000000000000")
	w := httptest.NewRecorder()
	h.HandlePR(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("invalid signature should return 403, got %d", w.Code)
	}
}

func TestWebhookHandler_OversizedBody(t *testing.T) {
	cfg := &config.Config{
		Webhook: config.WebhookConfig{
			Enabled:       true,
			Secret:        "",
			MaxBodyBytes:  100,
			AllowedActions: []string{"opened"},
		},
	}
	h := NewHandler(nil, zaptest.NewLogger(t), cfg)

	// Create body larger than max
	body := bytes.Repeat([]byte("x"), 200)
	req := httptest.NewRequest(http.MethodPost, "/webhook/pr", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	w := httptest.NewRecorder()
	h.HandlePR(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body should return 413, got %d", w.Code)
	}
}

func TestWebhookHandler_DuplicateDelivery(t *testing.T) {
	cfg := &config.Config{
		Webhook: config.WebhookConfig{
			Enabled:       false,
			MaxBodyBytes:  2 * 1024 * 1024,
			AllowedActions: []string{"opened"},
		},
	}
	h := NewHandler(nil, zaptest.NewLogger(t), cfg)

	payload := `{"action":"opened","number":1}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/pr", bytes.NewReader([]byte(payload)))
	req.Header.Set("X-GitHub-Delivery", "delivery-123")
	w := httptest.NewRecorder()
	h.HandlePR(w, req)

	// Send again with same delivery ID
	req2 := httptest.NewRequest(http.MethodPost, "/webhook/pr", bytes.NewReader([]byte(payload)))
	req2.Header.Set("X-GitHub-Delivery", "delivery-123")
	w2 := httptest.NewRecorder()
	h.HandlePR(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("duplicate delivery should return 200, got %d", w2.Code)
	}
}

func TestWebhookHandler_RepoAllowlist(t *testing.T) {
	cfg := &config.Config{
		Webhook: config.WebhookConfig{
			Enabled:       false,
			MaxBodyBytes:  2 * 1024 * 1024,
			AllowedRepos:  []string{"myorg/myrepo"},
			AllowedActions: []string{"opened"},
		},
	}
	h := NewHandler(nil, zaptest.NewLogger(t), cfg)

	payload := map[string]interface{}{
		"action": "opened",
		"repository": map[string]interface{}{
			"name": "other-repo",
			"owner": map[string]interface{}{
				"login": "myorg",
			},
		},
		"pull_request": map[string]interface{}{
			"number": 1,
			"title":  "Test PR",
			"head":   map[string]interface{}{"ref": "main", "sha": "abc123"},
			"base":   map[string]interface{}{"ref": "main"},
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/webhook/pr", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	w := httptest.NewRecorder()
	h.HandlePR(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("repo not in allowlist should return 403, got %d", w.Code)
	}
}

func TestWebhookHandler_AllowedActions(t *testing.T) {
	cfg := &config.Config{
		Webhook: config.WebhookConfig{
			Enabled:       false,
			MaxBodyBytes:  2 * 1024 * 1024,
			AllowedActions: []string{"opened"},
		},
	}
	h := NewHandler(nil, zaptest.NewLogger(t), cfg)

	payload := `{"action":"closed","number":1}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/pr", bytes.NewReader([]byte(payload)))
	req.Header.Set("X-GitHub-Event", "pull_request")
	w := httptest.NewRecorder()
	h.HandlePR(w, req)

	// "closed" not in allowed actions -> 200 OK (ignored)
	if w.Code != http.StatusOK {
		t.Errorf("disallowed action should return 200, got %d", w.Code)
	}
}

func TestWebhookHandler_NonPREvent(t *testing.T) {
	cfg := &config.Config{
		Webhook: config.WebhookConfig{
			Enabled: false,
			MaxBodyBytes: 2 * 1024 * 1024,
		},
	}
	h := NewHandler(nil, zaptest.NewLogger(t), cfg)

	req := httptest.NewRequest(http.MethodPost, "/webhook/pr", bytes.NewReader([]byte("{}")))
	req.Header.Set("X-GitHub-Event", "push")
	w := httptest.NewRecorder()
	h.HandlePR(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("non-PR event should return 200, got %d", w.Code)
	}
}

func TestWebhookHandler_MethodNotAllowed(t *testing.T) {
	cfg := &config.Config{}
	h := NewHandler(nil, zaptest.NewLogger(t), cfg)

	req := httptest.NewRequest(http.MethodGet, "/webhook/pr", nil)
	w := httptest.NewRecorder()
	h.HandlePR(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET should return 405, got %d", w.Code)
	}
}
