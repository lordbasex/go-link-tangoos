#!/usr/bin/env bash
# Genera versiones livianas (WebP 800w y 1600w) de las capturas del portal TangoOS Connect.
#   scripts/optimize-connect-img.sh        (requiere cwebp; los PNG originales quedan intactos)
set -euo pipefail
SRC="${1:-web/apps/connect/public/img}"
OUT="$SRC/opt"
command -v cwebp >/dev/null || { echo "falta cwebp (brew install webp)" >&2; exit 1; }
mkdir -p "$OUT"
for f in "$SRC"/*.png; do
  [ -e "$f" ] || continue
  n=$(basename "$f" .png)
  for w in 800 1600; do
    dst="$OUT/$n-$w.webp"
    [ "$dst" -nt "$f" ] && continue
    cwebp -quiet -q 82 -m 6 -resize "$w" 0 "$f" -o "$dst"
  done
done
du -sh "$OUT"
