package tokens

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// ----------------------------------------------------------------------------
// Random helpers
// ----------------------------------------------------------------------------

const (
	alphaNum      = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	upperAlphaNum = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	lowerAlpha    = "abcdefghijklmnopqrstuvwxyz"
	digits        = "0123456789"
	bcryptChars   = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789./"
)

// randStr returns n random chars from charset using crypto/rand.
func randStr(n int, charset string) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	chars := []byte(charset)
	for i, v := range b {
		b[i] = chars[int(v)%len(chars)]
	}
	return string(b), nil
}

// randB64 returns n random bytes encoded as standard base64.
func randB64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// now returns the current UTC time.
func now() time.Time { return time.Now().UTC() }

// ----------------------------------------------------------------------------
// AWS Credentials
// ----------------------------------------------------------------------------

// GenerateAWSCredentials generates a realistic AWS credentials file section.
// Format: AKIA + 16 uppercase alphanumeric chars for the key ID;
//
//	40 char base62 string for the secret.
func GenerateAWSCredentials() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}

	keyBody, err := randStr(16, upperAlphaNum)
	if err != nil {
		return Token{}, fmt.Errorf("aws key: %w", err)
	}
	accessKeyID := "AKIA" + keyBody

	secret, err := randStr(40, alphaNum+"+/")
	if err != nil {
		return Token{}, fmt.Errorf("aws secret: %w", err)
	}

	value := fmt.Sprintf(`[default]
aws_access_key_id = %s
aws_secret_access_key = %s
region = us-east-1
output = json
`, accessKeyID, secret)

	return Token{
		ID:        id,
		Type:      TypeAWSCredentials,
		Value:     value,
		Filename:  "credentials",
		CreatedAt: now(),
	}, nil
}

// ----------------------------------------------------------------------------
// SSH Private Key
// ----------------------------------------------------------------------------

// GenerateSSHKey generates a real ed25519 keypair in OpenSSH PEM format.
// The private key is stored in Value; the public key authorized-key line is
// appended after a sentinel so deploy can write both files.
func GenerateSSHKey() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}

	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Token{}, fmt.Errorf("ssh keygen: %w", err)
	}

	// Marshal the private key to OpenSSH PEM format.
	privBlock, err := ssh.MarshalPrivateKey(privKey, "")
	if err != nil {
		return Token{}, fmt.Errorf("ssh marshal private: %w", err)
	}
	privPEM := string(pem.EncodeToMemory(privBlock))

	// Build the public key authorized_keys line.
	sshPub, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return Token{}, fmt.Errorf("ssh public key: %w", err)
	}
	pubLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))

	// Combine both in Value with a sentinel separator so Phase 2 deploy can
	// split them out into id_ed25519 and id_ed25519.pub.
	value := privPEM + "---PUBLIC KEY---\n" + pubLine + "\n"

	return Token{
		ID:        id,
		Type:      TypeSSHKey,
		Value:     value,
		Filename:  "id_ed25519",
		CreatedAt: now(),
	}, nil
}

// ----------------------------------------------------------------------------
// .env Secrets
// ----------------------------------------------------------------------------

// GenerateEnvSecrets generates a .env file with realistic fake secrets.
func GenerateEnvSecrets() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}

	dbPass, err := randStr(24, alphaNum)
	if err != nil {
		return Token{}, err
	}
	stripeKey, err := randStr(24, alphaNum)
	if err != nil {
		return Token{}, err
	}
	jwtSecret, err := randStr(64, alphaNum+"-_")
	if err != nil {
		return Token{}, err
	}
	redisPass, err := randStr(20, alphaNum)
	if err != nil {
		return Token{}, err
	}
	apiKey, err := randStr(32, alphaNum)
	if err != nil {
		return Token{}, err
	}

	value := fmt.Sprintf(`# Application environment — production
NODE_ENV=production

DATABASE_URL=postgresql://admin:%s@db.internal:5432/production?sslmode=require
REDIS_URL=redis://:@redis.internal:6379/0
REDIS_PASSWORD=%s

STRIPE_SECRET_KEY=sk_live_%s
STRIPE_WEBHOOK_SECRET=whsec_%s

JWT_SECRET=%s
SESSION_SECRET=%s

INTERNAL_API_KEY=%s
`,
		dbPass, redisPass,
		stripeKey, stripeKey,
		jwtSecret, jwtSecret[:32],
		apiKey,
	)

	return Token{
		ID:        id,
		Type:      TypeEnvSecrets,
		Value:     value,
		Filename:  ".env",
		CreatedAt: now(),
	}, nil
}

// ----------------------------------------------------------------------------
// GitHub PAT
// ----------------------------------------------------------------------------

// GenerateGitHubPAT generates a token matching GitHub's real ghp_ format.
// Format: ghp_ + 36 lowercase alphanumeric chars.
func GenerateGitHubPAT() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}

	body, err := randStr(36, alphaNum)
	if err != nil {
		return Token{}, fmt.Errorf("github pat: %w", err)
	}
	value := "ghp_" + body

	return Token{
		ID:        id,
		Type:      TypeGitHubPAT,
		Value:     value,
		Filename:  ".github_token",
		CreatedAt: now(),
	}, nil
}

// ----------------------------------------------------------------------------
// Slack Bot Token
// ----------------------------------------------------------------------------

// GenerateSlackToken generates a token matching Slack's real xoxb- format.
// Format: xoxb-<10 digits>-<11 digits>-<24 alphanumeric>
func GenerateSlackToken() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}

	seg1, err := randStr(10, digits)
	if err != nil {
		return Token{}, err
	}
	seg2, err := randStr(11, digits)
	if err != nil {
		return Token{}, err
	}
	seg3, err := randStr(24, alphaNum)
	if err != nil {
		return Token{}, err
	}

	value := fmt.Sprintf("xoxb-%s-%s-%s", seg1, seg2, seg3)

	return Token{
		ID:        id,
		Type:      TypeSlackToken,
		Value:     value,
		Filename:  ".slack_token",
		CreatedAt: now(),
	}, nil
}

// ----------------------------------------------------------------------------
// Kubeconfig
// ----------------------------------------------------------------------------

// GenerateKubeconfig generates a structurally valid kubeconfig YAML with fake
// cluster endpoint and bearer token.
func GenerateKubeconfig() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}

	clusterSuffix, err := randStr(12, lowerAlpha+digits)
	if err != nil {
		return Token{}, err
	}
	caData, err := randB64(72)
	if err != nil {
		return Token{}, err
	}
	bearerToken, err := randStr(40, alphaNum+"-_")
	if err != nil {
		return Token{}, err
	}

	value := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://k8s-%s.internal:6443
    certificate-authority-data: %s
  name: decoy-cluster
contexts:
- context:
    cluster: decoy-cluster
    user: decoy-admin
  name: decoy-context
current-context: decoy-context
preferences: {}
users:
- name: decoy-admin
  user:
    token: %s
`, clusterSuffix, caData, bearerToken)

	return Token{
		ID:        id,
		Type:      TypeKubeconfig,
		Value:     value,
		Filename:  "config",
		CreatedAt: now(),
	}, nil
}

// ----------------------------------------------------------------------------
// Database Dump
// ----------------------------------------------------------------------------

// GenerateDBDump generates a fake but realistic-looking PostgreSQL dump.
func GenerateDBDump() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}

	dbSuffix, err := randStr(8, lowerAlpha)
	if err != nil {
		return Token{}, err
	}

	// Fake bcrypt-style password hashes.
	makeHash := func() (string, error) {
		salt, err := randStr(22, bcryptChars)
		if err != nil {
			return "", err
		}
		hash, err := randStr(31, bcryptChars)
		if err != nil {
			return "", err
		}
		return "$2b$12$" + salt + hash, nil
	}

	hash1, err := makeHash()
	if err != nil {
		return Token{}, err
	}
	hash2, err := makeHash()
	if err != nil {
		return Token{}, err
	}
	hash3, err := makeHash()
	if err != nil {
		return Token{}, err
	}

	apiKey1, _ := randStr(32, alphaNum)
	apiKey2, _ := randStr(32, alphaNum)
	apiKey3, _ := randStr(32, alphaNum)

	ts := now().Format("2006-01-02 15:04:05")

	value := fmt.Sprintf(`-- PostgreSQL database dump
-- Host: db-%s.internal    Port: 5432    Database: production
-- Dumped by: pg_dump 15.2
-- Started on: %s

SET statement_timeout = 0;
SET standard_conforming_strings = on;

CREATE TABLE public.users (
    id bigint NOT NULL,
    email character varying(255) NOT NULL,
    password_hash character varying(255) NOT NULL,
    api_key character varying(64),
    role character varying(32) DEFAULT 'user',
    created_at timestamp with time zone DEFAULT now(),
    last_login_at timestamp with time zone
);

CREATE SEQUENCE public.users_id_seq START WITH 1 INCREMENT BY 1;
ALTER TABLE ONLY public.users ALTER COLUMN id SET DEFAULT nextval('public.users_id_seq');

INSERT INTO public.users (id, email, password_hash, api_key, role, created_at) VALUES
(1, 'alice@%s.internal',  '%s', '%s', 'admin', '%s'),
(2, 'bob@%s.internal',    '%s', '%s', 'user',  '%s'),
(3, 'charlie@%s.internal','%s', '%s', 'user',  '%s');

CREATE TABLE public.api_tokens (
    id bigint NOT NULL,
    token_hash character varying(255) NOT NULL,
    user_id bigint REFERENCES public.users(id) ON DELETE CASCADE,
    expires_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now()
);

-- Completed on: %s
`,
		dbSuffix, ts,
		dbSuffix, hash1, apiKey1, ts,
		dbSuffix, hash2, apiKey2, ts,
		dbSuffix, hash3, apiKey3, ts,
		ts,
	)

	return Token{
		ID:        id,
		Type:      TypeDBDump,
		Value:     value,
		Filename:  "backup.sql",
		CreatedAt: now(),
	}, nil
}

// ----------------------------------------------------------------------------
// DNS Canary Token
// ----------------------------------------------------------------------------

// GenerateDNSCanary generates a unique 16-char subdomain label.
// The user wires this into a domain they control during the deploy phase.
// Value holds the label; the full hostname is composed during deploy.
func GenerateDNSCanary() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}

	// 16 lowercase alphanumeric chars — valid DNS label, hard to guess.
	label, err := randStr(16, lowerAlpha+digits)
	if err != nil {
		return Token{}, fmt.Errorf("dns label: %w", err)
	}

	value := fmt.Sprintf(`# DNS Canary Token
# Label: %s
#
# Create a DNS record for this label on a domain you control:
#   %s.<your-domain>  IN  A  <any-IP>   (or CNAME to a sink)
#
# Decoyd will alert when a DNS query for this hostname is detected
# (requires DNS provider log access — configured in Phase 4).
label=%s
`, label, label, label)

	return Token{
		ID:        id,
		Type:      TypeDNSCanary,
		Value:     value,
		Filename:  "dns_canary.txt",
		CreatedAt: now(),
	}, nil
}
