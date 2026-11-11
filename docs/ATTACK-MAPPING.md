# ATT&CK Mapping — Decoyd

> **Note**: Mapping based on ATT&CK Enterprise techniques referenced below.
> Verify technique IDs at https://attack.mitre.org/ before citing in reports.

## Primary Technique

| Technique | ID | Tactic | Description |
|-----------|-----|--------|-------------|
| Unsecured Credentials: Credentials In Files | T1552.001 | Credential Access | Adversaries search for credential files in well-known locations (`.npmrc`, `.pypirc`, `.netrc`, `id_rsa`, etc.). Decoyd places decoy credential files at these paths and alerts on access. |

## Related Techniques Detected by Decoyd

| Technique | ID | Tactic | Decoyd Coverage |
|-----------|-----|--------|-----------------|
| Supply Chain Compromise: Compromise Software Dependencies | T1195.002 | Initial Access | Tier 1 captures `npm_package_name`, `npm_package_version`, and `npm_lifecycle_event` from `/proc/<pid>/environ` when an npm lifecycle hook reads a decoy file. |
| Data from Local System | T1005 | Collection | Reading credential files from disk is a prerequisite to exfiltration. |
| Exfiltration over C2 Channel | T1041 | Exfiltration | Decoyd detects the local file access event; network exfiltration itself is out of scope. |
| Automated Collection | T1119 | Collection | Repeated accesses by the same PID can be correlated downstream (e.g. candidate Wazuh rule 100103, syntax-checked, untested). |
| OS Credential Dumping | T1003 | Credential Access | Credential files (`.git-credentials`, `.netrc`, `id_rsa`) are common local file targets. |

## Decoyd Token → ATT&CK Coverage

| Token Type | ATT&CK ID | Notes |
|------------|-----------|-------|
| `.npmrc` | T1552.001, T1195.002 | npm access token decoy (with npm env attribution in Tier 1) |
| `.pypirc` | T1552.001, T1195.002 | PyPI token decoy (Tier 1 gives PID/exe/cmdline; no pip-specific env parsing) |
| `docker/config.json` | T1552.001 | Container registry credential theft |
| `.git-credentials` | T1552.001 | Git hosting credential theft |
| `.netrc` | T1552.001 | FTP/HTTP auth credential theft |
| `id_rsa` / `id_ed25519` | T1552.004 | SSH private key theft |
| GCP service account JSON | T1552.001 | Cloud credential theft |
| `credentials.tfrc.json` | T1552.001 | Terraform Cloud credential theft |
| `.vault-token` | T1552.001 | HashiCorp Vault token theft |
| MCP `mcp.json` | T1552.001 | MCP server config credential theft |
| `.github_token` / `.slack_token` / AWS / Kubeconfig / `.env` / `backup.sql` | T1552.001 | Application / cloud / SCM credential file theft |

## Limitation

Decoyd is a **deception-based detection** tool, not a DLP or EDR. It detects access to decoy files only. It does not:
- Prevent credential theft from real credential files
- Detect in-memory credential theft (use OS EDR / memory analysis)
- Detect network exfiltration (use NDR)

Decoyd is intended as a high-fidelity tripwire on unused credential paths; in our WSL2 Tier 2 benchmark suite (`docs/BENCHMARKS.md`), `ls`, `stat`, `find`, `git status`, `grep -r` on a sibling directory, and a `50x` `lstat`/`stat` loop triggered 0 false positives (`cat <decoy>` triggered 1 alert). Production false-positive rates across full developer workstations are **UNKNOWN**.
