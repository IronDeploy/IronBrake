#!/bin/sh
# instalar o iron com um comando. Descobre o sistema e a
# arquitetura, baixa o binário e o SHA256SUMS da release do GitHub, confere o
# hash ANTES de instalar e instala em ~/.local/bin (sem sudo).
#
#   curl -fsSL https://github.com/IronDeploy/IronBrake/releases/latest/download/install.sh | sh
#
# Variáveis opcionais:
#   IRON_VERSION=v0.1.0         instala uma versão específica (padrão: a mais recente)
#   IRON_INSTALL_DIR=/pasta     onde instalar (padrão: ~/.local/bin)
#   IRON_BASE_URL=http://...    baixa de outro lugar (para testar sem o GitHub)
#   IRON_VERIFY_ATTESTATION=1   exige a verificação do atestado de proveniência
#                               (precisa do gh, GitHub CLI, com login). Sem isso,
#                               o instalador só confere o SHA256SUMS, que vem da
#                               mesma release e não pega uma release adulterada.

set -eu

REPO="IronDeploy/IronBrake"
VERSION="${IRON_VERSION:-latest}"
INSTALL_DIR="${IRON_INSTALL_DIR:-$HOME/.local/bin}"

fail() {
	echo "iron: $1" >&2
	exit 1
}

# Sistema: só macOS e Linux por este script (no Windows, baixe o .exe da release).
case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) fail "sistema não suportado por este script: $(uname -s). Baixe o binário na página de releases." ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "arquitetura não suportada: $(uname -m)" ;;
esac

if [ -n "${IRON_BASE_URL:-}" ]; then
	base="$IRON_BASE_URL"
elif [ "$VERSION" = "latest" ]; then
	base="https://github.com/$REPO/releases/latest/download"
else
	base="https://github.com/$REPO/releases/download/$VERSION"
fi

command -v curl >/dev/null 2>&1 || fail "precisa do curl"

file="iron_${os}_${arch}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "iron: baixando $file"
curl -fsSL -o "$tmp/$file" "$base/$file" || fail "não consegui baixar $base/$file"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || fail "não consegui baixar $base/SHA256SUMS"

# Confere o hash: o binário baixado precisa ser exatamente o da release.
expected="$(awk -v f="$file" '$2 == f { print $1 }' "$tmp/SHA256SUMS")"
[ -n "$expected" ] || fail "$file não está no SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then
	actual="$(sha256sum "$tmp/$file" | awk '{ print $1 }')"
else
	actual="$(shasum -a 256 "$tmp/$file" | awk '{ print $1 }')"
fi
[ "$actual" = "$expected" ] || fail "o hash não confere (esperado $expected, obtido $actual). Nada foi instalado."
echo "iron: hash SHA-256 conferido"

# Atestado de proveniência: assinado pelo GitHub, fora da release. Prova que o
# arquivo foi gerado pelo fluxo de release deste repositório (o SHA256SUMS não
# prova, porque quem troca o binário troca o SHA256SUMS junto).
if [ "${IRON_VERIFY_ATTESTATION:-}" = "1" ]; then
	command -v gh >/dev/null 2>&1 || fail "IRON_VERIFY_ATTESTATION=1 precisa do gh (https://cli.github.com). Nada foi instalado."
	gh attestation verify "$tmp/$file" --repo "$REPO" \
		--signer-workflow "$REPO/.github/workflows/release.yml" >/dev/null 2>&1 ||
		fail "o atestado de proveniência não confere (ou o gh não está logado: gh auth login). Nada foi instalado."
	echo "iron: atestado de proveniência conferido (gerado pelo fluxo de release de $REPO)"
else
	echo "iron: dica: para conferir também que o binário saiu do fluxo de release, use IRON_VERIFY_ATTESTATION=1 (precisa do gh)"
fi

mkdir -p "$INSTALL_DIR"
chmod 0755 "$tmp/$file"
mv "$tmp/$file" "$INSTALL_DIR/iron"
"$INSTALL_DIR/iron" version

echo "iron: instalado em $INSTALL_DIR/iron"
case ":$PATH:" in
*":$INSTALL_DIR:"*) ;;
*) echo "iron: atenção: $INSTALL_DIR não está no PATH. Adicione ao seu ~/.zshrc ou ~/.bashrc:
      export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
esac
echo "iron: próximo passo, na pasta do seu projeto:  iron init && iron doctor"
