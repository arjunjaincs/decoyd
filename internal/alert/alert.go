// Package alert implements the pluggable notification system for Decoyd.
// Each channel is a separate type satisfying the Alerter interface.
// Config is stored as JSON at $dataDir/alert_config.json with 0600 permissions.
package alert

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// ----------------------------------------------------------------------------
// Core types (spec-mandated)
// ----------------------------------------------------------------------------

// AlertPayload carries the details of a triggered decoy token.
// Every Alerter maps it to its own wire format.
type AlertPayload struct {
	TokenID     string    `json:"token_id"`
	TokenType   string    `json:"token_type"`
	Path        string    `json:"path"`
	TriggeredAt time.Time `json:"triggered_at"`
	Detail      string    `json:"detail"`
}

// Alerter is the pluggable notification interface.
//
// CONTRACT: every implementation must sanitize HTTP errors before returning
// them. Go's net/http wraps failures as *url.Error whose .Error() string
// contains the raw request URL — which for Telegram includes the bot token
// and for all channels may include the full webhook URL. Call sanitizeErr on
// every HTTP error before returning it from Send.
type Alerter interface {
	Send(ctx context.Context, payload AlertPayload) error
}

// ----------------------------------------------------------------------------
// Channel-type constants
// ----------------------------------------------------------------------------

const (
	ChannelDiscord  = "discord"
	ChannelSlack    = "slack"
	ChannelTelegram = "telegram"
	ChannelTeams    = "teams"
	ChannelNtfy     = "ntfy"
	ChannelWebhook  = "webhook"
)

// ChannelEntry is used by the TUI to present an ordered list of channel types.
type ChannelEntry struct {
	Type  string
	Label string
}

// Channels is the ordered list of supported alert channels.
// Local desktop notification is deferred to Phase 5 (requires cgo on Linux).
var Channels = []ChannelEntry{
	{ChannelDiscord, "Discord webhook"},
	{ChannelSlack, "Slack webhook"},
	{ChannelTelegram, "Telegram bot"},
	{ChannelTeams, "Microsoft Teams"},
	{ChannelNtfy, "ntfy.sh"},
	{ChannelWebhook, "Generic webhook"},
}

// ----------------------------------------------------------------------------
// Sentinel errors
// ----------------------------------------------------------------------------

// ErrMisconfigured is returned by NewAlerter when required credentials are absent.
var ErrMisconfigured = errors.New("alert channel is not configured")

// ----------------------------------------------------------------------------
// Config storage
// ----------------------------------------------------------------------------

// ChannelConfig holds the credentials for one configured alert channel.
//
// Security note: WebhookURL and BotToken are secrets. They are stored verbatim
// in the JSON file (protected by 0600 permissions on Linux) but must NEVER be
// included in error messages, log output, or any string displayed in the TUI.
// Use MaskSecret for any display string involving these fields.
type ChannelConfig struct {
	// ID is a stable 8-hex-byte identifier for this channel entry, generated
	// once by GenerateChannelID and persisted. Used as Token.AlertChannelID.
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
	// Label is a user-assigned name shown in the UI; not sensitive.
	Label string `json:"label,omitempty"`
	// WebhookURL is used by Discord, Slack, Teams, and the generic webhook channel.
	WebhookURL string `json:"webhook_url,omitempty"`
	// BotToken and ChatID are used by the Telegram channel.
	BotToken string `json:"bot_token,omitempty"`
	ChatID   string `json:"chat_id,omitempty"`
	// ServerURL and Topic are used by the ntfy channel.
	// ServerURL defaults to "https://ntfy.sh" when empty.
	ServerURL string `json:"server_url,omitempty"`
	Topic     string `json:"topic,omitempty"`
}

// AlertConfig is the root config shape written to alert_config.json.
// DefaultID is the ID of the channel used for tokens with no explicit
// assignment; it matches the ID field of one of the entries in Channels.
// DefaultIndex is retained for JSON round-trip compatibility with legacy
// files that predate the ID field; it is ignored in all new logic.
type AlertConfig struct {
	Channels     []ChannelConfig `json:"channels"`
	DefaultIndex int             `json:"default_index"` // legacy compat only
	DefaultID    string          `json:"default_id,omitempty"`
}

// ----------------------------------------------------------------------------
// Channel ID generation
// ----------------------------------------------------------------------------

// GenerateChannelID returns a cryptographically random 8-byte (16 hex char)
// channel identifier. Called once when a channel entry is first saved.
func GenerateChannelID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate channel ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ----------------------------------------------------------------------------
// AlertConfig resolution helpers
// ----------------------------------------------------------------------------

// DefaultChannel returns the ChannelConfig whose ID matches cfg.DefaultID.
// Falls back to the first channel if DefaultID is unset or stale.
// Returns the zero value and false when Channels is empty.
func (cfg AlertConfig) DefaultChannel() (ChannelConfig, bool) {
	if len(cfg.Channels) == 0 {
		return ChannelConfig{}, false
	}
	for _, ch := range cfg.Channels {
		if ch.ID == cfg.DefaultID && cfg.DefaultID != "" {
			return ch, true
		}
	}
	// DefaultID unset or stale — fall back to first entry.
	return cfg.Channels[0], true
}

// ResolveChannel returns the ChannelConfig with the given ID.
// Returns the zero value and false if no channel has that ID.
func (cfg AlertConfig) ResolveChannel(id string) (ChannelConfig, bool) {
	if id == "" {
		return ChannelConfig{}, false
	}
	for _, ch := range cfg.Channels {
		if ch.ID == id {
			return ch, true
		}
	}
	return ChannelConfig{}, false
}

// ChannelForToken returns the alert channel to use for a token with the given
// AlertChannelID. Priority:
//  1. If tokenChannelID is non-empty and a channel with that ID exists → use it.
//  2. If the assigned channel was deleted (stale ID) → fall back to default silently.
//  3. If tokenChannelID is empty → use the default channel.
//  4. If there are no channels at all → return zero value and false.
func (cfg AlertConfig) ChannelForToken(tokenChannelID string) (ChannelConfig, bool) {
	if tokenChannelID != "" {
		if ch, ok := cfg.ResolveChannel(tokenChannelID); ok {
			return ch, true
		}
		// Assigned channel was deleted — fall through to default.
	}
	return cfg.DefaultChannel()
}

const configFileName = "alert_config.json"

// Load reads and decodes alert_config.json from dataDir.
// Returns an empty AlertConfig (not an error) when the file does not exist yet.
// If any channel entry is missing an ID (legacy config), Load backfills IDs
// and persists the updated file before returning.
func Load(dataDir string) (AlertConfig, error) {
	path := filepath.Join(dataDir, configFileName)
	data, err := os.ReadFile(path) // #nosec G304 -- path is always filepath.Join(dataDir, configFileName), never user input
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return AlertConfig{}, nil
		}
		return AlertConfig{}, fmt.Errorf("load alert config: %w", err)
	}
	var cfg AlertConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return AlertConfig{}, fmt.Errorf("parse alert config: %w", err)
	}
	// Backfill IDs for any channel entry that predates the ID field.
	// If any were missing, persist the healed config immediately.
	needsSave := false
	for i := range cfg.Channels {
		if cfg.Channels[i].ID == "" {
			id, err := GenerateChannelID()
			if err != nil {
				return AlertConfig{}, fmt.Errorf("backfill channel ID: %w", err)
			}
			cfg.Channels[i].ID = id
			needsSave = true
		}
	}
	// Also backfill DefaultID if it's missing but channels exist.
	if cfg.DefaultID == "" && len(cfg.Channels) > 0 {
		cfg.DefaultID = cfg.Channels[0].ID
		needsSave = true
	}
	if needsSave {
		// Best-effort: don't return an error if the backfill write fails
		// (e.g. read-only filesystem), just return the in-memory config.
		_ = Save(dataDir, cfg)
	}
	return cfg, nil
}

// Save marshals cfg to JSON and writes it atomically to dataDir/alert_config.json.
// The file is created with permission 0600 so only the owning user can read
// the webhook URLs and bot tokens stored inside.
// Save assigns IDs to any channel entry that is missing one before writing.
func Save(dataDir string, cfg AlertConfig) error {
	// Assign IDs to any channel that doesn't have one yet.
	for i := range cfg.Channels {
		if cfg.Channels[i].ID == "" {
			id, err := GenerateChannelID()
			if err != nil {
				return fmt.Errorf("assign channel ID: %w", err)
			}
			cfg.Channels[i].ID = id
		}
	}
	// Ensure DefaultID is set when channels exist.
	if cfg.DefaultID == "" && len(cfg.Channels) > 0 {
		cfg.DefaultID = cfg.Channels[0].ID
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal alert config: %w", err)
	}
	path := filepath.Join(dataDir, configFileName)
	tmp := path + ".tmp"

	if err := os.WriteFile(tmp, data, 0o600); err != nil { // #nosec G304 -- tmp is always filepath.Join(dataDir, configFileName)+".tmp", never user input
		return fmt.Errorf("write alert config: %w", err)
	}
	if runtime.GOOS != "windows" {
		// Belt-and-suspenders: WriteFile honours umask so we chmod explicitly.
		_ = os.Chmod(tmp, 0o600)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install alert config: %w", err)
	}
	return nil
}

// ----------------------------------------------------------------------------
// Factory
// ----------------------------------------------------------------------------

// NewAlerter constructs the Alerter for the given ChannelConfig.
// Returns ErrMisconfigured when required credentials are absent.
func NewAlerter(cfg ChannelConfig) (Alerter, error) {
	switch cfg.Type {
	case ChannelDiscord:
		if cfg.WebhookURL == "" {
			return nil, ErrMisconfigured
		}
		return &DiscordAlerter{webhookURL: cfg.WebhookURL}, nil

	case ChannelSlack:
		if cfg.WebhookURL == "" {
			return nil, ErrMisconfigured
		}
		return &SlackAlerter{webhookURL: cfg.WebhookURL}, nil

	case ChannelTelegram:
		if cfg.BotToken == "" || cfg.ChatID == "" {
			return nil, ErrMisconfigured
		}
		return newTelegramAlerter(cfg.BotToken, cfg.ChatID, ""), nil

	case ChannelTeams:
		if cfg.WebhookURL == "" {
			return nil, ErrMisconfigured
		}
		return &TeamsAlerter{webhookURL: cfg.WebhookURL}, nil

	case ChannelNtfy:
		if cfg.Topic == "" {
			return nil, ErrMisconfigured
		}
		srv := cfg.ServerURL
		if srv == "" {
			srv = "https://ntfy.sh"
		}
		return &NtfyAlerter{serverURL: srv, topic: cfg.Topic}, nil

	case ChannelWebhook:
		if cfg.WebhookURL == "" {
			return nil, ErrMisconfigured
		}
		return &WebhookAlerter{webhookURL: cfg.WebhookURL}, nil

	default:
		return nil, fmt.Errorf("unknown channel type %q", cfg.Type)
	}
}

// ----------------------------------------------------------------------------
// MaskSecret — safe display string for credentials
// ----------------------------------------------------------------------------

// MaskSecret returns a string safe to display in the TUI: all characters
// except the last 4 are replaced with '•'. If s has 4 or fewer characters,
// the entire value is masked. Empty string returns empty string.
func MaskSecret(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= 4 {
		return dots(len(runes))
	}
	return dots(len(runes)-4) + string(runes[len(runes)-4:])
}

func dots(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = '•'
	}
	return string(b)
}

// ----------------------------------------------------------------------------
// Shared HTTP helpers (package-internal)
// ----------------------------------------------------------------------------

// alertTimeout is the per-request deadline applied by every Alerter.
const alertTimeout = 10 * time.Second

// doPost marshals body to JSON, POSTs it to rawURL with Content-Type
// application/json, and returns a sanitized error on failure.
func doPost(ctx context.Context, rawURL string, body any) error {
	ctx, cancel := context.WithTimeout(ctx, alertTimeout)
	defer cancel()

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(data))
	if err != nil {
		return sanitizeErr(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return sanitizeErr(err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
	}
	return nil
}

// doPostText POSTs a plain-text body to rawURL with custom headers.
func doPostText(ctx context.Context, rawURL, body string, headers map[string]string) error {
	ctx, cancel := context.WithTimeout(ctx, alertTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewBufferString(body))
	if err != nil {
		return sanitizeErr(err)
	}
	req.Header.Set("Content-Type", "text/plain")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return sanitizeErr(err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
	}
	return nil
}

// sanitizeErr rewraps *url.Error so the raw request URL — which may contain
// webhook paths or bot tokens — is never exposed in the error string.
// Every Alerter Send method must call this on every HTTP error before returning.
func sanitizeErr(err error) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		// Discard the URL. Surface only the HTTP operation and the root cause.
		return fmt.Errorf("http %s: %w", urlErr.Op, urlErr.Err)
	}
	return err
}
