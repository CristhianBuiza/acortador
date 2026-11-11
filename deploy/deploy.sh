#!/usr/bin/env bash
# Compila y publica el binario en un servidor Linux por SSH. El primer despliegue
# crea el usuario, el directorio de datos y un acortador.env con un ADMIN_TOKEN aleatorio.
#
# Uso: HOST=root@203.0.113.10 DOMAIN=s.ejemplo.com ./deploy/deploy.sh
set -euo pipefail

: "${HOST:?Define HOST, por ejemplo HOST=root@203.0.113.10}"
: "${DOMAIN:?Define DOMAIN, por ejemplo DOMAIN=s.ejemplo.com}"
SSH_PORT="${SSH_PORT:-22}"
APP_PORT="${APP_PORT:-8095}"
APP_DIR=/opt/acortador

cd "$(dirname "$0")/.."
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/acortador .

scp -P "$SSH_PORT" dist/acortador deploy/acortador.service "$HOST:/tmp/"

ssh -p "$SSH_PORT" "$HOST" bash -s <<EOF
set -euo pipefail
id -u acortador >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin acortador
mkdir -p $APP_DIR/data
if [ ! -f $APP_DIR/acortador.env ]; then
  umask 077
  cat > $APP_DIR/acortador.env <<ENV
ADDR=127.0.0.1:$APP_PORT
DB_PATH=$APP_DIR/data/acortador.db
BASE_URL=https://$DOMAIN
ADMIN_TOKEN=\$(openssl rand -hex 24)
TRUST_PROXY=1
ENV
fi
chown -R acortador:acortador $APP_DIR/data
install -m 755 /tmp/acortador $APP_DIR/acortador
install -m 644 /tmp/acortador.service /etc/systemd/system/acortador.service
rm -f /tmp/acortador /tmp/acortador.service
systemctl daemon-reload
systemctl enable acortador >/dev/null
systemctl restart acortador
sleep 1
curl -fsS http://127.0.0.1:$APP_PORT/healthz && echo " · acortador activo"
EOF
