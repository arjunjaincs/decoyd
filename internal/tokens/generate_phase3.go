package tokens

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
)

// ----------------------------------------------------------------------------
// .npmrc token
// ----------------------------------------------------------------------------

// npm registry auth token format: npm_<35 alphanumeric chars>
// Source: https://docs.npmjs.com/about-access-tokens
// Format confirmed as "npm_" prefix + ~36 chars; we use 35 for the body.
// UNKNOWN: exact character set not spec'd; using alphanumeric as observed in practice.
func GenerateNpmRC() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}
	body, err := randStr(35, alphaNum)
	if err != nil {
		return Token{}, fmt.Errorf("npmrc token: %w", err)
	}
	token := "npm_" + body
	value := fmt.Sprintf("//registry.npmjs.org/:_authToken=%s\n", token)
	return Token{
		ID:        id,
		Type:      TypeNpmRC,
		Value:     value,
		Filename:  ".npmrc",
		CreatedAt: now(),
	}, nil
}

// ----------------------------------------------------------------------------
// .pypirc token
// ----------------------------------------------------------------------------

// PyPI API token format: pypi-<base64url ~60 chars>
// Source: https://pypi.org/help/#apitoken (format: "pypi-" prefix + base64url)
// UNKNOWN: exact length not documented; using 60 chars of base64url as observed.
func GeneratePypiRC() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}
	// 60 chars of base64url — plausible pypi token body
	b := make([]byte, 45)
	if _, err := rand.Read(b); err != nil {
		return Token{}, fmt.Errorf("pypirc token: %w", err)
	}
	tokenBody := base64.URLEncoding.EncodeToString(b)[:60]
	token := "pypi-" + tokenBody
	value := fmt.Sprintf("[distutils]\nindex-servers = pypi\n\n[pypi]\nrepository = https://upload.pypi.org/legacy/\nusername = __token__\npassword = %s\n", token)
	return Token{
		ID:        id,
		Type:      TypePypiRC,
		Value:     value,
		Filename:  ".pypirc",
		CreatedAt: now(),
	}, nil
}

// ----------------------------------------------------------------------------
// Docker config.json token
// ----------------------------------------------------------------------------

// GenerateDockerConfig generates a Docker registry config.json with a fake auth token.
// Format: standard docker config.json with base64(user:password) auth field.
// Source: https://docs.docker.com/engine/reference/commandline/login/
func GenerateDockerConfig() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}
	// Generate a random 40-char alphanumeric password (registry token)
	pass, err := randStr(40, alphaNum)
	if err != nil {
		return Token{}, fmt.Errorf("docker token: %w", err)
	}
	// Docker stores base64(username:password)
	authStr := base64.StdEncoding.EncodeToString([]byte("oauth2accesstoken:" + pass))

	config := map[string]any{
		"auths": map[string]any{
			"https://index.docker.io/v1/": map[string]any{
				"auth": authStr,
			},
		},
		"HttpHeaders": map[string]any{
			"User-Agent": "Docker-Client/24.0.6 (linux)",
		},
	}
	data, err := json.MarshalIndent(config, "", "\t")
	if err != nil {
		return Token{}, fmt.Errorf("docker config json: %w", err)
	}
	return Token{
		ID:        id,
		Type:      TypeDockerConfig,
		Value:     string(data) + "\n",
		Filename:  "config.json",
		CreatedAt: now(),
	}, nil
}

// ----------------------------------------------------------------------------
// .git-credentials token
// ----------------------------------------------------------------------------

// GenerateGitCredentials generates a .git-credentials file with a fake GitHub token.
// Format: https://user:token@github.com per git-credential-store(1).
func GenerateGitCredentials() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}
	// Reuse the GitHub PAT generator for the token value.
	pat, err := GenerateGitHubPAT()
	if err != nil {
		return Token{}, fmt.Errorf("git-credentials pat: %w", err)
	}
	// Extract just the token value (first line after stripping prefix)
	patValue := pat.Value
	value := fmt.Sprintf("https://user:%s@github.com\n", patValue)
	return Token{
		ID:        id,
		Type:      TypeGitCredentials,
		Value:     value,
		Filename:  ".git-credentials",
		CreatedAt: now(),
	}, nil
}

// ----------------------------------------------------------------------------
// .netrc token
// ----------------------------------------------------------------------------

// GenerateNetrc generates a .netrc file with a fake API credential.
// Format: standard netrc(5) format.
func GenerateNetrc() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}
	pass, err := randStr(32, alphaNum)
	if err != nil {
		return Token{}, fmt.Errorf("netrc password: %w", err)
	}
	value := fmt.Sprintf("machine api.github.com\n  login token\n  password %s\n\nmachine uploads.github.com\n  login token\n  password %s\n", pass, pass)
	return Token{
		ID:        id,
		Type:      TypeNetrc,
		Value:     value,
		Filename:  ".netrc",
		CreatedAt: now(),
	}, nil
}

// ----------------------------------------------------------------------------
// RSA-2048 private key (PKCS#1 PEM)
// ----------------------------------------------------------------------------

// GenerateRSAKey generates a real RSA-2048 private key in PKCS#1 PEM format.
// The private key is never registered with any service.
// Note: RSA key generation is slow (~50-200ms). Tests should not call this 200x in a tight loop.
func GenerateRSAKey() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return Token{}, fmt.Errorf("rsa keygen: %w", err)
	}
	der := x509.MarshalPKCS1PrivateKey(privKey)
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: der,
	}
	pemBytes := pem.EncodeToMemory(block)
	return Token{
		ID:        id,
		Type:      TypeRSAKey,
		Value:     string(pemBytes),
		Filename:  "id_rsa",
		CreatedAt: now(),
	}, nil
}

// ----------------------------------------------------------------------------
// GCP service-account JSON
// ----------------------------------------------------------------------------

// GenerateGCPServiceAccount generates a realistic GCP service-account JSON.
// Includes a real but unregistered RSA PKCS#8 private key, a 40-hex key_id,
// and a 21-digit client_id.
// Source: https://cloud.google.com/iam/docs/service-account-overview
func GenerateGCPServiceAccount() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}
	// Generate a real RSA-2048 key for the private_key field (PKCS#8 PEM).
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return Token{}, fmt.Errorf("gcp rsa keygen: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		return Token{}, fmt.Errorf("gcp pkcs8: %w", err)
	}
	block := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: der,
	}
	privateKeyPEM := string(pem.EncodeToMemory(block))

	// 40-hex key_id
	keyIDBytes := make([]byte, 20)
	if _, err := rand.Read(keyIDBytes); err != nil {
		return Token{}, fmt.Errorf("gcp key_id: %w", err)
	}
	keyID := fmt.Sprintf("%x", keyIDBytes)

	// 21-digit client_id (matches GCP format)
	var clientIDNum big.Int
	clientIDNum.SetString("100000000000000000000", 10)            // 10^20
	max, _ := new(big.Int).SetString("900000000000000000000", 10) // 9 * 10^20
	nonce, err := rand.Int(rand.Reader, max)
	if err != nil {
		return Token{}, fmt.Errorf("gcp client_id: %w", err)
	}
	clientID := new(big.Int).Add(&clientIDNum, nonce).String()

	// Random project ID
	projBody, err := randStr(10, lowerAlpha+digits)
	if err != nil {
		return Token{}, fmt.Errorf("gcp project: %w", err)
	}
	projectID := "project-" + projBody

	// Random service account email
	saBody, err := randStr(8, lowerAlpha)
	if err != nil {
		return Token{}, fmt.Errorf("gcp sa: %w", err)
	}
	saEmail := saBody + "@" + projectID + ".iam.gserviceaccount.com"

	sa := map[string]any{
		"type":                        "service_account",
		"project_id":                  projectID,
		"private_key_id":              keyID,
		"private_key":                 privateKeyPEM,
		"client_email":                saEmail,
		"client_id":                   clientID,
		"auth_uri":                    "https://accounts.google.com/o/oauth2/auth",
		"token_uri":                   "https://oauth2.googleapis.com/token",
		"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
		"client_x509_cert_url":        "https://www.googleapis.com/robot/v1/metadata/x509/" + saEmail,
		"universe_domain":             "googleapis.com",
	}
	data, err := json.MarshalIndent(sa, "", "  ")
	if err != nil {
		return Token{}, fmt.Errorf("gcp json: %w", err)
	}
	return Token{
		ID:        id,
		Type:      TypeGCPServiceAccount,
		Value:     string(data) + "\n",
		Filename:  "service-account.json",
		CreatedAt: now(),
	}, nil
}

// ----------------------------------------------------------------------------
// Terraform credentials.tfrc.json
// ----------------------------------------------------------------------------

// GenerateTerraformCreds generates a Terraform credentials file.
// Format: https://developer.hashicorp.com/terraform/cli/config/config-file#credentials
// Token format: 51 alphanumeric chars (plausible; exact format not publicly documented).
// UNKNOWN: Terraform Cloud token exact format; using plausible 51-char alphanumeric.
func GenerateTerraformCreds() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}
	token, err := randStr(51, alphaNum)
	if err != nil {
		return Token{}, fmt.Errorf("terraform token: %w", err)
	}
	creds := map[string]any{
		"credentials": map[string]any{
			"app.terraform.io": map[string]any{
				"token": token,
			},
		},
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return Token{}, fmt.Errorf("terraform json: %w", err)
	}
	return Token{
		ID:        id,
		Type:      TypeTerraformCreds,
		Value:     string(data) + "\n",
		Filename:  "credentials.tfrc.json",
		CreatedAt: now(),
	}, nil
}

// ----------------------------------------------------------------------------
// HashiCorp Vault token file
// ----------------------------------------------------------------------------

// GenerateVaultToken generates a Vault token file.
// Format: Vault tokens have format s.<24 alphanumeric chars> (service tokens).
// Source: https://developer.hashicorp.com/vault/docs/concepts/tokens#token-types-in-detail
// Note: "s." prefix is for service tokens (the most common type).
func GenerateVaultToken() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}
	body, err := randStr(24, alphaNum)
	if err != nil {
		return Token{}, fmt.Errorf("vault token: %w", err)
	}
	token := "s." + body
	// Vault tokens are stored in ~/.vault-token as a bare token string.
	value := token + "\n"
	return Token{
		ID:        id,
		Type:      TypeVaultToken,
		Value:     value,
		Filename:  ".vault-token",
		CreatedAt: now(),
	}, nil
}

// ----------------------------------------------------------------------------
// MCP config (~/.cursor/mcp.json) with fake GitHub PAT
// ----------------------------------------------------------------------------

// GenerateMCPConfig generates a Cursor-style MCP server config containing a
// fake GitHub PAT as an API key. The PAT uses the same verified checksum
// algorithm as GenerateGitHubPAT.
// Format: https://docs.cursor.com/context/model-context-protocol (MCP config)
func GenerateMCPConfig() (Token, error) {
	id, err := NewID()
	if err != nil {
		return Token{}, err
	}
	// Reuse the GitHub PAT generator for the token value.
	pat, err := GenerateGitHubPAT()
	if err != nil {
		return Token{}, fmt.Errorf("mcp github pat: %w", err)
	}
	patValue := pat.Value

	config := map[string]any{
		"mcpServers": map[string]any{
			"github": map[string]any{
				"command": "npx",
				"args":    []string{"-y", "@modelcontextprotocol/server-github"},
				"env": map[string]any{
					"GITHUB_PERSONAL_ACCESS_TOKEN": patValue,
				},
			},
		},
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return Token{}, fmt.Errorf("mcp config json: %w", err)
	}
	return Token{
		ID:        id,
		Type:      TypeMCPConfig,
		Value:     string(data) + "\n",
		Filename:  "mcp.json",
		CreatedAt: now(),
	}, nil
}
