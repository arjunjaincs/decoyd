# ATT&CK Mapping — Decoyd

> **Note**: This mapping is based on the ATT&CK Enterprise matrix v15.
> Verify technique IDs at https://attack.mitre.org/ before citing in reports.

## Primary Technique

| Technique | ID | Tactic | Description |
|-----------|-----|--------|-------------|
| Unsecured Credentials: Credentials In Files | T1552.001 | Credential Access | Adversaries search for credential files in well-known locations (`.npmrc`, `.pypirc`, `.netrc`, `id_rsa`, etc.). Decoyd places decoy credential files at these paths and alerts on access. |

## Related Techniques Detected by Decoyd

| Technique | ID | Tactic | Decoyd Coverage |
|-----------|-----|--------|-----------------|
| Supply Chain Compromise: Compromise Software Dependencies | T1195.002 | Initial Access | Decoyd's package attribution detects npm/pip install scripts that access credential files. |
| Data from Local System | T1005 | Collection | Reading credential files from disk is a prerequisite to exfiltration. |
| Exfiltration over C2 Channel | T1041 | Exfiltration | Decoyd detects the access event; exfiltration itself is out of scope but the decoy triggers incident response before data leaves. |
| Automated Collection | T1119 | Collection | A process accessing multiple decoy files in a short window (rule 100103) may indicate automated credential sweeping. |
| OS Credential Dumping | T1003 | Credential Access | Credential files (`.git-credentials`, `.netrc`, `id_rsa`) are targets of credential-dumping tools. Decoyd complements memory-based detections. |

## Decoyd Token → ATT&CK Coverage

| Token Type | ATT&CK ID | Notes |
|------------|-----------|-------|
| `.npmrc` | T1552.001, T1195.002 | npm install-time theft |
| `.pypirc` | T1552.001, T1195.002 | pip/twine credential theft |
| `docker/config.json` | T1552.001 | Container registry credential theft |
| `.git-credentials` | T1552.001 | Git hosting credential theft |
| `.netrc` | T1552.001 | FTP/HTTP auth credential theft |
| `id_rsa` | T1552.004 | SSH private key theft |
| GCP service account JSON | T1552.001 | Cloud credential theft |
| `credentials.tfrc.json` | T1552.001 | Terraform Cloud credential theft |
| `.vault-token` | T1552.001 | HashiCorp Vault token theft |
| MCP `config.json` | T1552.001 | AI agent/MCP server token theft |
| GitHub PAT (via git-credentials/mcp) | T1552.001 | SCM token theft |

## Limitation

Decoyd is a **deception-based detection** tool, not a DLP or EDR. It detects access to decoy files only. It does not:
- Prevent credential theft from real credential files
- Detect in-memory credential theft (use OS EDR / memory analysis)
- Detect network exfiltration (use NDR)

Decoyd is intended as a **high-fidelity tripwire** with low false-positive rate.
