#!/usr/bin/env bash
# Headless screenshot of a chartplotter URL — e.g. a "Share this view" link
# (<origin>/#share). Thin wrapper over scripts/shot.mjs (playwright-core) which
# waits for the wasm app to finish baking before capturing.
#
# Usage: scripts/shot.sh <url> [out.png] [width] [height] [settle-ms]
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
exec node "$here/shot.mjs" "$@"
