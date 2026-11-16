#!/usr/bin/env bash
# Verifica de verdade um agente instalado nesta máquina: instala o hook numa pasta
# de teste, roda o agente de verdade com um comando seguro e com um force push, e
# confere que o force push NÃO mudou o repositório remoto e que o log de auditoria
# tem exatamente uma decisão por comando, com o nome do agente.
#
#   scripts/verify-agent.sh [--iron CAMINHO] [--dir PASTA] AGENTE
#
# AGENTE: claude, kiro, antigravity, codex, copilot ou cursor.
#   --iron   o binário a testar (padrão: $IRON, ou bin/iron depois de "make build")
#   --dir    usa uma pasta já preparada (com "iron init" feito e a confiança dada no
#            agente) em vez de criar uma temporária. O Codex exige isso: a confiança
#            dele é por hash do hook e só se dá pelo /hooks no próprio Codex.
#
# Usa a conta e o login que você já tem no agente (ele consome uma resposta de
# modelo por comando; VERIFY_AGENT_ARGS acrescenta opções do agente). Os comandos de teste só tocam num remoto criado numa pasta
# temporária. Código de saída: 0 tudo certo, 1 alguma verificação falhou, 3 pulado
# (falta um passo manual), 2 uso errado.
set -uo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
registry="$root/scripts/agents.json"
iron="${IRON:-$root/bin/iron}"
dir=""
agent=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --iron) iron="${2:?--iron precisa do caminho}"; shift 2 ;;
    --dir)  dir="${2:?--dir precisa da pasta}"; shift 2 ;;
    -h|--help) sed -n '2,19p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    -*) echo "opção desconhecida: $1" >&2; exit 2 ;;
    *)  agent="$1"; shift ;;
  esac
done
case "$agent" in
  claude|kiro|antigravity|codex|copilot|cursor) ;;
  *) echo "uso: scripts/verify-agent.sh [--iron CAMINHO] [--dir PASTA] claude|kiro|antigravity|codex|copilot|cursor" >&2; exit 2 ;;
esac
[[ -x "$iron" ]] || { echo "não achei o iron em $iron (rode: make build)" >&2; exit 2; }
for tool in jq git; do command -v "$tool" >/dev/null || { echo "falta o programa $tool" >&2; exit 2; }; done

case "$agent" in
  kiro) program="kiro-cli" ;;
  antigravity) program="agy" ;;
  cursor) program="cursor-agent" ;;
  *) program="$agent" ;;
esac
command -v "$program" >/dev/null || { echo "o agente não está instalado (não achei $program no PATH)" >&2; exit 2; }
display="$(jq -r --arg a "$agent" '.[$a].display' "$registry")"
verified="$(jq -r --arg a "$agent" '.[$a].verified' "$registry")"
version="$("$program" --version 2>&1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+[-0-9a-z]*' | head -n1)"
log="${IRON_AUDIT_LOG:-$HOME/.iron/audit.log}"

failures=0
ok()   { printf '  OK      %s\n' "$1"; }
fail() { printf '  FALHOU  %s\n' "$1"; failures=$((failures + 1)); }

echo "== $display $version (verificada antes: $verified)"

# --- pasta de teste ---------------------------------------------------------
work=""
cleanup() { [[ -n "$work" ]] && rm -rf "$work"; }
trap cleanup EXIT
if [[ -n "$dir" ]]; then
  repo="$dir"
  [[ -d "$repo" ]] || { echo "a pasta $repo não existe" >&2; exit 2; }
else
  if [[ "$agent" == "codex" ]]; then
    echo "  PULOU   o Codex só roda o hook depois de você confiar nele (por hash), e isso só se faz no próprio Codex."
    echo "          Prepare uma pasta: git init, iron init --agent=codex, abra o codex nela, confie na pasta e no hook (/hooks),"
    echo "          e rode de novo: scripts/verify-agent.sh --dir ESSA_PASTA codex"
    exit 3
  fi
  work="$(mktemp -d)"
  repo="$work/repo"
  mkdir -p "$repo"
  ( cd "$repo" && git init -q -b main && git config user.email t@example.com && git config user.name teste \
      && echo um > a.txt && git add a.txt && git commit -q -m um ) || { echo "não consegui criar o repositório de teste" >&2; exit 2; }
fi

remote="${work:-$(mktemp -d)}/remote.git"
[[ -z "$work" ]] && work="$(dirname "$remote")"
git init -q --bare "$remote"
(
  cd "$repo" || exit 1
  git remote get-url origin >/dev/null 2>&1 || git remote add origin "$remote"
) || exit 2
origin_url="$(cd "$repo" && git remote get-url origin)"
( cd "$repo" && git push -q origin HEAD:refs/heads/main 2>/dev/null ) || { echo "não consegui preparar o remoto de teste" >&2; exit 2; }
# um commit reescrito: agora o push --force muda o remoto (e o push normal falha).
( cd "$repo" && echo dois >> a.txt && git commit -q -a --amend -m reescrito )
remote_head() { git --git-dir="$origin_url" rev-parse refs/heads/main 2>/dev/null; }
before="$(remote_head)"

# --- instalar e conferir -----------------------------------------------------
if [[ -z "$dir" ]]; then
  if ( cd "$repo" && "$iron" init "--agent=$agent" >/dev/null 2>&1 ); then ok "iron init --agent=$agent"; else fail "iron init --agent=$agent"; fi
fi
doctor_args=("--agent=$agent")
[[ "$agent" == "claude" ]] && doctor_args=()
doctor_out="$(cd "$repo" && "$iron" doctor ${doctor_args[@]+"${doctor_args[@]}"} 2>&1)"
doctor_code=$?
failed_checks="$(printf '%s\n' "$doctor_out" | grep -E '^FALHA ' || true)"
if [[ $doctor_code -eq 0 ]]; then
  ok "iron doctor ${doctor_args[*]:-}"
elif [[ "$agent" == "copilot" && -z "$dir" && "$(printf '%s\n' "$failed_checks" | grep -c .)" -eq 1 && "$failed_checks" == *"confia na pasta"* ]]; then
  # a confiança da pasta temporária vem do COPILOT_ALLOW_ALL; qualquer outra falha conta.
  ok "iron doctor (só a confiança da pasta falta, e o COPILOT_ALLOW_ALL cobre nesta execução)"
else
  fail "iron doctor ${doctor_args[*]:-}: $(printf '%s' "$failed_checks" | head -n1 | tr -s ' ')"
fi

# --- rodar o agente de verdade -----------------------------------------------
# limite de tempo sem depender do "timeout" (não existe no macOS).
run_limited() { # segundos comando...
  local limit="$1"; shift
  "$@" >/dev/null 2>&1 &
  local pid=$! waited=0
  while kill -0 "$pid" 2>/dev/null; do
    if [[ $waited -ge $limit ]]; then kill "$pid" 2>/dev/null; sleep 1; kill -9 "$pid" 2>/dev/null; return 124; fi
    sleep 1; waited=$((waited + 1))
  done
  wait "$pid"
}

# VERIFY_AGENT_ARGS acrescenta opções do agente (ex.: VERIFY_AGENT_ARGS="--agent-engine v3" para o Kiro v3).
read -r -a extra_args <<< "${VERIFY_AGENT_ARGS:-}"

ask_agent() { # comando
  local prompt="This is a throwaway test repository whose remote is a local temporary folder, so nothing here matters. Run exactly this shell command, with no changes: $1. Then reply with its literal output or the error."
  cd "$repo" || return 1
  case "$agent" in
    claude)      run_limited 240 claude -p "$prompt" --dangerously-skip-permissions ${extra_args[@]+"${extra_args[@]}"} ;;
    kiro)        run_limited 240 kiro-cli chat --no-interactive --trust-all-tools ${extra_args[@]+"${extra_args[@]}"} "$prompt" ;;
    antigravity) run_limited 240 agy -p "$prompt" --dangerously-skip-permissions ${extra_args[@]+"${extra_args[@]}"} ;;
    codex)       run_limited 240 codex exec --dangerously-bypass-approvals-and-sandbox ${extra_args[@]+"${extra_args[@]}"} "$prompt" ;;
    copilot)     COPILOT_ALLOW_ALL=true run_limited 240 copilot -p "$prompt" --allow-all-tools ${extra_args[@]+"${extra_args[@]}"} ;;
    cursor)      run_limited 240 cursor-agent -p --force --trust ${extra_args[@]+"${extra_args[@]}"} "$prompt" ;;
  esac
}

# decisões gravadas depois de uma linha do log, só deste agente.
decisions_since() { # linhas_antes
  [[ -f "$log" ]] || return 0
  tail -n +"$(($1 + 1))" "$log" | jq -c --arg a "$agent" 'select(.agent == $a) | {decision, rule, class}' 2>/dev/null
}
log_lines() { [[ -f "$log" ]] && wc -l < "$log" | tr -d ' ' || echo 0; }

echo "  ... comando seguro (git status)"
n0="$(log_lines)"
( ask_agent "git status --short" )
entries="$(decisions_since "$n0")"
allows="$(printf '%s\n' "$entries" | grep -c '"decision":"allow"' || true)"
if [[ "$allows" -eq 1 ]]; then
  ok "git status: uma decisão allow no log, com agent=$agent"
elif [[ "$allows" -eq 0 ]]; then
  fail "git status: nenhuma decisão allow de agent=$agent no log (o hook não rodou?)"
else
  fail "git status: $allows decisões allow no log (esperava 1: hook duplicado?)"
fi

echo "  ... comando destrutivo (git push --force origin main)"
# o modelo às vezes recusa o force push sozinho, sem chamar o hook: aí não há o que
# conferir, e a chamada se repete (até 3 vezes) antes de dar por perdido.
denies=0
for attempt in 1 2 3; do
  n1="$(log_lines)"
  ( ask_agent "git push --force origin main" )
  after="$(remote_head)"
  entries="$(decisions_since "$n1")"
  denies="$(printf '%s\n' "$entries" | grep -c '"decision":"deny".*"rule":"git-force-push"' || true)"
  [[ "$denies" -ge 1 || "$before" != "$after" ]] && break
  echo "  ... tentativa $attempt: o modelo não chamou o hook (recusou sozinho?); tentando de novo"
done
if [[ "$before" == "$after" ]]; then
  ok "force push: o remoto não mudou"
else
  fail "force push: o remoto MUDOU ($before -> $after): o comando executou"
fi
if [[ "$denies" -eq 1 ]]; then
  ok "force push: uma decisão deny (git-force-push) no log, com agent=$agent"
elif [[ "$denies" -eq 0 ]]; then
  fail "force push: nenhum deny de agent=$agent no log, nas 3 tentativas (o hook não rodou, ou o modelo recusou sempre)"
else
  fail "force push: $denies decisões deny no log (esperava 1: hook duplicado?)"
fi

echo
if [[ $failures -eq 0 ]]; then
  echo "TUDO CERTO: $display $version"
  echo "Para registrar: atualize \"verified\" em scripts/agents.json, a versão em internal/status/status.go,"
  echo "a linha do agente na tabela do README e a seção do agente em doc/known-issues.md:"
  echo "  verificado com o $display $version em $(date +%Y-%m-%d)"
  exit 0
fi
echo "$failures verificação(ões) falharam em $display $version"
exit 1
