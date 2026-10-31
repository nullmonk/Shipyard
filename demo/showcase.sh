#!/usr/bin/env bash
# Shipyard showcase: validate -> inspect -> build -> test on two systems.
# Run and screen-record in one take: ./demo/showcase.sh
# (needs Docker + the Dagger engine; images are pulled on first run).
set -u
cd "$(dirname "$0")/showcase"
echo '### 1. Validate the shipfile'
shipyard validate
echo
echo '### 2. Inspect it'
shipyard info
echo
echo '### 3. Build it (watch the phase markers)'
shipyard build --no-remote .
echo
echo '### 4. Build once, test on two systems'
shipyard build --no-remote --test-system debian:bookworm --test-system archlinux:latest .
echo
echo '### done: one bundle, tested on two distros'
ls -la dist/ 2>/dev/null || true
