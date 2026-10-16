#!/bin/sh
# Instaladores de TangoOS para escritorio (ADR 0018) dentro de la imagen de electron-builder:
# Linux (AppImage, deb, rpm) y Windows (NSIS, con wine). Uso: sh scripts/package.sh [linux|win|all]
set -eu
cd "$(dirname "$0")/.."
TARGET=${1:-all}
IMAGE=electronuserland/builder:24-wine-mono-05.26
STAGE=stage
rm -rf "$STAGE" && mkdir -p "$STAGE"
# Solo lo que va en la app: el main y los preloads ya traen sus dependencias (vite, ssr.noExternal).
cp -R dist assets electron-builder.yml "$STAGE"/
node -e '
const p = require("./package.json");
const out = { name: "tangoos-desktop", productName: "TangoOS", version: p.version, description: p.description, author: { name: "TangoOS", email: "noreply@tangoos.org" }, license: p.license, main: p.main, homepage: "https://github.com/lordbasex/go-link-tangoos" };
require("fs").writeFileSync("stage/package.json", JSON.stringify(out, null, 2));
'
EV=$(node -p 'require("../../node_modules/electron/package.json").version')
case "$TARGET" in
  linux) ARGS="--linux AppImage deb rpm" ;;
  win) ARGS="--win nsis" ;;
  *) ARGS="--linux AppImage deb rpm --win nsis" ;;
esac
docker run --rm -v "$PWD/$STAGE":/project -w /project \
  -e ELECTRON_CACHE=/root/.cache/electron -e ELECTRON_BUILDER_CACHE=/root/.cache/electron-builder \
  -v tangoos-electron-cache:/root/.cache \
  "$IMAGE" sh -c "npx --yes electron-builder@26.15.3 --config electron-builder.yml -c.electronVersion=$EV $ARGS"
ls -la "$STAGE/release" | grep -E '\.(AppImage|deb|rpm|exe)$'
