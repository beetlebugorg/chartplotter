#!/usr/bin/env bash
# GoReleaser post-build hook: sign + notarize macOS binaries with quill (Anchore),
# straight from the Linux release runner — no Mac needed. quill writes the Apple
# code signature into the Mach-O in place and submits it to Apple's notary service,
# so the archives GoReleaser builds afterwards contain the signed binary.
#
# It is a no-op for non-darwin targets, and a no-op when the signing credentials
# are not in the environment — so local `goreleaser release --snapshot`, forks, and
# PRs still build without secrets.
#
# Required env (wired from GitHub secrets on the release workflow):
#   QUILL_SIGN_P12        base64 of the "Developer ID Application" cert (.p12)
#   QUILL_SIGN_PASSWORD   the .p12 export password
#   QUILL_NOTARY_KEY      base64 of the App Store Connect API key (.p8)
#   QUILL_NOTARY_KEY_ID   the API key ID
#   QUILL_NOTARY_ISSUER   the App Store Connect issuer ID
# (quill reads all of these directly from the environment.)
#
# Usage, from a .goreleaser.yaml build post-hook:
#   quill-sign.sh "{{ .Path }}" "{{ .Os }}"
set -euo pipefail

bin="${1:?usage: quill-sign.sh <binary-path> <goos>}"
os="${2:?usage: quill-sign.sh <binary-path> <goos>}"

if [ "$os" != "darwin" ]; then
  exit 0 # only macOS binaries are code-signed
fi
if [ -z "${QUILL_SIGN_P12:-}" ]; then
  echo "quill-sign: no signing credentials in env — skipping $bin" >&2
  exit 0
fi

echo "quill-sign: signing + notarizing $bin" >&2
quill sign-and-notarize "$bin"
