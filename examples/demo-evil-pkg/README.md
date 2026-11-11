# demo-evil-pkg — Benign Test Package

> **BENIGN TEST TOOL**: This package does **not** exfiltrate, modify, or
> transmit any data. It exists solely to test Decoyd's credential-access
> detection.

## Purpose

This npm package simulates a malicious package that reads well-known credential
file paths during its `postinstall` lifecycle hook. When used with Decoyd, it
triggers decoy file alerts so you can verify that Decoyd detects the access and
attributes it to this package.

## Usage

```bash
# 1. Deploy decoy files (e.g., via the interactive `decoyd` TUI, which writes
#    decoy files and records them in ~/.decoyd/deployed_tokens.json)
decoyd

# 2. Start Decoyd (Tier 1 for full attribution, Tier 2 for basic)
sudo decoyd watch --tier1 &

# 3. Install the demo package (triggers the postinstall hook)
npm install --no-save ./examples/demo-evil-pkg

# 4. Check if Decoyd caught it
decoyd events --json
decoyd events --fail-if-any
```

## What it does

The `postinstall.js` script:
1. Lists well-known credential file paths (`.npmrc`, `.pypirc`, `id_rsa`, etc.)
2. Opens and reads each file found (reports file name + byte count only)
3. Immediately discards the contents — **no data is stored or transmitted**

If `decoyd-emu` is on `$PATH`, the script delegates to it (performing the same
read-only credential scan in a subprocess).

## Expected output

```
=== demo-evil-pkg: BENIGN TEST TOOL — postinstall credential scanner ===

.npmrc                                              142 bytes
.pypirc                                             (not found)
...
=== demo-evil-pkg: scan complete ===
```

In Tier 1 mode, `decoyd events --json` includes an attribution object with:
- `watcher_tier`: `"tier1"`
- `attribution.accessor.pid`, `attribution.accessor.exe`, `attribution.accessor.cmdline`
- `attribution.confidence`: `3` (high), `2` (medium), or `1` (low)
- `attribution.npm_package`: `"demo-evil-pkg"`
- `attribution.npm_version`: `"0.0.1"`
- `attribution.npm_lifecycle_event`: `"postinstall"`
