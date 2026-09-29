# Problemas conhecidos

Tudo marcado como **verificado** foi reproduzido com o binário (`bin/iron
hook`, evento montado à mão). Para reproduzir:

```bash
check() { jq -nc --arg c "$1" --arg d "${2:-$PWD}" \
  '{tool_name:"Bash",cwd:$d,tool_input:{command:$c}}' | bin/iron hook; echo "exit=$?"; }
check 'git push --force'    # exit=2
```

## 1. Comandos que escapam das regras (verificado em 2026-09-28)

O `splitCommands` segue o bash de perto: aspas, `\` + quebra de linha,
`$'...'`, `$(...)` e crases (inclusive entre aspas duplas), subshells `( )`,
grupos `{ }`, `if/then/do/!`, atribuições `VAR=x`, prefixos (`sudo`, `env`,
`time`, `nohup`, `timeout`, `nice`, `xargs`, `command`, `exec`...) e scripts
em linha (`sh -c`, `bash -c`, `eval`, `watch`), com limite de profundidade.
Nomes de programa não diferenciam maiúsculas (`GIT`, `Terraform`) e ignoram
`.exe`.

O que **ainda** sai com código 0:

| Comando | Por que escapa |
|---|---|
| `git push $F` com `F=--force` definida em **outra** chamada do shell | o valor só existe quando o shell roda. Variável definida na mesma linha (`F=--force; git push $F`, `export`) é resolvida. Um `git push` com variável ou saída de comando que a linha não define (`$F`, `$OPTS`, `"$(echo --force)"`) é **ask**, salvo variável com nome de branch/remoto/tag (`$BRANCH`, `$REMOTE`) ou `$(git branch --show-current)`. Um comando cujo programa é variável (`$RM -rf x`) também é ask. O que escapa: uma variável com nome de branch que guarda `--force` de propósito |
| `alias p="git push --force"; p` | alias e funções do shell |
| `git -c remote.origin.push=+refs/heads/main push` | o force vem de uma configuração, não do comando |
| script que monta o comando em variável, `make` com receita gerada por variável (`$(CMD)`), script fora do limite (3 níveis) ou maior que 4 MB | o conteúdo de `./x.sh`, `bash x.sh`, `source x.sh`, `python3 x.py`, `node x.js`, `make ALVO` (o Makefile e as dependências do alvo) **é lido** e julgado pelas mesmas regras; variável dentro do script só é vista em um caso: `rm -rf` numa variável sozinha (`$1`, `$DIR/*`) que o script não define e não protege com `set -u` ou `${VAR:?}` é ask |
| `os.system(cmd)` com `cmd` montado em variável | o código na linha (`python3 -c`, `node -e`...) e em arquivo (`python3 x.py`) é lido: os textos passados a `os.system`, `subprocess`, `execSync`... são julgados como comandos, mas variável não |
| `ssh prod-host "terraform destroy"` | o comando roda em outra máquina (não é analisado) |

Todos exigem esforço deliberado; a proteção é contra erros e loops, não
contra um agente que tenta burlar (seção 10).

## 2. Falsos negativos conhecidos das regras (verificado)

A cobertura foi ampliada em 2026-09-28. **Passaram a ser tratados** (deny em
produção, ask fora, salvo os catastróficos que são deny sempre): filesystem
(`rm -rf /`, `dd`, `mkfs`, `wipefs`, `shred`, `> /dev/...`, `chmod/chown -R` em
sistema, `find -delete`), `curl|sh` e `curl -X DELETE`, contêineres (docker/
podman prune, `volume rm`, `rm -f`, `compose down -v`), sistema (`shutdown`,
`systemctl stop`, `crontab -r`, firewall), publicação de pacote (`npm publish`
etc.), `terraform state rm`/`mv`/`push`/`taint`/`workspace delete`/`force-unlock`/`import` (e `tofu` e
`terragrunt`, inclusive `run-all`, `run --all --`, `apply-all` e `destroy-all`),
git (`branch -D`, `tag -d`, `reflog expire`, `gc --prune=now`, `filter-branch`,
`restore`/`checkout` de descarte, `push :branch`/`--delete` — branch protegida é
deny), kubectl (`delete pvc/pv/crd`, `delete -A`/`--all-namespaces`, `scale --replicas=0`,
`replace --force`), helm
(`uninstall`, `rollback`), nuvem em massa (`s3 rm --recursive`, `s3 rb --force`,
`gcloud/gsutil storage rm`, `az ... delete-*`/`purge`), SQL (`DROP SCHEMA`, `DELETE ... WHERE 1=1`, `WHERE TRUE`, `WHERE id=1 OR 1=1` e outros
`WHERE` sempre verdadeiros).

Também passou a **ler o arquivo apontado** pelo comando: `kubectl delete -f x.yaml`
(procura `kind: Namespace`/`PersistentVolumeClaim`/`PersistentVolume`/
`CustomResourceDefinition`, em YAML ou JSON, inclusive dentro de `kind: List`) e
`psql -f drop.sql` / `mysql < drop.sql` (roda a análise de SQL no conteúdo).
Alvo que não dá para ler (URL remota, stdin `-`, kustomize `-k`, arquivo
inexistente): allow fora de produção, ask em produção.

O que **ainda** sai com **código 0**:

| Categoria | Comando | Por que passa |
|---|---|---|
| kubernetes | `kubectl delete deploy NOME` e `kubectl delete -f` com `kind: Deployment`/`StatefulSet`/`DaemonSet`/`Service`/`Ingress` **fora de produção**; `kubectl delete pod`, `configmap`, `secret`, `job` em qualquer ambiente | de propósito: não perdem dados (um `apply` recria) e são o dia a dia de um deploy. **Em produção** os cinco tipos acima são **ask** (nunca deny), porque tiram o serviço do ar. Pod fica de fora: apagar pod é como se reinicia. Só Namespace, PVC, PV e CRD (apagam dados ou em cascata) são bloqueados também fora de produção |
| SQL | `DELETE ... WHERE` cuja condição só é verdadeira para toda linha por causa dos dados (`WHERE age > 0`, `WHERE coalesce(x, 0) >= 0`); e, **fora de produção**, `WHERE deleted_at IS NOT NULL` / `status <> 'x'` (em produção, um filtro assim sozinho é ask) | são reconhecidos os literais e igualdades óbvias (`1=1`, `TRUE`, `id=id`, `1<>0`), `coluna >= 0` e `> -1` em qualquer coluna, filtros em colunas-chave (`id`, `uuid`, `pk`, `*_id`: `> 0`, `IS NOT NULL`, `LIKE '%'`, `< 2147483647`) e `LENGTH/ABS(...) >= 0`. Sem o esquema do banco não dá para saber quantas linhas um filtro amplo pega. Como `deleted_at IS NOT NULL` é uma limpeza comum, só pergunta em produção, e só quando é o único filtro (com `AND created_at < ...` ou `id = 5` junto, passa) |
| arquivos | `rm -rf` dentro da pasta do projeto, mesmo que o nome tenha `prod`; apagar por outros meios (`unlink`, `find -delete` com filtro) | o `rm -rf` fora da pasta do projeto e das pastas temporárias (`/tmp`, `/var/folders`, `$TMPDIR`) é ask, e deny em produção (`/data/prod` já conta como produção pelo nome). `rm -rf $DIR/x` com variável que a linha não define é ask |
| nuvem | exclusão via SDK montada em variável ou vinda de outro arquivo que o script importa; API chamada por outro cliente que não seja `curl`/`wget` | é lido o código escrito na linha (`python3 -c`, heredoc, pipe) e o arquivo executado (`python3 deploy.py`): `.delete_*`, `.terminate_*`, `requests.delete(`, `method: 'DELETE'`, `shutil.rmtree`, `fs.rmSync`, `DROP`/`DELETE` sem filtro dentro de textos. `curl`/`wget` com `-X DELETE`, `Action=Delete*/Terminate*`, `X-Amz-Target: ...Delete*` ou `X-HTTP-Method-Override: DELETE` |

O `terragrunt apply` segue a mesma regra do terraform ("sem plano, sem apply"): o
plano é lido com `terragrunt show -json PLANO` (também `run-all` e `run --all --`,
somando os planos dos módulos) e o cartão de risco vale igual. **Ainda não foi
testado com o terragrunt de verdade** (só com um programa falso no lugar dele, nos
testes): confirme com um `plan -out` real antes de confiar. No `run-all`, os
módulos não aparecem no comando, então a detecção de produção só vale pelo
contexto (perfil, workspace, `--working-dir`).

Qualquer regra também escapa por variável no lugar do comando e por scripts
(seção 1). Prefixos (`sudo`, `env`, `sh -c`, `eval`...) **não** escapam: são
desmontados (seção 12).

## 3. O motivo do ask não aparece a tempo (limitação do Claude Code)

Na extensão do VS Code:

- a caixa de confirmação **não mostra** o `permissionDecisionReason` (a
  documentação diz que é "shown to the user");
- o `systemMessage` só aparece **depois** que o comando roda, como linhas
  `PreToolUse:Bash says: ...`;
- se você recusa, **nada** é mostrado.

Pesquisado na documentação de hooks: `defer` só funciona com `claude -p`;
`PermissionRequest` decide mas não mostra nada; `terminalSequence` é só
notificação de terminal. Nenhum resolve.

**Contorno atual:** todo ask abre a janela nativa do macOS com o motivo.
**Continua sem solução fora do macOS** (Linux, Windows, SSH, nuvem): o
fallback é o ask do Claude Code, com o motivo invisível na hora de decidir.

## 4. Linha com plan e apply juntos é bloqueada (verificado)

`terraform plan -out=novo.tfplan && terraform apply novo.tfplan` → deny
("não consegui ler o plano salvo"), porque o plano ainda não existe quando o
hook roda. É seguro, mas obriga a separar em dois comandos. A mensagem orienta
isso.

## 5. `cd` na mesma linha é bloqueado

`cd infra && terraform apply tfplan` → deny, porque o Iron Brake não sabe com
certeza qual arquivo será aplicado (`cd ~`, `cd -`, `cd $VAR`...). Só `cd`,
`pushd` e `popd` são detectados; `(cd x; ...)` e outras formas de trocar de
pasta não são. Alternativa aceita: `terraform -chdir=infra apply tfplan`.

## 6. Depois de aprovar na janela, o Claude Code podia perguntar de novo (corrigido e verificado)

Corrigido: ao aprovar na janela, o Iron Brake responde `permissionDecision:
"allow"` explícito. Exceção proposital: se a linha tiver outros comandos
(`terraform apply tfplan && rm -rf x`), fica "sem opinião", para o resto da
linha não passar de carona — e aí o Claude Code ainda pode perguntar.

## 7. Riscos residuais da janela nativa

- **Timeout configurado abaixo de ~530 s:** se alguém definir `"timeout"`
  menor no hook do `settings.json`, o Claude Code pode matar o hook enquanto a
  janela espera — e hook que estoura o tempo **não bloqueia**: o comando
  passa. `iron init` não define timeout (vale o padrão de 600 s). O `iron
  doctor` ainda não verifica isso.
- **Janela atrás de outras:** não confirmado se ela sempre vem para a frente.
- **Agente com controle do computador** (computer use, permissão de
  Acessibilidade) poderia, em tese, clicar em Executar.

## 8. Teste intermitente do doctor (corrigido)

`go test -count=1 ./...` falhava às vezes em `internal/doctor` com "o hook
não respondeu em 1s": com a máquina ocupada, iniciar o script `sh` falso
passava de 1 s. Corrigido: os casos normais usam 10 s, e só o caso que testa
travamento usa 1 s. Depois disso, 5 rodadas completas sem falha.

## 9. Limites da detecção de produção

- **Só por nomes.** Se o cluster de produção se chama `cluster-a` e a conta
  AWS `empresa`, nada indica produção: tudo vira ask. Declare os nomes em
  `production_patterns`, ou ligue `assume_production: true` no
  `.iron/policy.yaml` (modo estrito, abaixo).
- **Modo estrito (`assume_production: true`):** o cluster do kubectl, o
  `AWS_PROFILE`, o workspace do terraform e o projeto do gcloud precisam ter um
  nome de teste reconhecido (`dev`, `staging`, `test`, `qa`, `sandbox`,
  `local`, `minikube`, `docker-desktop`, `kind`, `demo`, `hml`... a lista é fixa
  no código, para o arquivo não poder afrouxá-la); se **algum** não tiver,
  vale como produção (`cluster-a` vira deny). Sem nenhum contexto de nuvem
  (`git push`), não muda nada. Custo: `default` e nomes como `empresa` viram
  produção, então quem liga o modo precisa nomear os ambientes de teste.
  Só soma, como o resto do arquivo.
- **Contexto de onde o hook roda:** as variáveis de ambiente e o kubeconfig
  lidos são os do processo do hook (herdados do Claude Code). Um
  `export AWS_PROFILE=prod` feito antes, no shell do agente, não é visto — a
  não ser que esteja na mesma linha do comando.
- **Workspace do terraform (corrigido):** é lido na pasta atual e também na
  de `terraform -chdir=DIR` (e `tofu`) e na de `cd DIR && terraform ...`, com
  `TF_DATA_DIR` respeitado (relativo, vale a partir da pasta do comando).
  Limites: um `cd` que não dá para resolver (`cd ~`, `cd -`, `cd $VAR`,
  `popd`) volta ao que já se sabia; um `(cd x; ...)` conta como `cd x` para o
  resto da linha (pode dar falso positivo, nunca falso negativo); `TF_DATA_DIR=x`
  escrito na mesma linha, antes do comando, não é visto; e o terragrunt não é
  coberto (o workspace dele fica no cache do módulo).
- **O agente pode editar o `.iron/policy.yaml`** com a ferramenta Write (o
  Iron Brake só vê Bash). Como o arquivo só soma, ele não consegue remover os
  padrões embutidos; mas pode adicionar padrões que só geram mais bloqueios,
  ou deixar o arquivo inválido (o que também só gera mais bloqueios).
- **Falso positivo por palavra:** uma pasta ou recurso chamado `prod` que não
  é produção vira deny. Como o arquivo só soma, não dá para remover `prod`.

## 10. Limites da memória da sessão

- **Conta tentativas, não execuções.** O `PreToolUse` roda antes do comando:
  um apply recusado na janela, ou que falhou, conta igual. Contar só o que
  rodou exigiria um hook `PostToolUse`.
- **O agente pode apagar o estado** (`rm -rf ~/Library/Caches/ironbrake`) e
  zerar os contadores. A proteção é contra loops por engano, não contra um
  agente que tenta burlar de propósito.
- **Evento sem `session_id`:** a memória não roda (não há onde guardar).
- **Windows não testado:** a trava lá usa o arquivo aberto sem
  compartilhamento; compila, mas nunca rodou num Windows.
- **"Muito parecido" ignora números, nomes de arquivo e ordem das opções:**
  além de espaços, aspas, barras invertidas e maiúsculas, cada sequência de
  dígitos vale igual; o argumento do `terraform apply` (o plano) vale igual
  qualquer que seja o nome (`plan1`, `plan-a`, `outro.tfplan`); qualquer
  argumento que termine em `.tfplan`, `.yaml`, `.json`, `.sql`, `.tf`... vale
  igual (`kubectl delete -f a.yaml` e `b.yaml`, `-out=a.tfplan` e `b.tfplan`); e
  as opções são comparadas em ordem alfabética (`-auto-approve -input=false`
  = `-input=false -auto-approve`). Efeito colateral: comandos legítimos que só
  mudam o número ou o arquivo também se somam (`kubectl delete pod web-1`,
  `web-2`, `web-3` em 5 minutos → ask). **Não vale** para nomes sem número em
  outros comandos (`kubectl delete pod web-a`, `web-b` seguem diferentes; de
  propósito, para não juntar recursos distintos) nem para opção com valor
  separado (`-n prod` e `-n dev` continuam diferentes).
- **Hash de segredo curto:** o hash com sal impede tabelas prontas e liga o
  hash a uma sessão só, mas quem tem o arquivo (que já é `0600`) e sabe o
  formato do comando pode testar senhas fracas uma a uma.
- **Concorrência testada com goroutines** (cada uma abre o arquivo por conta
  própria, o que o `flock` trata como processos diferentes), não com vários
  processos `iron` de verdade.

## 11. Limites do log de auditoria

- **Apagar o arquivo inteiro ou as últimas linhas não é detectado**: nenhuma
  linha depois delas guardava o hash delas. Demonstrado em
  `TestVerifyCannotDetectTruncatedEnd`.
- **Alterar a última linha não é detectado**, pelo mesmo motivo.
- **Quem altera uma linha e recalcula os `prev` seguintes deixa a corrente
  íntegra de novo.** A corrente pega edição descuidada, não quem conhece o
  formato (o agente roda com o seu usuário e pode escrever no arquivo).
  Demonstrado em `TestVerifyCannotDetectRewrittenChain`.
- Os três só se resolvem com um registro **fora da máquina** (fase 2: um
  servidor que recebe periodicamente o hash da última linha).
- **Falha ao gravar (corrigido):** o aviso continua indo para o stderr, que o
  Claude Code só mostra no deny, mas agora o hook também grava um marcador
  (`audit.log.error`, com hora e motivo) e o `iron doctor` (verificação 5)
  falha enquanto a última gravação tiver falhado, ou se o log não puder ser
  gravado, ou se a corrente estiver quebrada. Limite: se a própria pasta
  `~/.iron` não aceita gravação, o marcador também não entra; aí quem pega é a
  checagem de escrita do doctor, que só roda quando você roda o doctor.
- **Rotação (corrigido):** passando de 5 MB o log vira `audit.log.1` e ficam 5
  antigos (no máximo ~30 MB, cerca de 30 mil decisões por arquivo). A
  corrente continua entre os arquivos e `iron audit verify` confere todos.
  Tamanho e quantidade podem ser aumentados no `audit_log` do
  `.iron/policy.yaml` (de 5 a 100, nunca abaixo do padrão). **O log é um só por
  usuário e o `policy.yaml` é por projeto:** quem gira o log é o hook do
  projeto onde a decisão acontece, e usa a configuração dele; um projeto sem
  `audit_log` (padrão 5) poda o que outro projeto configurou para guardar mais.
  Para o valor valer sempre, ponha o mesmo `audit_log` em todos os projetos.
  Efeito: depois de muitas decisões, as mais antigas **somem** do disco
  (o que sai é o arquivo mais antigo inteiro). Quem precisa guardar tudo deve
  copiar `audit.log.*` antes; o `.anchor` mantém a corrente conferível, mas
  não é uma prova externa (mesma limitação das outras linhas desta seção).
- **Endereços do terraform** podem conter dados em chaves de `for_each`
  (ex.: `aws_iam_user.u["ana@empresa.com"]`).
- **`session` é o `session_id` do Claude Code**, gravado como veio.

## 12. Revisão de segurança de 2026-09-28 (corrigido)

Falhas encontradas lendo todo o código e confirmadas com o binário (todas
saíam com código 0 em produção); cada uma tem teste:

| Falha | Correção |
|---|---|
| `\` + quebra de linha deslocava os argumentos (`kubectl delete \⏎ namespace prod`) | continuação de linha como no bash |
| `do`, `then`, `if`, `!`, `{`, `(`, `time`, `sudo`, `env`, `VAR=x`, `nohup`, `timeout`, `xargs`... escondiam o programa | desembrulho dos prefixos e palavras do shell |
| `$(...)` e crases (soltos ou entre aspas duplas) não eram analisados | o conteúdo é analisado como comando |
| `sh -c`, `bash -c`, `eval`, `watch` | o script é analisado (até 8 níveis) |
| `$'--force'` e `$'\x2d\x2dforce'` | aspas ANSI-C traduzidas |
| `GIT push`, `Terraform destroy`, `kubectl.exe` | nome do programa sem diferenciar maiúsculas e sem `.exe` |
| kubectl/aws: opção com valor fora da lista deslocava as posições | kubectl procura o verbo; aws trata toda opção longa como valorada, menos as booleanas |
| SQL: `-- WHERE ...`, `/* */`, `WITH ... DELETE`, `EXPLAIN ANALYZE DELETE`; clientes `pgcli`, `mycli`, `duckdb`... | comentários removidos antes; CTE e EXPLAIN tratados; mais clientes |
| `git push --mirror` | bloqueado como force push |
| a janela usava o `osascript` do PATH: um falso aprovaria tudo | `/usr/bin/osascript` |
| FIFO ou link para `/dev/zero` no `policy.yaml`, `.terraform/environment`, kubeconfig ou estado da sessão travava o hook, e o Claude Code liberava no timeout | leitura só de arquivo comum com limite de tamanho (`internal/safefile`) e prazo total de 560 s que responde deny |
| release: tag com crase executava comando no `make dist` | versão vai ao shell como variável de ambiente; o fluxo valida `vX.Y.Z` |
| cartão com centenas de recursos | lista limitada a 20 + "e mais N" |

Decisões tomadas depois da revisão:

| Ponto | Decisão |
|---|---|
| **Risco:** um `terraform` falso num diretório do `PATH` muda o resultado do `terraform show` (o Iron Brake leria um plano inventado) | **mitigado:** o caminho absoluto vai em `tools.terraform` (`tofu`, `terragrunt`) no `~/.iron/config.yaml` e o Iron Brake executa exatamente esse. Sem configuração, ele recusa o programa do `PATH` que estiver **dentro do projeto** ou que **qualquer usuário possa alterar** (arquivo ou pasta gravável por todos). O `iron doctor` (verificação 5) mostra o que será usado. Fica de fora: um falso num diretório do `PATH` só seu, que o agente escreve com o seu usuário |
| O caminho do terraform não pode ficar no `.iron/policy.yaml` | de propósito: o hook executa o programa indicado, e o `policy.yaml` vem do repositório (ou de um agente que o edita); um projeto clonado rodaria código na sua máquina. Por isso a configuração é do usuário, em `~/.iron/config.yaml` |
| Chaves de `for_each` vão no motivo enviado ao Claude e poderiam carregar texto de prompt injection | **corrigido:** o `tfplan.Summarize` saneia os endereços (chave com caractere fora de `A-Z a-z 0-9 _ . : / @ + -` ou com mais de 40 caracteres vira `["…"]`; caracteres de controle viram `?`; até 200 caracteres). Vale para o cartão, a janela e o log |
| `timeout` do hook menor que o prazo do Iron Brake anula o deny por tempo | **corrigido:** o `iron doctor` confere (verificação 4/5): ausente (padrão 600 s) ou pelo menos 570 s |

## 13. Limites da distribuição

- **Windows compila, mas não foi testado**; o `install.sh` não roda no
  Windows (instalação manual pelo `.exe`). Nomes como `terraform.exe` na
  linha de comando não são reconhecidos pelas regras.
- **O `SHA256SUMS` vem da mesma release que o binário:** pega download
  corrompido, não release adulterada. A conferência contra adulteração é o
  atestado de proveniência (`gh attestation verify`, ou
  `IRON_VERIFY_ATTESTATION=1` no instalador; ver [release.md](release.md)).
  **Verificado em 2026-09-29 (`v0.1.1`):** o `gh attestation verify` (gh 2.101.0,
  com `--signer-workflow` e `--source-ref refs/tags/v0.1.1`) passou para os 6
  binários, o `install.sh` e o `SHA256SUMS`; o `shasum -c` também. O
  `IRON_VERIFY_ATTESTATION=1` do instalador foi testado no macOS arm64, em
  diretório temporário: instalou a release real; recusou, sem instalar nada,
  um binário adulterado cujo `SHA256SUMS` foi ajustado junto (o hash passa, o
  atestado não); e, **sem** a variável, instalou esse mesmo binário adulterado
  (é o limite descrito acima). Não testado: o instalador no Linux e o caso de
  `gh` ausente. O instalador **não** exige o atestado por padrão (o `gh` não
  vem instalado). O `gh` precisa de login por OAuth: um fine-grained PAT com
  validade acima de 366 dias é recusado (HTTP 403) por organizações que
  limitam isso.
- **Fluxo de release verificado em 2026-09-28:** a tag `v0.1.0` rodou o
  `.github/workflows/release.yml` de ponta a ponta — `test` e `release`
  concluíram com sucesso, com os 6 binários, o `install.sh` e o
  `SHA256SUMS`. Os testes rodaram em Linux pela primeira vez (antes só tinha
  sido compilação cruzada) e passaram.

## 14. Outros limites

- **Plano trocado entre a leitura e o apply:** se outro processo reescrever o
  arquivo do plano depois que o hook o leu, o terraform aplica o novo. Risco
  baixo.
- **`terraform show` precisa de `terraform init`** na pasta; sem isso, o plano
  não é lido e o resultado é deny.
- **Backends remotos (Terraform Cloud/HCP):** não testado.
- **Endereços no cartão:** chaves de `for_each` aparecem no endereço (ex.:
  `aws_iam_user.u["ana@empresa.com"]`). Não são atributos, mas podem conter
  dados que você preferiria não ver na tela.

## 15. Pendências fora do binário (roadmap do plano, não são bugs)

Diferente das seções acima, isto não foi "verificado com o binário" — é
trabalho que o plano original prevê e ainda não começou.

**v0.5 (depois da validação, seção 11 do plano):**
- Estimativa de custo no cartão de risco (`terraform show` → Infracost ou
  OpenInfraQuote). O que o v0.5 do plano também listava aqui — regra de
  `curl`/`wget` com `-X DELETE` — **já está implementado** (seção 2 acima,
  `internal/rules/remotecode.go`).
- Guia por nuvem de como criar um perfil só-leitura para o agente.

**Validação com usuário real (seção 11 do plano) — nada disso começou:**
- 10 conversas de 20 min com desenvolvedores/tech leads sobre a dor real
  (agente com credencial de nuvem, o que já deu errado).
- 3 a 5 testadores usando por duas semanas, com as métricas da seção 11
  (tempo até o primeiro bloqueio, bloqueios indevidos por semana, bloqueios
  que evitariam dano, times que pedem política central/Slack).
- Decisão registrada (seguir para o plano de controle, pivotar ou parar) só
  depois disso — não antes.
