# Release

Como gerar e publicar uma versão do Iron Brake.

## Gerar os binários

```bash
make dist VERSION=v0.1.0
```

Gera em `dist/`:

| Arquivo | Para |
|---|---|
| `iron_linux_amd64`, `iron_linux_arm64` | Linux (estáticos: não dependem de nenhuma biblioteca do sistema) |
| `iron_darwin_amd64`, `iron_darwin_arm64` | macOS Intel e Apple Silicon (dependem só do `libSystem`, que todo Mac tem) |
| `iron_windows_amd64.exe`, `iron_windows_arm64.exe` | Windows (compila; **não testado**) |
| `install.sh` | instalador para macOS e Linux |
| `SHA256SUMS` | o hash SHA-256 de cada arquivo acima |

Os nomes não têm a versão, para que
`.../releases/latest/download/iron_linux_amd64` sempre aponte para a mais
recente. A versão fica **dentro** do binário (`iron version`).

`make clean` apaga só o `dist/`. O `bin/` nunca é apagado pelo Makefile: um
hook que aponta para um binário que sumiu **não bloqueia nada**.

## Publicar

1. Crie o repositório `IronDeploy/IronBrake` no GitHub (é o endereço que
   `scripts/install.sh` e o `README.md` usam; se o nome for outro, troque
   nos dois).
2. Em **Settings → Environments**, crie o ambiente `release` com
   **Required reviewers** (você). O job de release só roda depois da sua
   aprovação.
3. Em **Settings → Rules → Rulesets**, crie uma regra para **tags** `v*` que
   só você (ou um time) pode criar, e uma para a branch principal exigindo
   pull request. Proteja `.github/` com um `CODEOWNERS`.
4. Publique a versão:

   ```bash
   git tag v0.1.0
   git push origin v0.1.0
   ```

   O fluxo `.github/workflows/release.yml` roda os testes, gera o `dist/` e,
   depois da sua aprovação, cria a release com todos os arquivos.

## Segurança do fluxo

- **Ações fixadas pelo SHA do commit** (`actions/checkout@3d3c42e…  # v7.0.1`).
  Uma tag como `v7` pode ser movida para outro commit por quem controla o
  repositório da ação (ou por quem o invadir); o SHA não muda. O comentário
  com a versão é para humanos e para o Dependabot atualizar.
- **Permissões mínimas:** `permissions: {}` no topo; `contents: read` nos
  testes; `contents: write` só no job que cria a release.
- **`persist-credentials: false`:** o token não fica gravado no `.git` do
  runner para os passos seguintes.
- **Sem cache na release:** um cache envenenado por outro fluxo não entra no
  binário.
- **Sem ação de terceiros para publicar:** o `gh` já vem no runner.
- **Versão nunca colada no comando:** o Makefile passa a versão ao shell
  como variável de ambiente (`$$VERSION`), e o fluxo recusa tags fora de
  `vX.Y.Z`. O git aceita crase em nome de tag; antes, uma tag com crase
  executava um comando no `make dist`.
- **Nenhum agente de IA com escrita no fluxo de release.** Quem cria uma tag
  `v*` publica um binário que outras pessoas instalam com um comando e que o
  `SHA256SUMS` vai "confirmar" (o hash é gerado junto com o binário). Um
  agente com essa permissão — enganado por um texto numa issue, num README
  de dependência ou num comentário — publicaria código malicioso com a sua
  assinatura. Por isso: tokens de agentes só com leitura, sem escopo
  `workflow`, sem permissão de criar tags `v*`, e o ambiente `release`
  exigindo aprovação humana.

## O que o SHA256SUMS garante (e o que não)

O `install.sh` baixa o binário **e** o `SHA256SUMS` da mesma release e
confere um contra o outro. Isso pega download corrompido ou trocado no
caminho. **Não** pega uma release inteira adulterada (quem troca o binário
troca o `SHA256SUMS` junto). Para isso seria preciso assinar a release fora
do repositório (por exemplo, atestados de proveniência do GitHub,
`gh attestation verify`) — ainda não feito.
