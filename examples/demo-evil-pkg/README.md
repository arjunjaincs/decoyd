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
# 1. Deploy decoys
decoyd deploy --all

# 2. Start Decoyd (Tier 1 for full attribution, Tier 2 for basic)
sudo decoyd watch --tier1 &

# 3. Install the demo package (triggers the postinstall hook)
npm install ./examples/demo-evil-pkg

# 4. Check if Decoyd caught it
decoyd events --fail-if-any
```

## What it does

The `postinstall.js` script:
1. Lists well-known credential file paths (`.npmrc`, `.pypirc`, `id_rsa`, etc.)
2. Opens and reads each file found (reports file name + byte count only)
3. Immediately discards the contents — **no data is stored or transmitted**

If `decoyd-emu` is on `$PATH`, the script delegates to it (which provides
better process attribution in Tier 1 mode).

## Expected output

```
=== demo-evil-pkg: BENIGN TEST TOOL — postinstall credential scanner ===

.npmrc                                              142 bytes
.pypirc                                             (not found)
...
=== demo-evil-pkg: scan complete ===
```

Decoyd should report an alert with:
- `package.name`: `demo-evil-pkg`
- `package.confidence`: `high` (npm `postinstall` lifecycle event)
