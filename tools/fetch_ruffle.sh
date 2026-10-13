#!/bin/bash
# Downloads the Ruffle Flash emulator (self-hosted build) into vendor/ruffle and checks its npm integrity hash.
set -e
cd "$(dirname "$0")/.."
VERSION=0.7.1
INTEGRITY='sha512-6ZWxx/cfX3RoNyQ1AVimECSae4gKqMxdPimOD+chiIcOFiJS0ATGKcjkretxNT03lqNUpiSyJjF55AuvvZ+QeA=='
TMP=$(mktemp -d)
curl -sL -o "$TMP/ruffle.tgz" "https://registry.npmjs.org/@ruffle-rs/ruffle/-/ruffle-$VERSION.tgz"
GOT="sha512-$(openssl dgst -sha512 -binary "$TMP/ruffle.tgz" | base64 -w0)"
[ "$GOT" = "$INTEGRITY" ] || { echo "integrity mismatch: $GOT" >&2; exit 1; }
tar -xzf "$TMP/ruffle.tgz" -C "$TMP"
rm -rf vendor/ruffle && mkdir -p vendor/ruffle
cp "$TMP"/package/{ruffle.js,core.ruffle.*.js,*.wasm,LICENSE_APACHE,LICENSE_MIT,package.json,README.md} vendor/ruffle/
rm -rf "$TMP"
echo "Ruffle $VERSION -> vendor/ruffle"
