# Iron Brake

O Iron Brake é um freio para agentes de IA no terminal. Ele roda como hook do
agente (Claude Code, Kiro CLI, Antigravity CLI ou Codex CLI) e analisa cada comando **antes** de ele executar: libera o que é
seguro, pede sua confirmação para o que é arriscado e bloqueia o que destrói.

## Instalação (macOS e Linux)

```bash
curl -fsSL https://github.com/IronDeploy/IronBrake/releases/latest/download/install.sh | sh
```

O script descobre o seu sistema, baixa o binário, **confere o SHA-256** e
instala em `~/.local/bin/iron`, sem `sudo`. Se essa pasta não estiver no PATH, o
instalador a adiciona ao perfil do seu shell (`~/.zshrc` no zsh; `~/.bash_profile`
no bash do macOS; `~/.bashrc` no bash do Linux; fish via `fish_add_path`). Abra um
novo terminal depois. Para não alterar o perfil, use `IRON_NO_MODIFY_PATH=1`.
Se preferir ler antes de rodar:
baixe o `install.sh` da [página de releases](https://github.com/IronDeploy/IronBrake/releases),
leia e rode com `sh install.sh`. No Windows, baixe o `iron_windows_amd64.exe`
(ou `arm64`) da mesma página. Com Go instalado:
`go install github.com/IronDeploy/IronBrake/cmd/iron@latest`.

O SHA-256 vem da mesma release do binário: pega download corrompido, não uma
release adulterada. Para conferir que o binário foi gerado pelo fluxo de release
do repositório (atestado assinado pelo GitHub, fora da release), com o
[gh](https://cli.github.com) instalado e logado:

```bash
curl -fsSL https://github.com/IronDeploy/IronBrake/releases/latest/download/install.sh | IRON_VERIFY_ATTESTATION=1 sh
# ou, num binário já baixado:
gh attestation verify iron_linux_amd64 --repo IronDeploy/IronBrake \
  --signer-workflow IronDeploy/IronBrake/.github/workflows/release.yml
```

Nesse modo, o instalador não instala nada se o atestado não bater. Detalhes e
limites em [doc/release.md](doc/release.md).

Depois, na pasta do seu projeto:

```bash
iron init      # instala o hook em .claude/settings.json
iron doctor    # confere se está protegendo
```

## Conferir com `iron doctor`

```
OK     1/9  settings.json contém o hook do Iron Brake
OK     2/9  o binário do hook existe e é executável
OK     3/9  o hook bloqueia um force push de teste
             o force push de teste foi bloqueado (código 2)
OK     4/9  o timeout do hook dá tempo ao Iron Brake
OK     5/9  os programas que o Iron Brake executa (terraform, tofu, terragrunt) são confiáveis
             terraform: /opt/homebrew/bin/terraform (PATH)
OK     6/9  o log de auditoria grava e a corrente está íntegra
             /Users/voce/.iron/audit.log: 42 linha(s) em 1 arquivo(s), 7.8 KB
OK     7/9  o hook está vendo os comandos que o agente executa (iron watch)
             3 comando(s) de shell desde 01/10 09:12, todos com decisão do Iron Brake
OK     8/9  etiqueta do agente na AWS (opcional)
             AWS_SDK_UA_APP_ID=iron-claude: o CloudTrail mostra app/iron-claude nas chamadas do agente
OK     9/9  versão

Tudo certo: o hook está instalado, bloqueou o force push de teste e o log de auditoria está gravando.
```

O teste 3 roda o hook de verdade com um `git push --force` falso: se não sair
**bloqueado**, o doctor falha e diz como corrigir. O teste 4 confere que um
`"timeout"` no `settings.json` não é curto demais (um hook que estoura o
tempo deixa o comando passar). O teste 5 falha se o `terraform`, o `tofu` ou o `terragrunt` achado no `PATH` estiver
dentro do projeto ou puder ser alterado por qualquer usuário (um programa falso
mostraria ao Iron Brake um plano inventado); para fixar o programa, veja
`~/.iron/config.yaml` abaixo. O teste 6 falha se o log não puder ser gravado, se
a última gravação do hook falhou (o aviso no stderr do hook quase nunca chega
até você) ou se a corrente de hashes estiver quebrada. O teste 7 roda o mesmo
cruzamento do `iron watch --once` sobre as sessões do Claude Code desde que o hook foi
instalado: **falha** se algum comando de shell rodou sem decisão (o hook não está
disparando; se você instalou com o agente aberto, reinicie a sessão). O 8 só informa se a
etiqueta do agente na AWS está gravada.

## Conferir de fora com `iron watch`

O hook decide antes do comando, mas não sabe se **deixou de rodar** (agente que o
ignora, configuração errada, ou um modo do agente em que ele não carrega, como o
Kiro v3 com `--no-interactive` ou um hook do Codex ainda não confiado). O
`iron watch` lê o transcript do agente e o log de auditoria e avisa quando um
comando rodou **sem nenhuma decisão** do Iron Brake:

```bash
iron watch             # acompanha esta pasta e avisa na hora (--notify para notificação)
iron watch --once      # confere a última hora e sai com código 1 se houver lacuna
iron watch --agent=codex --once   # também: antigravity, kiro (o padrão é claude)
```

Só observa (não bloqueia). Lê os transcripts do Claude Code, do Kiro CLI, do Antigravity CLI e do Codex CLI
(os três últimos guardam as sessões de todos os projetos numa pasta só: o `iron watch` confere só os comandos desta pasta).
O `iron doctor --agent=NOME` inclui o mesmo cruzamento. Detalhes e limites em [doc/watch.md](doc/watch.md).

## Portão de credenciais da AWS

Para o que o hook não enxerga (um script, um SDK, um agente que não entrega o comando ao
hook), o `iron aws-creds` é a ponta do `credential_process` do `~/.aws/config`: humano passa
direto; **agente** em produção só recebe credenciais depois de você clicar em "Executar", e
cada pedido de agente fica no log. Também há uma regra do hook contra o agente extrair a
credencial real (`aws configure export-credentials`). O `iron init` ainda etiqueta o agente
(`AWS_SDK_UA_APP_ID`) para o CloudTrail mostrar `app/iron-claude` nas chamadas dele, e o
`iron aws-alerts` imprime um modelo CloudFormation que avisa quando um agente chama uma API
destrutiva. Configuração, limites e o que foi verificado em [doc/aws.md](doc/aws.md).

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
no workspace do terraform (também o de `-chdir` e o de `cd pasta && ...`), no contexto do kubectl ou em `AWS_PROFILE`. Para
acrescentar as suas, crie `.iron/policy.yaml`:

```yaml
production_patterns: [live]
critical_resource_types: [google_sql_database_instance]
assume_production: true   # opcional: cluster/perfil/workspace sem nome de teste (dev, staging, qa...) vale como produção
audit_log:            # opcional: guardar mais do log de auditoria (de 5 a 100)
  max_size_mb: 20     # tamanho de cada arquivo antes de girar (padrão 5)
  keep: 10            # arquivos antigos guardados (padrão 5)
```

No macOS, "pergunta" abre uma janela com o motivo e os botões **Cancelar** e
**Executar**; o Claude espera a sua resposta. Toda decisão fica registrada em
`~/.iron/audit.log` (sem o comando, só a classe), e `iron audit verify`
confere se o log foi alterado. O log gira sozinho (5 MB por arquivo, 5 antigos guardados) sem quebrar a
corrente, e o `iron doctor` avisa se ele não está gravando. O `audit_log` do
`policy.yaml` só aumenta essa retenção, nunca reduz.

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

## Agentes

| Agente | Instalar na pasta do projeto | Estado |
|---|---|---|
| Claude Code | `iron init` | suportado |
| Kiro CLI (engines v2 e v3, uso interativo e v2 não interativo) | `iron init --agent=kiro` e `iron doctor --agent=kiro` | suportado, verificado com o Kiro CLI 2.26.1 em 2026-10-01 |
| Antigravity CLI (`agy`), uso interativo e `-p` | `iron init --agent=antigravity` e `iron doctor --agent=antigravity` | suportado, verificado com o Antigravity CLI 1.2.14 em 2026-10-01 |
| Codex CLI, chat e `codex exec` | `iron init --agent=codex` e `iron doctor --agent=codex` | suportado, verificado com o Codex CLI 0.159.3 em 2026-10-01, **depois de você confiar no hook** (veja abaixo) |
| Kiro CLI v3 com `--no-interactive` | | **não suportado**: o Kiro não executa hooks nesse modo |
| Kiro IDE | | **não suportado**: o hook do IDE não recebe o comando |

**Instalar em mais de um agente.** `iron init` sem opções instala o hook do Claude Code (como sempre) e, se achar outros agentes
nesta máquina (a pasta `~/.kiro`, `~/.gemini/antigravity-cli` ou `~/.codex`, ou o programa no PATH), **pergunta um por um**
quando está num terminal e, sem terminal, só mostra o comando. `iron init --agent=kiro,codex` instala nos agentes listados
(`claude`, `kiro`, `antigravity` ou `agy`, `codex`) e `iron init --agent=all` em todos os detectados; o `--harden` vale só para o Claude.
`iron status` mostra, por agente, se o hook está instalado nesta pasta, com que versão ele foi verificado, a última decisão
registrada e os avisos (hook não confiado no Codex, `hooks.json` inválido no Antigravity, programa que não existe...), sem executar
o hook nem gravar no log. Para testar de verdade, rode `iron doctor --agent=NOME`.

No Kiro, o `iron init` grava o hook nos dois formatos: dentro do agente (`.kiro/agents/`, o v2 só
lê assim) e em `.kiro/hooks/iron-brake.json` (o v3). Se a pasta não tem `kiro_default.json`, ele
cria um mínimo, que **substitui o agente padrão do Kiro nessa pasta** (sem o prompt longo dele); agentes
que já existem recebem o hook sem perder o resto. O Kiro não entende "perguntar": quando o Iron Brake
precisaria perguntar, ele mostra a própria janela (macOS) e, sem janela, bloqueia. Limites em
[doc/known-issues.md](doc/known-issues.md) (seção 19).

No Antigravity, o `iron init` grava o conjunto `iron-brake` em `.agents/hooks.json` do projeto (o resto do
arquivo fica). O agente só roda o hook de uma pasta em que você confiou, e **ignora em silêncio um
`hooks.json` com JSON inválido**: o `iron doctor --agent=antigravity` confere isso. O Iron Brake nunca usa o
"ask" do Antigravity (com a aprovação automática do agente ligada ele não segura nada): pergunta na própria
janela e, sem janela, bloqueia. `iron watch --agent=antigravity` confere de fora se o hook está vendo os comandos. Limites na seção 20 do [doc/known-issues.md](doc/known-issues.md).

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

**Escopo**

- Só enxerga comandos de terminal (a ferramenta Bash do Claude Code; no Kiro, a ferramenta de shell).
  Arquivos editados, chamadas de MCP e APIs não passam por ele.
- Não substitui permissões mínimas. Se o agente usa uma credencial que pode
  apagar o banco de produção, o banco está em risco: a credencial do agente
  precisa **não conseguir** fazer o que ele não deve. O Iron Brake é uma
  camada a mais, não a principal.

**O que escapa da análise**

Comandos são desmontados e analisados através de `sh -c`, `eval`, prefixos
(`env`, `sudo`, `nohup`, `timeout`, `xargs`...), aspas, `$'\x2d'`, subshells
e encadeamentos (`&&`, `;`, `|`). Variáveis definidas na mesma linha
(`F=--force; git push $F`) são resolvidas, e o conteúdo de scripts, Makefiles
e programas em outras linguagens que o comando executa (`./deploy.sh`,
`make deploy`, `python3 deploy.py`, `python3 -c "..."`) é lido e julgado.
Ainda assim, exigindo esforço deliberado, escapam: variável definida fora da
linha ou saída de comando (`git push "$(echo --force)"`), aliases e funções do
shell, comando montado em variável dentro de um script e comando rodado numa
máquina remota via `ssh`. Lista completa na seção 1 e 2 de
[known-issues.md](doc/known-issues.md).

**Falsos positivos conhecidos**

- `terraform plan -out=x && terraform apply x` na mesma linha é bloqueado — o
  plano ainda não existe no disco quando o hook analisa. Separe em dois
  comandos.
- `cd pasta && terraform apply tfplan` é bloqueado, porque não há garantia de
  qual pasta o `cd` deixa como atual. Use `terraform -chdir=pasta apply
  tfplan`.

**Qual `terraform` o Iron Brake executa**

Para ler o plano, o Iron Brake executa `terraform show -json` (ou `tofu`,
`terragrunt`). Ele recusa o programa do `PATH` que estiver dentro do projeto ou
que qualquer usuário possa alterar. Para fixar o programa, crie
`~/.iron/config.yaml` (do usuário, nunca do projeto):

```yaml
tools:
  terraform: /opt/homebrew/bin/terraform
  # tofu: /usr/local/bin/tofu
  # terragrunt: /opt/homebrew/bin/terragrunt
```

**Detecção de produção**

A detecção é por palavra (`prod`, `production`, `prd`) e pelos padrões
declarados em `production_patterns`. Um ambiente sem esse nome no comando, na
pasta, no workspace do terraform ou no `AWS_PROFILE` não é reconhecido —
declare os nomes reais do seu ambiente, ou ligue `assume_production: true`
para que tudo que não tenha nome de teste (`dev`, `staging`, `qa`...) valha
como produção. Variáveis de ambiente e kubeconfig são
lidos do processo do hook (herdados do Claude Code); algo definido antes, fora
da mesma linha do comando, não é visto.

**Não é defesa contra quem tenta burlar**

A proteção é contra erros e loops de um agente bem-intencionado, não contra
alguém — agente ou pessoa — que tenta contornar deliberadamente com o mesmo
usuário do sistema: é possível apagar o estado da sessão, reescrever o log de
auditoria recalculando a corrente de hashes, ou editar o `.iron/policy.yaml`
(o Iron Brake só vê comandos Bash, não a ferramenta de edição de arquivos).

**Plataforma**

A janela de confirmação com o motivo do bloqueio só existe no macOS. No Linux
e no Windows, a pergunta aparece pelo Claude Code, mas sem mostrar o porquê
antes da decisão. O Windows foi validado numa máquina real (Windows 11 ARM64,
2026-10-02): os testes passam, e `iron init`, `iron doctor` e `iron hook`
funcionam com o `iron.exe` compilado lá. Ainda não foi testado com o Claude Code
instalado no Windows (só com eventos de hook simulados). Detalhes em
[known-issues.md](doc/known-issues.md#13-limites-da-distribuição).

**Latência**

Comandos comuns respondem em poucos milissegundos (p95 abaixo de 5 ms, dez
vezes abaixo da meta de 50 ms). A exceção é `terraform apply` com plano
salvo: ler o plano para o cartão de risco leva ~130 ms (p95), porque o
próprio terraform inicia um processo por provedor para isso — irrelevante
perto do tempo do apply em si, mas mensurável. Medição completa em
[doc/latency.md](doc/latency.md).

A lista completa de limites, com cada caso verificado, está em
[doc/known-issues.md](doc/known-issues.md); o que cada regra faz, em
[doc/spec.md](doc/spec.md).

## Compilar do código

```bash
make test     # testes
make build    # bin/iron para esta máquina
make dist     # dist/: 6 binários (Linux, macOS, Windows × amd64, arm64) + SHA256SUMS
```

Para contribuir (commits, como propor uma regra, como escrever testes que
passem no Windows), veja o [CONTRIBUTING.md](CONTRIBUTING.md).

## Licença

[Apache 2.0](LICENSE).
