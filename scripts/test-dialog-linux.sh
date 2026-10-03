#!/usr/bin/env bash
# Testa a janela de confirmação do Linux com o zenity de verdade, dentro do
# Docker: um servidor X virtual (Xvfb), um gerenciador de janelas e o xdotool
# para clicar. Roda na raiz do repositório: scripts/test-dialog-linux.sh
set -euo pipefail
cd "$(dirname "$0")/.."

IMAGE=iron-dialog-linux
ARCH=$(docker info --format '{{.Architecture}}' | sed 's/x86_64/amd64/;s/aarch64/arm64/')
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

docker build -q -t "$IMAGE" - <<'DOCKERFILE' >/dev/null
FROM debian:trixie-slim
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    xvfb xdotool zenity openbox fonts-dejavu-core dbus-x11 && rm -rf /var/lib/apt/lists/*
RUN useradd -m tester
USER tester
DOCKERFILE

GOOS=linux GOARCH=$ARCH go test -c -o "$WORK/dialog.test" ./internal/dialog

cat > "$WORK/run.sh" <<'INNER'
#!/usr/bin/env bash
export LANG=C.UTF-8 GSK_RENDERER=cairo IRON_DIALOG_LIVE=1
Xvfb :99 -screen 0 1024x768x24 >/dev/null 2>&1 &
export DISPLAY=:99
sleep 1; openbox >/dev/null 2>&1 &
sleep 1
fail=0

# scenario NOME ESPERADA AÇÃO: abre a janela, age sobre ela e confere a resposta.
scenario() {
  local name=$1 expect=$2 action=$3
  IRON_DIALOG_EXPECT=$expect /work/dialog.test -test.run TestConfirmLinuxLive >/tmp/out 2>&1 &
  local pid=$!
  if [ -n "$action" ]; then
    sleep 3
    local win; win=$(xdotool search --name 'Iron Brake' | head -1)
    eval "$action"
  fi
  if wait $pid; then echo "ok    $name -> $expect"; else echo "FALHA $name (esperava $expect)"; cat /tmp/out; fail=1; fi
}

geom() { eval "$(xdotool getwindowgeometry --shell "$win")"; }
click() { geom; xdotool mousemove $((X + WIDTH * 717 / 1000)) $((Y + HEIGHT - 52)) click 1; }   # Executar
cancel() { geom; xdotool mousemove $((X + WIDTH * 373 / 1000)) $((Y + HEIGHT - 52)) click 1; }  # Cancelar

scenario "clicar em Executar"   approved 'click'
scenario "clicar em Cancelar"   rejected 'cancel'
scenario "Enter (padrão)"       rejected 'xdotool key --window "$win" Return; xdotool key Return'
scenario "Esc"                  rejected 'xdotool key Escape'
scenario "fechar a janela"      rejected 'xdotool key alt+F4'
scenario "tempo esgotado (6 s)" rejected ''
(
  unset DISPLAY
  scenario "sem display"        unavailable ''
)
exit $fail
INNER
chmod +x "$WORK/run.sh"

docker run --rm -v "$WORK":/work "$IMAGE" bash /work/run.sh
