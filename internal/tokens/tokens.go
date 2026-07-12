// Package tokens defines the canary token data model, type constants,
// category groupings for the TUI, and the central dispatch function.
package tokens

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// ----------------------------------------------------------------------------
// Type constants — every token type has a stable string key.
// ----------------------------------------------------------------------------

const (
	TypeAWSCredentials = "aws_credentials"
	TypeSSHKey         = "ssh_key"
	TypeEnvSecrets     = "env_secrets"
	TypeGitHubPAT      = "github_pat"
	TypeSlackToken     = "slack_token"
	TypeKubeconfig     = "kubeconfig"
	TypeDBDump         = "db_dump"
	TypeDNSCanary      = "dns_canary"
)

// ----------------------------------------------------------------------------
// Token — canonical data model (matches spec exactly)
// ----------------------------------------------------------------------------

// Token is the canonical canary token record. Every generated token has one.
type Token struct {
	ID             string     // random 16-hex-char identifier
	Type           string     // one of the Type* constants
	Value          string     // the generated secret / file content
	Filename       string     // suggested on-disk filename
	CreatedAt      time.Time  // UTC creation time
	DeployedPath   string     // absolute path once deployed; empty if not yet deployed
	AlertChannelID string     // ID of the alert channel to use on trigger
	Triggered      bool       // true once a trigger event has been recorded
	TriggeredAt    *time.Time // time of first trigger; nil until triggered
	Notes          string     // optional user-supplied label / context
}

// ----------------------------------------------------------------------------
// ID generation
// ----------------------------------------------------------------------------

// NewID generates a cryptographically random 8-byte (16 hex char) token ID.
func NewID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ----------------------------------------------------------------------------
// Category / type registry — used by the Generate screen
// ----------------------------------------------------------------------------

// TypeDef describes a single token type for TUI display.
type TypeDef struct {
	Key         string // matches a Type* constant
	Label       string // human-readable name shown in the checklist
	Description string // brief one-line description shown as a subtitle
}

// CategoryDef groups related token types for display in the Generate screen.
type CategoryDef struct {
	Name  string
	Types []TypeDef
}

// Categories is the authoritative grouping used in the Generate screen.
// Order here controls display order.
var Categories = []CategoryDef{
	{
		Name: "Cloud / Infra",
		Types: []TypeDef{
			{TypeAWSCredentials, "AWS credentials", "AKIA... access key + secret in credentials format"},
			{TypeSSHKey, "SSH private key", "real ed25519 keypair, validly formatted, never registered"},
			{TypeKubeconfig, "Kubeconfig", "structurally valid kubeconfig YAML with fake cluster + bearer token"},
			{TypeDNSCanary, "DNS canary token", "unique 16-char subdomain label for a domain you control"},
		},
	},
	{
		Name: "Dev Tools",
		Types: []TypeDef{
			{TypeGitHubPAT, "GitHub PAT", "ghp_... format personal access token (36 alphanumeric chars)"},
			{TypeSlackToken, "Slack bot token", "xoxb-... format bot token matching Slack's real segment structure"},
		},
	},
	{
		Name: "Data",
		Types: []TypeDef{
			{TypeEnvSecrets, ".env secrets", "DATABASE_URL, STRIPE_SECRET_KEY, JWT_SECRET with realistic values"},
			{TypeDBDump, "Database dump", "backup.sql with real-looking schema, connection header, fake rows"},
		},
	},
}

// ----------------------------------------------------------------------------
// Generate — central dispatch
// ----------------------------------------------------------------------------

// Generate creates a token of the given type. It returns an error if the
// type key is unknown or if the generator itself fails (e.g. crypto/rand).
func Generate(tokenType string) (Token, error) {
	switch tokenType {
	case TypeAWSCredentials:
		return GenerateAWSCredentials()
	case TypeSSHKey:
		return GenerateSSHKey()
	case TypeEnvSecrets:
		return GenerateEnvSecrets()
	case TypeGitHubPAT:
		return GenerateGitHubPAT()
	case TypeSlackToken:
		return GenerateSlackToken()
	case TypeKubeconfig:
		return GenerateKubeconfig()
	case TypeDBDump:
		return GenerateDBDump()
	case TypeDNSCanary:
		return GenerateDNSCanary()
	default:
		return Token{}, fmt.Errorf("unknown token type: %q", tokenType)
	}
}
