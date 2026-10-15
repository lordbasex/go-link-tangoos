#!/usr/bin/env bash
# Publica el repositorio APT de TangoOS (dist/apt, lo arma `make apt-repo`) en
# https://tangoos.go-link.org/apt: el mismo bucket S3 y la misma distribución CloudFront que el
# portal (scripts/deploy-connect-web.sh, que excluye apt/ de su --delete).
#
#   make apt-repo && AWS_PROFILE=tangoos TANGOOS_DISTRIBUTION=ESSYABR5TAKTB scripts/publish-apt.sh
#
# Orden: primero el pool (inmutable: una versión publicada no cambia), después los índices
# (no-cache), así ningún índice apunta a un .deb que todavía no está. Antes de subir se verifica
# la firma de InRelease con la clave pública del repo.
set -euo pipefail

REPO="${1:-dist/apt}"
SUITE="${TANGOOS_CHANNEL:-stable}"
PROFILE="${AWS_PROFILE:-tangoos}"
BUCKET="${TANGOOS_BUCKET:-tangoos.go-link.org}"
DISTRIBUTION="${TANGOOS_DISTRIBUTION:?definí TANGOOS_DISTRIBUTION con el id de la distribución de CloudFront}"
KEYRING="build/keys/tangoos-archive-keyring.gpg"

[ -f "$REPO/dists/$SUITE/InRelease" ] || { echo "no hay repositorio firmado en $REPO: ejecutá make apt-repo" >&2; exit 1; }
cmp -s "$REPO/tangoos-archive-keyring.gpg" "$KEYRING" || { echo "$REPO/tangoos-archive-keyring.gpg no es $KEYRING" >&2; exit 1; }
gpgv --keyring "$PWD/$KEYRING" "$REPO/dists/$SUITE/InRelease" 2>/dev/null || { echo "la firma de InRelease no verifica con $KEYRING" >&2; exit 1; }
gpgv --keyring "$PWD/$KEYRING" "$REPO/dists/$SUITE/Release.gpg" "$REPO/dists/$SUITE/Release" 2>/dev/null || { echo "Release.gpg no verifica" >&2; exit 1; }

aws() { command aws --profile "$PROFILE" "$@"; }

aws s3 sync "$REPO/pool" "s3://$BUCKET/apt/pool" --cache-control "public, max-age=31536000, immutable" \
  --content-type application/vnd.debian.binary-package --only-show-errors
aws s3 sync "$REPO/dists/$SUITE" "s3://$BUCKET/apt/dists/$SUITE" --delete --cache-control "no-cache" \
  --content-type "text/plain; charset=utf-8" --only-show-errors
# Packages.gz con su tipo real (el sync de arriba le puso text/plain)
aws s3 cp "$REPO/dists/$SUITE/main/binary-amd64/Packages.gz" "s3://$BUCKET/apt/dists/$SUITE/main/binary-amd64/Packages.gz" \
  --cache-control "no-cache" --content-type application/gzip --only-show-errors
aws s3 cp "$REPO/tangoos-archive-keyring.gpg" "s3://$BUCKET/apt/tangoos-archive-keyring.gpg" \
  --cache-control "no-cache" --content-type application/pgp-keys --only-show-errors

id=$(aws cloudfront create-invalidation --distribution-id "$DISTRIBUTION" \
  --paths "/apt/dists/*" "/apt/tangoos-archive-keyring.gpg" "/install.sh" --query Invalidation.Id --output text)
echo "repositorio publicado en https://$BUCKET/apt (suite $SUITE, invalidación $id)"
