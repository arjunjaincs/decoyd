#!/usr/bin/env node
// =============================================================================
// BENIGN TEST TOOL — demo-evil-pkg/postinstall.js
//
// This script simulates what a malicious npm package might do in its
// postinstall hook: it reads well-known credential file paths and reports
// their existence and size.
//
// It does NOT exfiltrate, modify, or transmit any data.
// It is used exclusively to trigger Decoyd decoy file alerts in CI/tests.
// =============================================================================
'use strict';

const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const home = os.homedir();

const credPaths = [
  path.join(home, '.npmrc'),
  path.join(home, '.pypirc'),
  path.join(home, '.docker', 'config.json'),
  path.join(home, '.git-credentials'),
  path.join(home, '.netrc'),
  path.join(home, '.ssh', 'id_rsa'),
  path.join(home, '.vault-token'),
  path.join(home, '.cursor', 'mcp.json'),
];

console.log('=== demo-evil-pkg: BENIGN TEST TOOL — postinstall credential scanner ===');
console.log();

// Try to call decoyd-emu if it's on PATH (preferred — triggers attribution)
const emu = spawnSync('decoyd-emu', credPaths, { encoding: 'utf8' });
if (emu.status === 0) {
  process.stdout.write(emu.stdout);
} else {
  // Fallback: scan directly in JS (still triggers Decoyd inotify/fsnotify)
  for (const p of credPaths) {
    try {
      const stat = fs.lstatSync(p);
      if (!stat.isFile()) {
        console.log(`${path.basename(p).padEnd(50)} (not a regular file)`);
        continue;
      }
      const data = fs.readFileSync(p);
      const n = data.length;
      // discard immediately
      console.log(`${path.basename(p).padEnd(50)} ${n} bytes`);
    } catch (e) {
      if (e.code === 'ENOENT') {
        console.log(`${path.basename(p).padEnd(50)} (not found)`);
      } else {
        console.log(`${path.basename(p).padEnd(50)} (error: ${e.code})`);
      }
    }
  }
}

console.log();
// Hold the Node process open briefly (300 ms) so asynchronous Linux Tier 1
// /proc attribution (exe, environ, ancestor chain) completes while live.
setTimeout(() => {
  console.log('=== demo-evil-pkg: scan complete ===');
}, 300);
