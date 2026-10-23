#!/bin/sh
# Testa o install.sh em imagens Linux limpas (Docker), sem depender do GitHub:
# serve o dist/ (make dist) num servidor local e aponta o instalador para ele
# com IRON_BASE_URL. Uso:
#
#   make dist VERSION=v0.0.0-teste
#   scripts/test-install-linux.sh [imagem...]
#
# Sem argumentos, testa Debian, Ubuntu e Alpine na arquitetura do host.
# Para outra arquitetura: PLATFORM=linux/amd64 scripts/test-install-linux.sh
# No Docker Desktop (macOS), o container não enxerga o 127.0.0.1 do host:
#   HOST=host.docker.internal scripts/test-install-linux.sh
set -eu

cd "$(dirname "$0")/.."
[ -f dist/SHA256SUMS ] || { echo "rode make dist antes" >&2; exit 1; }

images="${*:-debian:stable-slim ubuntu:24.04 alpine:latest}"
port="${PORT:-8765}"
host="${HOST:-127.0.0.1}"
bind=127.0.0.1
net="--network host"
[ "$host" = 127.0.0.1 ] || { bind=0.0.0.0; net=; }

python3 -m http.server "$port" --bind "$bind" --directory dist >/dev/null 2>&1 &
server=$!
trap 'kill $server 2>/dev/null || true' EXIT
sleep 1

# Dentro do container: instala, confere a versão, e confirma que
# IRON_VERIFY_ATTESTATION=1 sem o gh falha (exit 1) sem instalar nada.
inner='
set -eu
export DEBIAN_FRONTEND=noninteractive
command -v curl >/dev/null 2>&1 || {
	if command -v apt-get >/dev/null 2>&1; then
		apt-get update -qq && apt-get install -y -qq curl ca-certificates >/dev/null
	else
		apk add -q curl
	fi
}
export HOME=/root
sh /install.sh
/root/.local/bin/iron version

if IRON_VERIFY_ATTESTATION=1 IRON_INSTALL_DIR=/root/sem-gh sh /install.sh >/tmp/out 2>&1; then
	echo "FALHA: o instalador passou sem o gh com IRON_VERIFY_ATTESTATION=1" >&2
	exit 1
fi
grep -q "precisa do gh" /tmp/out || { echo "FALHA: mensagem sem o gh não apareceu" >&2; cat /tmp/out >&2; exit 1; }
[ ! -e /root/sem-gh ] || { echo "FALHA: instalou algo sem o gh" >&2; exit 1; }
echo "OK"
'

status=0
for img in $images; do
	echo "===== $img ${PLATFORM:-}"
	if ! docker run --rm ${PLATFORM:+--platform "$PLATFORM"} $net \
		-v "$PWD/scripts/install.sh:/install.sh:ro" \
		-e IRON_BASE_URL="http://$host:$port" \
		"$img" sh -c "$inner"; then
		echo "FALHOU: $img" >&2
		status=1
	fi
done
exit $status
