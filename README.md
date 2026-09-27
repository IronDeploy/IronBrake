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

| Comando | Fora de produção | Em produção |
|---|---|---|
| `git push --force`, `-f`, `+main` | bloqueia | bloqueia |
| `git push --force-with-lease` | pergunta | pergunta |
| `terraform apply` **sem** plano salvo (`-auto-approve`) | bloqueia | bloqueia |
| `terraform apply tfplan` que apaga ou substitui recursos | pergunta, mostrando o cartão de risco | pergunta; **bloqueia** se for recurso crítico (banco, bucket, cluster) |
| `terraform destroy`, `terraform apply -destroy` | pergunta | bloqueia |
| `kubectl delete namespace`, `kubectl delete --all`, `kubectl drain` | pergunta | bloqueia |
| `aws ... terminate-instances` / `delete-*`, `az ... delete`, `gcloud ... delete` | pergunta | bloqueia |
| `DROP DATABASE`, `DROP TABLE`, `TRUNCATE`, `DELETE` sem `WHERE` (via `psql`, `mysql`, `sqlite3`...) | pergunta | bloqueia |
| `git reset --hard`, `git clean -f` | pergunta | bloqueia |
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

## Limites (leia antes de confiar)

- **Só enxerga comandos de terminal** (a ferramenta Bash do Claude Code).
  Arquivos editados, chamadas de MCP e APIs não passam por ele.
- **Não substitui permissões mínimas.** Se o agente usa uma credencial que
  pode apagar o banco de produção, o banco está em risco: dê ao agente
  credenciais que **não conseguem** fazer o que ele não deve. O Iron Brake é
  uma camada a mais, não a principal.
- **Escapa com esforço:** `sh -c "..."`, prefixos (`env`, `sudo`), scripts e
  Makefiles não são analisados por dentro.
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
