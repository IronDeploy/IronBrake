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
   **Required reviewers**. O job de release só roda depois da aprovação.
3. Em **Settings → Rules → Rulesets**, crie uma regra para **tags** `v*` que
   só você (ou um time) pode criar, e uma para a branch principal exigindo
   pull request. Proteja `.github/` com um `CODEOWNERS`.
4. Publique a versão:

   ```bash
   git tag vX.Y.Z
   git push origin vX.Y.Z
   ```

   O fluxo `.github/workflows/release.yml` roda os testes, gera o `dist/` e,
   depois da aprovação, cria a release com todos os arquivos.

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
- **Atestado de proveniência** dos binários, do `install.sh` e do
  `SHA256SUMS`, assinado fora da release (seção abaixo). `id-token: write` e
  `attestations: write` só no job de release.
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
caminho. **Não** pega uma release inteira adulterada: quem troca o binário
troca o `SHA256SUMS` junto. Quem faz essa conferência é o atestado abaixo.

## Atestado de proveniência (`gh attestation verify`)

O passo "Atestar a proveniência" do fluxo (`actions/attest-build-provenance`,
fixada pelo SHA) assina, com a identidade do próprio fluxo, cada binário, o
`install.sh` e o `SHA256SUMS`. A assinatura fica no GitHub, **fora da
release**: trocar um arquivo da release não a refaz. O job de release ganha
`id-token: write` e `attestations: write` (só ele; o resto continua sem
permissão).

**Conferir um binário baixado** (precisa do [gh](https://cli.github.com),
com `gh auth login`):

```bash
gh attestation verify iron_linux_amd64 --repo IronDeploy/IronBrake \
  --signer-workflow IronDeploy/IronBrake/.github/workflows/release.yml
```

Para exigir uma versão exata, some `--source-ref refs/tags/vX.Y.Z`. Passando,
o `gh` confirma que o arquivo tem exatamente o hash atestado e que foi gerado
por aquele fluxo, a partir daquela tag e daquele commit.

**No instalador**, a conferência é opcional (o `gh` não vem instalado na
maioria das máquinas):

```bash
curl -fsSL https://github.com/IronDeploy/IronBrake/releases/latest/download/install.sh \
  | IRON_VERIFY_ATTESTATION=1 sh
```

Com `IRON_VERIFY_ATTESTATION=1`, o instalador confere o hash e o atestado, e
**não instala nada** se faltar o `gh`, se ele não estiver logado ou se o atestado
não bater. Sem a variável, só confere o hash e mostra uma dica.

**O que o atestado garante:** o arquivo foi gerado pelo `release.yml` deste
repositório, a partir de uma tag. **O que não garante:** que o código seja
inofensivo. Quem consegue rodar o fluxo (criar a tag `v*` e aprovar o ambiente
`release`) publica um binário atestado do mesmo jeito; por isso valem os
controles da seção "Segurança do fluxo" (tags protegidas, aprovação humana,
nenhum agente de IA com escrita). E o atestado só vale se quem confere pede o
fluxo certo (`--signer-workflow`), não qualquer fluxo do repositório.

**Verificado em 2026-09-29 (`v0.1.1`):** o `gh attestation verify` (gh 2.101.0)
passou para os 6 binários, o `install.sh` e o `SHA256SUMS`, com
`--signer-workflow` e `--source-ref refs/tags/v0.1.1`. O `IRON_VERIFY_ATTESTATION=1`
do instalador foi testado no macOS arm64 num diretório temporário: instalou a
release real e recusou um binário adulterado com o `SHA256SUMS` ajustado junto
(sem a variável, o mesmo binário foi instalado). Não testado: Linux e o caso de
`gh` ausente.

Se o `gh` responder `HTTP 403 ... forbids access via a fine-grained personal
access tokens`, o login está usando um PAT com validade acima de 366 dias.
Refaça com `gh auth logout && gh auth login` e escolha o navegador (OAuth).
