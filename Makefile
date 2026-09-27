# compilar o iron para uso local e gerar os binários de release
# para Linux, macOS e Windows (amd64 e arm64), com o arquivo SHA256SUMS.
#
#   make test                  roda todos os testes
#   make build                 compila bin/iron para esta máquina
#   make dist                  gera dist/ com os 6 binários, o install.sh e o SHA256SUMS
#   make dist VERSION=v0.1.0   o mesmo, gravando a versão no binário
#   make clean                 apaga dist/ (o bin/ fica: hooks instalados apontam para bin/iron)

# Versão gravada no binário (iron version). No fluxo de release é a tag.
# Vai para o shell como variável de ambiente ($$VERSION), nunca colada no
# comando: uma versão como "$(comando)" não é executada.
VERSION ?= dev
export VERSION

# Sistemas e arquiteturas da release: SISTEMA/ARQUITETURA.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

# -s -w       tira a tabela de símbolos e a informação de depuração (binário menor)
# -X          troca o valor da variável main.version na compilação
LDFLAGS := -s -w -X main.version=$$VERSION

# CGO_ENABLED=0  não usa código C: o binário não depende de bibliotecas do sistema
# -trimpath      tira do binário os caminhos da máquina que compilou
GOBUILD := CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)"

.PHONY: test build dist clean

test:
	go test -count=1 ./...

build:
	$(GOBUILD) -o bin/iron ./cmd/iron

# Cada binário se chama iron_SISTEMA_ARQUITETURA (.exe no Windows), sem a
# versão no nome: assim o link .../releases/latest/download/iron_linux_amd64
# sempre aponta para a versão mais recente.
dist:
	rm -rf dist
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=""; if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		echo "compilando $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch $(GOBUILD) -o dist/iron_$${os}_$${arch}$$ext ./cmd/iron || exit 1; \
	done
	cp scripts/install.sh dist/install.sh
	cd dist && shasum -a 256 iron_* install.sh > SHA256SUMS
	@echo "pronto: dist/"
	@cat dist/SHA256SUMS

# Não apaga o bin/: um hook que aponta para um binário que sumiu NÃO bloqueia
# nada (o Claude Code trata como erro e o comando passa).
clean:
	rm -rf dist
