#!/usr/bin/env bash
# Compara a versão mais nova que cada agente publicou com a versão com que o Iron
# Brake foi verificado (scripts/agents.json). Só lê fontes públicas (npm e a API
# de casks do Homebrew); não instala nada.
#
#   scripts/check-agent-versions.sh            tabela; sai com 1 se algum agente tem versão nova
#   scripts/check-agent-versions.sh --json     uma linha JSON por agente com versão nova
#
# Uma versão MENOR que a verificada (a fonte atrasou, ou voltou atrás) não conta.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
registry="$root/scripts/agents.json"
mode="${1:-table}"

latest_version() { # npm|brew nome
  local raw
  case "$1" in
    npm)  raw="$(curl -fsS --max-time 30 "https://registry.npmjs.org/$2/latest" | jq -r '.version')" ;;
    brew) raw="$(curl -fsS --max-time 30 "https://formulae.brew.sh/api/cask/$2.json" | jq -r '.version')" ;;
    *)    echo "fonte desconhecida: $1" >&2; return 1 ;;
  esac
  # o Homebrew junta versão e build com vírgula ("1.5.9,4515...").
  raw="${raw%%,*}"
  [[ -n "$raw" && "$raw" != "null" ]] || { echo "sem versão em $1:$2" >&2; return 1; }
  printf '%s' "$raw"
}

# newer A B: sucesso se A é estritamente mais nova que B.
newer() { [[ "$1" != "$2" && "$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -n1)" == "$1" ]]; }

found=0
failed=0
[[ "$mode" == "--json" ]] || printf '%-14s %-22s %-22s %s\n' AGENTE VERIFICADA MAIS-NOVA ESTADO
for agent in $(jq -r 'keys[]' "$registry"); do
  display="$(jq -r --arg a "$agent" '.[$a].display' "$registry")"
  verified="$(jq -r --arg a "$agent" '.[$a].verified' "$registry")"
  source="$(jq -r --arg a "$agent" '.[$a].latest | keys[0]' "$registry")"
  name="$(jq -r --arg a "$agent" '.[$a].latest | to_entries[0].value' "$registry")"

  if ! latest="$(latest_version "$source" "$name")"; then
    failed=1
    [[ "$mode" == "--json" ]] || printf '%-14s %-22s %-22s %s\n' "$agent" "$verified" "?" "FALHOU ao consultar $source:$name"
    continue
  fi
  if newer "$latest" "$verified"; then
    found=1
    if [[ "$mode" == "--json" ]]; then
      jq -cn --arg agent "$agent" --arg display "$display" --arg verified "$verified" --arg latest "$latest" \
        '{agent:$agent, display:$display, verified:$verified, latest:$latest}'
    else
      printf '%-14s %-22s %-22s %s\n' "$agent" "$verified" "$latest" "VERSÃO NOVA: retestar"
    fi
  elif [[ "$mode" != "--json" ]]; then
    printf '%-14s %-22s %-22s %s\n' "$agent" "$verified" "$latest" "em dia"
  fi
done

# Falha ao consultar não pode passar por "tudo em dia".
[[ $failed -eq 0 ]] || exit 2
[[ "$mode" == "--json" ]] && exit 0
exit "$found"
