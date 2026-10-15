#!/usr/bin/env bash
# Publica el portal TangoOS Connect (web/apps/connect/dist) en https://tangoos.go-link.org:
# un bucket S3 detrás de CloudFront, con el mismo esquema que scripts/deploy-web.sh de go-link-desk
# (desk.go-link.org). Los assets con hash se cachean un año; las páginas se revalidan en cada visita.
#
#   make connect-web-build && scripts/deploy-connect-web.sh
#
# Configuración (entorno): AWS_PROFILE (un usuario que solo pueda escribir este bucket e invalidar
# esta distribución), TANGOOS_BUCKET, TANGOOS_DISTRIBUTION (obligatoria: no hay valor por defecto).
# Las cabeceras de seguridad (CSP, etc.) van en la "response headers policy" de CloudFront: ver docs/16 §7.
set -euo pipefail

DIST="${1:-web/apps/connect/dist}"
PROFILE="${AWS_PROFILE:-go-link-desk}"
BUCKET="${TANGOOS_BUCKET:-tangoos.go-link.org}"
DISTRIBUTION="${TANGOOS_DISTRIBUTION:?definí TANGOOS_DISTRIBUTION con el id de la distribución de CloudFront}"

[ -f "$DIST/index.html" ] || { echo "no hay build en $DIST: ejecutá make connect-web-build" >&2; exit 1; }
if find "$DIST" -name '*.map' | grep -q .; then
  echo "hay source maps en $DIST: el build de producción no los genera" >&2
  exit 1
fi
if find "$DIST/img" -maxdepth 1 -name '*.png' 2>/dev/null | grep -q .; then
  echo "hay capturas PNG originales en $DIST/img: el build solo publica las WebP de img/opt" >&2
  exit 1
fi

aws() { command aws --profile "$PROFILE" "$@"; }

# Primero los assets (su nombre cambia con el contenido) y las imágenes optimizadas; después las
# páginas que los apuntan, así ninguna página apunta a algo que todavía no está.
aws s3 sync "$DIST/assets" "s3://$BUCKET/assets" --cache-control "public, max-age=31536000, immutable" --only-show-errors
[ -d "$DIST/img/opt" ] && aws s3 sync "$DIST/img/opt" "s3://$BUCKET/img/opt" --cache-control "public, max-age=31536000, immutable" --content-type image/webp --only-show-errors
aws s3 sync "$DIST" "s3://$BUCKET" --exclude "assets/*" --exclude "img/opt/*" --exclude "install.sh" --cache-control "no-cache" --only-show-errors
# install.sh (curl … | sh) como texto: se puede leer en el navegador antes de ejecutarlo.
[ -f "$DIST/install.sh" ] && aws s3 cp "$DIST/install.sh" "s3://$BUCKET/install.sh" --cache-control "no-cache" --content-type "text/plain; charset=utf-8" --only-show-errors
# Los assets viejos quedan un tiempo para las páginas ya abiertas; el resto se borra. apt/ es el
# repositorio APT (scripts/publish-apt.sh): no es parte del build y nunca se borra desde acá.
aws s3 sync "$DIST" "s3://$BUCKET" --delete --exclude "assets/*" --exclude "img/opt/*" --exclude "install.sh" --exclude "apt/*" --only-show-errors

id=$(aws cloudfront create-invalidation --distribution-id "$DISTRIBUTION" --paths "/*" --query Invalidation.Id --output text)
echo "publicado en https://$BUCKET (invalidación $id)"
