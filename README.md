# Iron Brake

O Iron Brake é um freio para agentes de IA no terminal. Ele roda como hook do
Claude Code e analisa cada comando **antes** de ele executar: libera o que é
seguro, pede sua confirmação para o que é arriscado e bloqueia o que destrói.

## Instalação (macOS e Linux)

```bash
curl -fsSL https://github.com/IronDeploy/IronBrake/releases/latest/download/install.sh | sh
```

O script descobre o seu sistema, baixa o binário, **confere o SHA-256** e
instala em `~/.local/bin/iron`, sem `sudo`. Se preferir ler antes de rodar:
baixe o `install.sh` da [página de releases](https://github.com/IronDeploy/IronBrake/releases),
leia e rode com `sh install.sh`. No Windows, baixe o `iron_windows_amd64.exe`
(ou `arm64`) da mesma página. Com Go instalado:
`go install github.com/IronDeploy/IronBrake/cmd/iron@latest`.

Depois, na pasta do seu projeto:

```bash
iron init      # instala o hook em .claude/settings.json
iron doctor    # confere se está protegendo
```

## Conferir com `iron doctor`

```
OK     1/5  settings.json contém o hook do Iron Brake
OK     2/5  o binário do hook existe e é executável
OK     3/5  o hook bloqueia um force push de teste
             o force push de teste foi bloqueado (código 2)
OK     4/5  o timeout do hook dá tempo ao Iron Brake
OK     5/5  versão

Tudo certo: o hook está instalado e bloqueou o force push de teste.
```

O teste 3 roda o hook de verdade com um `git push --force` falso: se não sair
**bloqueado**, o doctor falha e diz como corrigir. O teste 4 confere que um
`"timeout"` no `settings.json` não é curto demais (um hook que estoura o
tempo deixa o comando passar).

## O que ele bloqueia

**Sempre bloqueia** (catastrófico em qualquer máquina):

| Comando | |
|---|---|
| `git push --force`, `-f`, `+main`, `--mirror` | bloqueia |
| `git push origin :main` / `--delete` de branch protegida (main, master, production...) | bloqueia |
| `rm -rf /` · `/*` · `~` · `$HOME` · `/etc`, `/usr`... · `--no-preserve-root` | bloqueia |
| `dd of=/dev/DISCO`, `mkfs*`, `wipefs`, `shred` de disco, `> /dev/DISCO` | bloqueia |
| `chmod`/`chown -R` em caminho de sistema | bloqueia |

**Pergunta fora de produção, bloqueia em produção:**

| Comando | Fora de produção | Em produção |
|---|---|---|
| `terraform apply` **sem** plano salvo (`-auto-approve`) | bloqueia | bloqueia |
| `terraform apply tfplan` que apaga ou substitui recursos | pergunta, mostrando o cartão de risco | pergunta; **bloqueia** se for recurso crítico (banco, bucket, cluster) |
| `terraform destroy`, `apply -destroy` (e `tofu`) | pergunta | bloqueia |
| `terraform state rm`, `taint`, `workspace delete`, `force-unlock` | pergunta | bloqueia |
| `kubectl delete namespace`/`--all`/`pvc`/`pv`, `drain`, `scale --replicas=0`, `replace --force` | pergunta | bloqueia |
| `kubectl delete -f arq.yaml` com `kind: Namespace`/`PVC`/`PV` (lê o arquivo) | pergunta | bloqueia |
| `helm uninstall`, `helm rollback` | pergunta | bloqueia |
| `aws ... terminate-instances`/`delete-*`, `az ... delete`, `gcloud ... delete` | pergunta | bloqueia |
| `aws s3 rm --recursive`/`rb --force`, `gcloud storage rm`, `gsutil rm` | pergunta | bloqueia |
| `DROP DATABASE`/`SCHEMA`/`TABLE`, `TRUNCATE`, `DELETE` sem `WHERE` (via `psql`, `mysql`, `sqlite3`...), inclusive em `psql -f arq.sql` / `mysql < arq.sql` (lê o arquivo) | pergunta | bloqueia |
| `git push origin :branch` / `--delete` de outra branch remota | pergunta | pergunta |
| `git reset --hard`, `git clean -f`, `branch -D`, `tag -d`, `reflog expire`, `gc --prune=now`, `filter-branch`, `restore`/`checkout` de descarte | pergunta | bloqueia |
| `docker system`/`volume`/`image prune`, `volume rm`, `rm -f`, `compose down -v` | pergunta | bloqueia |
| `shutdown`/`reboot`, `systemctl stop`/`disable`, `crontab -r`, flush de firewall | pergunta | bloqueia |
| `curl`/`wget ... \| sh` (baixar e executar), `curl -X DELETE` | pergunta | bloqueia |
| `npm`/`yarn`/`pnpm publish`, `npm unpublish`, `cargo publish`, `gem push`, `twine upload` | pergunta | bloqueia |
| `find ... -delete`/`-exec`, `shred` de arquivo | pergunta | bloqueia |
| o mesmo comando de infra repetido (agente em loop) | 3× em 5 min: pergunta · 6×: bloqueia | igual |
| mais de 3 applies ou 20 recursos alterados na sessão | pergunta | pergunta |

"Produção" = as palavras `prod`, `production` ou `prd` no comando, na pasta,
no workspace do terraform, no contexto do kubectl ou em `AWS_PROFILE`. Para
acrescentar as suas, crie `.iron/policy.yaml`:

```yaml
production_patterns: [live]
critical_resource_types: [google_sql_database_instance]
```

No macOS, "pergunta" abre uma janela com o motivo e os botões **Cancelar** e
**Executar**; o Claude espera a sua resposta. Toda decisão fica registrada em
`~/.iron/audit.log` (sem o comando, só a classe), e `iron audit verify`
confere se o log foi alterado.

## Exemplo: o primeiro bloqueio

Peça ao Claude Code: *"rode `git push --force origin main`"*. O comando não
roda, e aparece na conversa:

```
PreToolUse:Bash hook error: [/Users/voce/.local/bin/iron hook]: Iron Brake: force push bloqueado.
Ele reescreve o histórico remoto e pode apagar o trabalho de outras pessoas. Use um push
normal; se realmente for necessário, peça ao usuário para executar.
```

(O começo da linha vem do Claude Code e pode mudar entre versões; o texto a
partir de "Iron Brake:" é do Iron Brake.) O Claude recebe esse motivo e muda
de estratégia (em geral, propõe um push
normal ou pede que você mesmo rode o comando).

## Iron Shield: o que o agente enxerga

O freio (Iron Brake) barra a ação; o **Iron Shield** ataca a causa — a
credencial ao alcance do agente. Dois comandos:

```bash
iron scan            # raio-X das credenciais que o agente alcançaria (só leitura)
iron init --harden   # instala o hook E bloqueia a leitura desses lugares
```

O `iron scan` **nunca lê nem imprime o valor de um segredo** — só tipo, local e
gravidade. Ele avalia: variáveis de ambiente, arquivos `.env`, perfis do
`~/.aws`, chaves em `~/.ssh`, `~/.kube/config`, state e token do Terraform,
`.npmrc`, credencial do Docker, token do `gh` e token embutido na URL de remote
git. Exemplo:

```
🔴 CRÍTICO
   • AWS · ~/.aws/credentials [perfil default] — perfil AWS com chave de longa duração
🟠 ALTO
   • .env · ~/projeto/.env — segredo em texto puro no arquivo (ex.: GITHUB_TOKEN)
```

O `iron init --harden` grava regras `deny` de leitura (`Read(~/.aws/**)`,
`Read(**/.env)`, …) no `.claude/settings.json`, tirando as credenciais do
caminho do agente. Detalhes e tabela completa em [doc/iron-shield.md](doc/iron-shield.md).

## Limites (leia antes de confiar)

- **Só enxerga comandos de terminal** (a ferramenta Bash do Claude Code).
  Arquivos editados, chamadas de MCP e APIs não passam por ele.
- **Não substitui permissões mínimas.** Se o agente usa uma credencial que
  pode apagar o banco de produção, o banco está em risco: dê ao agente
  credenciais que **não conseguem** fazer o que ele não deve. O Iron Brake é
  uma camada a mais, não a principal.
- **Enxerga através de disfarces de shell:** `sh -c "..."`, `eval`, prefixos
  (`env`, `sudo`, `nohup`, `timeout`, `xargs`), aspas, `$'\x2d'`, subshells e
  encadeamentos (`&&`, `;`, `|`) são desmontados e analisados. **Mas:** o
  comando montado em variável (`$RM -rf /`), scripts e Makefiles não são
  analisados por dentro.
- **Não é contra um agente malicioso:** ele protege de erros e loops. Um
  agente com o seu usuário pode apagar o estado da sessão ou reescrever o log.
- A janela de confirmação só existe no macOS; no Linux e no Windows, o motivo
  da pergunta não aparece antes da decisão. O Windows compila, mas não foi
  testado.

A lista completa, com cada caso verificado, está em
[doc/known-issues.md](doc/known-issues.md); o que cada regra faz, em
[doc/spec.md](doc/spec.md).

## Compilar do código

```bash
make test     # testes
make build    # bin/iron para esta máquina
make dist     # dist/: 6 binários (Linux, macOS, Windows × amd64, arm64) + SHA256SUMS
```

## Licença

[Apache 2.0](LICENSE).
