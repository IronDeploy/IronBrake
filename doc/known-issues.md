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
| script que monta o comando em variável, `make` com receita gerada por variável (`$(CMD)`), script além do limite (só 2 scripts encadeados são lidos: o que o comando chama e o que esse chama; o terceiro da cadeia não é aberto) ou maior que 4 MB | o conteúdo de `./x.sh`, `bash x.sh`, `source x.sh`, `python3 x.py`, `node x.js`, `make ALVO` (o Makefile e as dependências do alvo) **é lido** e julgado pelas mesmas regras; variável dentro do script só é vista em um caso: `rm -rf` numa variável sozinha (`$1`, `$DIR/*`) que o script não define e não protege com `set -u` ou `${VAR:?}` é ask |
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
`gcloud/gsutil storage rm`, `az ... delete-*`/`purge`), IaC da AWS e vizinhos
(`cdk destroy`, `cdk deploy --require-approval never`, `sam delete`, `eksctl delete`,
`pulumi destroy`/`up --yes`/`stack rm --force`/`state delete`, `serverless remove`,
`cdktf destroy`; também via `npx`/`pnpx`/`bunx`/`pnpm dlx`/`yarn dlx`/`npm exec` e pelo nome do
pacote, como `aws-cdk@2`; só verificado com o binário e eventos
montados à mão, **não** com as ferramentas de verdade), SQL (`DROP SCHEMA`, `DELETE ... WHERE 1=1`, `WHERE TRUE`, `WHERE id=1 OR 1=1` e outros
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

**Contorno atual:** todo ask abre a janela nativa com o motivo, no macOS
(`osascript`), no Windows (PowerShell, Windows Forms) e no Linux (`zenity`).
**Continua sem solução em sessões sem tela** (SSH, nuvem) e no Linux sem o
`zenity`: o fallback é o ask do Claude Code, com o motivo invisível na hora de
decidir. No Windows, sem desktop interativo (SSH, serviço) a janela não abre e a
resposta é `Unavailable`, o mesmo fallback.

**Windows, verificado em 2026-10-02** (Windows 11 ARM64, sessão gráfica): a
janela aparece com o cartão, aspas e acentuação intactas; **Executar** aprova;
**Cancelar** é o botão padrão, então Enter, Esc, o X e o tempo esgotado
recusam. Ponta a ponta com o `iron hook` real: Executar devolve `allow`
explícito ("aprovado pelo usuário"), Cancelar devolve a recusa para o agente.
**Com o Claude Code 2.1.288 real** (mesma máquina): Executar fez o comando rodar e
Cancelar o impediu, com `userApproved`/`rejected` no log (tabela na seção 13).
Limites: o PowerShell leva alguns segundos para abrir (o tempo de espera conta a
partir da janela); janela em tela cheia, vários monitores e o desktop seguro do
UAC não foram testados.

**Linux, verificado em 2026-10-02** (Docker arm64, Debian trixie, `zenity`
4.1.90, Xvfb + openbox, cliques com `xdotool`; roteiro em
`scripts/test-dialog-linux.sh`): **Executar** aprova; **Cancelar**, Enter (o
padrão), Esc, fechar a janela e o tempo esgotado recusam; sem `DISPLAY` nem
`WAYLAND_DISPLAY` a resposta é `Unavailable` e o programa nem é executado. Texto
hostil (`$(...)`, crase, `<b>`, `--ok-label=...`) aparece como texto puro e não
executa nada. Só o código de saída 0 do `zenity` aprova.
Limites:
- **`kdialog` não é usado.** Medido: o botão padrão dele é sempre o afirmativo
  (Enter aprova) e não há opção para mudar; inverter os rótulos faria a
  aprovação depender do código 1, que também é o código de erro dele. Um KDE
  sem `zenity` cai no fallback (ask do Claude Code).
- O programa é o `/usr/bin/zenity` (caminho absoluto, contra um `zenity` falso no
  PATH): distribuições sem `/usr/bin/zenity` (NixOS, `zenity` de Flatpak ou Snap)
  caem no fallback.
- O texto é cortado em 24 linhas e 1600 caracteres (marcado com …): sem isso o
  `zenity` cresce além da tela e os botões somem. Num cartão enorme, o fim da
  lista (inclusive "... e mais N") pode não aparecer.
- Sem display de verdade o `zenity` sai com 1, o mesmo código do Cancelar; o
  Iron Brake distingue pelo "display" no stderr do GTK. Se essa mensagem mudar,
  o resultado é uma recusa (deny), nunca uma aprovação.
- **Não verificado:** GNOME, KDE ou Wayland de verdade (só Xvfb), o `zenity` 3.x
  abrindo a janela (só as opções foram conferidas), vários monitores, um clique
  humano (os cliques foram do `xdotool`) e o Claude Code no Linux.

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

- **Timeout configurado abaixo de 570 s:** se alguém definir `"timeout"`
  menor no hook do `settings.json`, o Claude Code pode matar o hook enquanto a
  janela espera — e hook que estoura o tempo **não bloqueia**: o comando
  passa. `iron init` não define timeout (vale o padrão de 600 s). O `iron
  doctor` confere isso (verificação 4) e falha se o valor for menor.
- **Janela atrás de outras (verificado em 2026-10-01, macOS 26, 3 de 3
  tentativas):** o diálogo do `osascript` abre na camada 8 (painel modal),
  acima de todas as janelas normais, mesmo com o VS Code em foco. Colocar
  `activate` no script **piora**: o diálogo nem apareceu em 1,5 s. Não testado:
  app em tela cheia (outro Space) e vários monitores.
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
- **Windows:** a trava lá usa o arquivo aberto sem compartilhamento. **Verificado
  em 2026-10-02** (Windows 11 ARM64): os testes da trava e da sessão passam.
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
  (`audit.log.error`, com hora e motivo) e o `iron doctor` (verificação 6)
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
| `timeout` do hook menor que o prazo do Iron Brake anula o deny por tempo | **corrigido:** o `iron doctor` confere (verificação 4): ausente (padrão 600 s) ou pelo menos 570 s |

## 13. Limites da distribuição

- **Windows: validado numa máquina real, com limites.** **Verificado em
  2026-10-02** num Windows 11 ARM64 (build 26100, UTM no Apple Silicon), com Go
  1.27.1 nativo: `go vet`, `gofmt` e todos os testes passam; com o `iron.exe`
  compilado lá, `iron init` grava o hook (`command` + `args`, sem shell),
  `iron doctor` dá 9/9 e `iron hook` bloqueia force push, `kubectl delete
  namespace` em produção e `rm -rf` fora do projeto, e libera `rm -rf build`.
  - **Invólucros:** `cmd /c`, `cmd.exe /c`, `powershell -Command`, `pwsh -c` e
    `-EncodedCommand` (base64 UTF-16) são abertos e o comando de dentro é julgado
    como se estivesse solto. Antes, `cmd /c git push --force` passava.
  - **Remoção recursiva do Windows:** `rmdir /s`, `rd /s`, `del /s` e
    `Remove-Item -Recurse` (e `ri`) entram na regra de "fora da pasta do
    projeto". Caminhos com `\` só são lidos dentro de `cmd /c` e
    `powershell -Command`; fora deles o shell do agente (Git Bash) já trata `\`
    como escape. Letra de unidade (`C:/x`, `C:\x`) conta como caminho absoluto.
  - **Permissões:** o `terraform`/`tofu`/`terragrunt` só é executado se nem o
    arquivo nem a pasta forem graváveis por Todos, Usuários Autenticados ou
    Usuários (lê a ACL). O `.iron/audit.log` herda a ACL do perfil (só o dono,
    SYSTEM e Administradores).
  - **Claude Code 2.1.288 real no Windows: verificado em 2026-10-02.**
    Máquina: a mesma (Windows 11 ARM64), Claude Code nativo
    (`irm https://claude.ai/install.ps1 | iex`), modo auto, Git for Windows 2.55
    instalado (por isso a ferramenta `Bash` existe), `iron.exe` da `main`,
    `iron init` numa pasta de teste, `iron doctor` 9/9. Cada prompt pediu um
    comando exato à ferramenta indicada; o resultado foi conferido na tela e no
    `audit.log` (corrente íntegra):

    | Comando pedido ao agente | O que se viu | Log |
    |---|---|---|
    | `git push --force origin main` (Bash) | bloqueado antes de rodar; o motivo chegou ao modelo, que não tentou alternativa | `deny`, `git-force-push` |
    | `echo iron-ok` (Bash) | passou | `allow` |
    | `git reset --hard` (Bash), **Executar** | a janela abriu; o comando rodou (falhou: a pasta não era repositório) | `userApproved`, `dialog: approved` |
    | `git reset --hard` (Bash), **Cancelar** | o comando **não** rodou; o modelo recebeu "o usuário recusou… não tente de novo" | `deny`, `dialog: rejected` |
    | `Write-Output ps-ok` (**PowerShell**) | passou | **nenhuma linha**: o hook não viu |

    Isso fecha o exit 2 ponta a ponta no Windows (bloqueio e recusa na janela).
  - **Limite: a ferramenta `PowerShell` do Claude Code não passa pelo hook**
    (o `matcher` instalado é `Bash`). Medido: `Write-Output` pela ferramenta
    PowerShell rodou sem nenhuma linha no log. Com o Git for Windows instalado
    o agente tem as duas ferramentas; **sem o Git, só a PowerShell**, e o Iron
    Brake não vê nada. O que ele vê no Windows hoje é a ferramenta `Bash`. Falta
    capturar um evento real da ferramenta PowerShell para ampliar o `matcher`.
  - **Não verificado:** Kiro, Codex e Antigravity no Windows (só eventos
    simulados); o `terraform apply` destrutivo pelo agente real no Windows; o
    `iron.exe` x64 (a máquina era ARM64); a janela de confirmação num desktop Linux real (Windows e Linux em Docker foram verificados, ver seção 3). O `install.sh` não roda no
    Windows (instalação manual pelo `.exe`). O portão da AWS recusa funcionar lá.
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
  que evitariam dano).

## 16. Limites do iron watch

Detalhes em [watch.md](watch.md). Em resumo: só lê os transcripts do Claude Code;
o transcript é escrito pelo agente (não pega quem burla de propósito); só observa
(o comando já rodou quando o aviso chega); só chamadas de shell, sem subagentes;
confere por sessão, classe e hora (não pelo comando); decisão sem `session_id`
vale para qualquer sessão; precisa estar rodando para acompanhar ao vivo.
**Verificado em 2026-10-01** contra o transcript real, sem hook ativo; o caso
"com decisão" só foi exercitado em testes automatizados.

## 17. Limites do portão de credenciais da AWS

Detalhes em [aws.md](aws.md). Em resumo: o agente roda com o seu usuário e pode ler as
credenciais reais por outro caminho ou forjar a liberação (o arquivo é do seu usuário); processo
desligado do pai parece humano, a não ser com `--require-tty` (que não serve para CI); a
identificação é por nome de programa, de script ou de pasta de pacote (`claude`, `kiro-cli`, `gemini`, `codex`,
`cursor-agent`); sem `--label` no modo auto, o pedido vale como produção; decide
por pedido de credencial, não por comando; só macOS e Linux (no Windows recusa funcionar). **Verificado
em 2026-10-01** só com o AWS CLI e credenciais falsas; terraform, cdk, boto3, outros agentes, SSO e
Linux não foram testados. Custa ~30 ms por pedido.

## 18. Limites da etiqueta do agente e do alerta no CloudTrail

Detalhes em [aws.md](aws.md). A etiqueta (`AWS_SDK_UA_APP_ID=iron-claude` no `env` do `settings.json`)
é uma declaração do ambiente do agente: ele pode zerá-la, e só vale com a pasta confiada. Verificada só
com o AWS CLI v2 e o Claude Code; terraform, CDK, boto3 e os outros agentes não foram testados. O modelo
CloudFormation do alerta (`iron aws-alerts`) foi validado com `cfn-lint`, **não implantado**; exige um
trail do CloudTrail com logging ativo, uma pilha por região, e só pega eventos de escrita.

**Corrigido nesta rodada (verificado):** o `iron init` chamado por caminho relativo (`../bin/iron`)
gravava o hook com `..` no caminho e, numa segunda execução, instalava um **segundo** hook em vez de
reconhecer o primeiro (cada comando passava duas vezes pelo Iron Brake).

## 19. Limites do Kiro CLI

Verificado com o Kiro CLI **2.26.1** (macOS), em 2026-10-01, com o agente real e o `iron` real: bloqueio de
`git push --force` e liberação de comando seguro no v2 (interativo e não interativo) e no v3 interativo;
`ask` resolvido pela janela do Iron Brake (Executar → roda, Cancelar → bloqueia); prazo de 600 s
respeitado; `agent: kiro` no log. Em **Linux** (Docker, usuário não root) foi verificado o binário `iron` real: `init`, `doctor`, hook com os
dois payloads, `ask` sem janela virando bloqueio e corrente do log íntegra; o **Kiro de verdade não foi rodado em Linux**. Não verificado: Windows, outras versões do Kiro.

- **v3 com `--no-interactive` não executa hooks** (o comando passa sem o Iron Brake, sem erro). Reproduzido
  aqui para o `preToolUse`; é a issue [kirodotdev/Kiro#11281](https://github.com/kirodotdev/Kiro/issues/11281). O
  `iron doctor --agent=kiro` só avisa; use `iron watch`, o portão da AWS e o CloudTrail onde couber.
- **Kiro IDE: sem proteção** (o hook do IDE não recebe o comando; issues #7500 e #7375, não reconferidas).
- **Só a saída 2 bloqueia.** JSON de resposta (`permissionDecision`) é ignorado, e exit 1 só avisa e libera.
  Estourar o prazo libera o comando; sem o campo de prazo o v2 libera após ~10 s, por isso o `iron init` grava
  `timeout_ms: 600000` (v2) e `timeout: 600` (v3) e o doctor falha se faltar.
- **Dois formatos, dois arquivos.** O v2 só lê o hook de dentro do agente e ignora `.kiro/hooks`; o v3 lê o
  `.kiro/hooks` e **não carrega** agente em formato v2 ("needs upgrading"). Se você converter um agente para o
  v3 (auto-upgrade do Kiro), o hook passa a rodar **duas vezes** por comando no v3 (conta em dobro na memória
  de sessão); o doctor avisa.
- **O `kiro_default.json` do projeto substitui o agente padrão** do Kiro nessa pasta (sem o prompt longo dele).
  Agentes que já existem são só acrescidos do hook.
- **`matcher`:** no v2 é `shell`; no v3 é uma regex (`^(execute_bash|shell|execute_cmd)$`). O `*` do v2 não
  compila como regex e o v3 descarta o hook sem avisar. `execute_cmd` (Windows) só consta na documentação.
- **Sem variável com a raiz do projeto:** vale o `cwd` do stdin. O `tool_input.cwd` do v3 é ignorado de
  propósito (quem o preenche é o modelo).
- **Fora do escopo:** `--harden` (o Iron Shield usa regras de permissão do Claude) e a etiqueta da AWS. O `iron watch --agent=kiro` lê as
  sessões v2 e v3 (limites na seção 23) e **acusa** o v3 não interativo.
- **O modelo pode recusar antes do hook:** nos testes o Kiro às vezes se recusou a chamar a ferramenta; isso
  não é o Iron Brake.

## 20. Limites do Antigravity CLI

Verificado com o Antigravity CLI **1.2.14** (macOS, `agy`), em 2026-10-01, com o agente real e o `iron` real: bloqueio de
`git push --force` e liberação de `git status` no modo interativo e no `-p`, com e sem `--dangerously-skip-permissions`; `ask`
pela janela do Iron Brake (Executar → o comando roda, Cancelar → bloqueia); prazo de 600 s respeitado; `iron watch --agent=antigravity`
e a cobertura do doctor (sem lacuna com o hook ativo, uma lacuna acusada com o hook desligado); `agent: antigravity` no log. Em **Linux**
(Docker, usuário não root) foi verificado o binário `iron` real com o payload capturado; o **agy de verdade não foi rodado em Linux**
(exigiria o seu login lá). Não verificado: Windows, outras versões.

- **Resposta do hook:** o Iron Brake responde `{"decision":"deny","reason":...}` (exit 0) para bloquear e **não escreve nada** para
  deixar passar. Qualquer saída de erro (exit ≠ 0, stdout que não é JSON, `{}`, campo desconhecido) **bloqueia**; um `{}` vira deny
  com motivo vazio. `allow` e saída vazia passam a decisão à permissão do agente (o prompt "Run this command?" continua).
- **O `ask` não é usado.** No modo normal ele mostra `Reason: ...` ao usuário, mas com `--dangerously-skip-permissions` o comando
  executa mesmo com `ask` ou `force_ask`. O Iron Brake pergunta na própria janela (macOS) e, sem janela, bloqueia.
- **Prazo:** padrão 30 s, em segundos, campo `timeout`. Estourado, o agente **mata o hook e bloqueia** o comando (não é falha
  aberta), mas 30 s cortariam a janela de confirmação (480 s): o `iron init` grava `timeout: 600` e o doctor falha se faltar.
- **`hooks.json` inválido = hook desligado em silêncio, e o comando executa** (verificado com aprovação automática). O doctor valida
  o arquivo; o `init` recusa-se a sobrescrever um arquivo inválido.
- **Confiança na pasta:** no modo interativo o agente pergunta "Do you trust the contents of this project?" e só então roda o hook.
  O `-p` rodou o hook sem essa pergunta. O prompt de confiança e a opção "always allow ... (Persist to settings.json)" são do usuário.
- **Hook global:** `~/.gemini/config/hooks.json` também é lido. O `init` só grava no projeto; um hook global do Iron Brake junto com
  o do projeto o rodaria duas vezes por comando.
- **Pasta do projeto:** o hook roda com `pwd` em `.agents/`; não há variável com a raiz (só `ANTIGRAVITY_CONVERSATION_ID`). Dentro de
  um `workspacePaths` vale a raiz dele; fora de todos, vale o `Cwd` do comando.
- **Matcher:** `run_command` (nome exato; o agy aceita também `a|b`, regex e `*`). Só `run_command` e `view_file` foram observados;
  outras ferramentas que executem comandos (se existirem) não foram enumeradas.
- **`iron watch --agent=antigravity`** lê `~/.gemini/antigravity-cli/brain/<conversa>/.system_generated/logs/transcript_full.jsonl`
  (uma pasta só para todos os projetos: só contam os comandos cujo `Cwd` está na pasta atual; `--all` tira o filtro). O `created_at` do passo
  de resultado é o **início** dele (o hook espera o clique na janela e grava depois), então a chamada só termina na resposta seguinte do
  modelo (passo n+2). O horário tem precisão de 1 s. Num passo com mais de uma chamada só a primeira é conferida. Não vê o que roda fora
  do `agy` nem comandos sem `Cwd`.
- **Etiqueta da AWS:** o Antigravity não tem onde gravar variáveis de ambiente (o `settings.json` é global e não tem chave `env`). O
  `iron init --agent=antigravity` só **informa** o comando: exporte `AWS_SDK_UA_APP_ID=iron-antigravity` no terminal antes de abrir o
  `agy` (verificado: ele repassa o ambiente aos comandos). O prefixo `iron-` do alerta do CloudTrail já casa. Não verificado com chamadas
  reais à AWS.
- **Fora do escopo:** `--harden` (o Iron Shield usa regras de permissão do Claude).
- **O modelo pode recusar antes do hook:** nos testes o agy às vezes se recusou a chamar a ferramenta; isso não é o Iron Brake.
- **Gemini CLI:** o Homebrew o marca como sem suporte do projeto (desativado em 2026-12-18) e indica o Antigravity. O Gemini CLI não
  foi integrado.

## 21. Limites do Codex CLI

Verificado com o Codex CLI **0.159.3** (macOS), em 2026-10-01, com o agente real e o `iron` real: bloqueio de `git push --force` e liberação de
`git status` no `codex exec` e no chat; hook de 600 s; `agent: codex` no log; `iron doctor --agent=codex` (falha sem a confiança, passa com ela);
a etiqueta da AWS nos comandos. Em **Linux** (Docker, usuário não root) foi verificado o binário `iron` real com o payload capturado; o **Codex de
verdade não foi rodado em Linux**. Não verificado: Windows, outras versões e outros modelos.

- **O Codex LIBERA o comando quando o hook falha.** Só bloqueiam o exit 2 (com o motivo no stderr, que o modelo recebe) e o JSON
  `hookSpecificOutput.permissionDecision: "deny"` (ou `{"decision":"block"}`). Exit 1, stdout que não é JSON, `{}`, `{"decision":"deny"}`,
  prazo estourado, e `ask`/`allow` em JSON ("unsupported permissionDecision") deixam o comando **executar**. Por isso o Iron Brake responde só
  com exit 2 e nunca com `ask`.
- **Hook não confiado não roda, em silêncio.** O Codex exige confiar na pasta (`[projects."<pasta>"] trust_level = "trusted"`) e no hook por hash
  (`[hooks.state."<hooks.json>:pre_tool_use:<grupo>:<handler>"] trusted_hash`), ambos no `~/.codex/config.toml`. **Provado:** com o hook instalado
  e não confiado, o `codex exec` deixou um force push executar, sem nenhuma mensagem e com 0 entradas no log. Alterar o hook (caminho, matcher,
  prazo) volta a exigir a confiança ("Modified since last trusted"). O doctor confere a presença das duas confianças e avisa quando o
  `hooks.json` é mais novo que a última gravação do `config.toml`; **não recalcula o hash** (o algoritmo não foi descoberto), então um hook
  alterado depois da confiança pode passar despercebido se o `config.toml` tiver sido gravado depois por outro motivo.
- **Os hooks carregam no início da sessão.** Confiar no meio de uma sessão só vale para as próximas. O chat usa um daemon compartilhado
  (`codex app-server daemon`); numa sessão nova ainda apareceu "hooks disabled until the project is trusted" até o daemon ser reiniciado
  (`codex app-server daemon restart`). O hook do chat roda no ambiente do **daemon**, não no do seu terminal.
- **Prazo e janela.** O `timeout` do hook é em segundos, padrão 600; o `iron init` grava 600. Estourado, o Codex mata o hook e **libera**. No
  chat o Codex espera um hook de 100 s sem problema; no `codex exec` uma chamada com hook acima de ~55 s é **abandonada e refeita** (intermitente:
  um segundo hook começa e o original fica órfão). Por isso a janela do Iron Brake espera no máximo **45 s** no Codex (`Capabilities.DialogCap`).
- **Só o shell é coberto.** O hook recebe `tool_name: "Bash"` com o comando em `tool_input.command`. O `apply_patch` também chega com o texto do
  patch em `tool_input.command` e **não** é analisado (o matcher é `^Bash$`; um patch com o texto `git push --force` não bloqueia). MCP e outras
  ferramentas não são cobertos. Nos testes o modelo rodou comandos por um `custom_tool_call` "exec" que chama `tools.exec_command`, e o hook ainda viu
  `Bash`; o nome `exec_command` da documentação nunca apareceu como `tool_name`, e outros modelos podem usar outro caminho (não verificado).
- **`permission_mode`** é `default` no chat e `bypassPermissions` no `codex exec` (aprovação "never"). Não há variável com a raiz do projeto: vale o `cwd`.
- **Etiqueta da AWS:** `[shell_environment_policy] set = { AWS_SDK_UA_APP_ID = "iron-codex" }` num `.codex/config.toml` **do projeto** chega aos comandos
  no `exec` e no chat (verificado). O `iron init` só cria o arquivo; se ele já existe, mostra o trecho a acrescentar (não há leitor de TOML). Não
  verificado com chamadas reais à AWS.
- **Janela de confirmação (`ask` do Iron Brake), verificada com o Codex real nos dois lados.** "Executar" (clicado pelo usuário no `codex exec`): o hook
  sai 0 em silêncio ("Completed"), o comando executa e o log registra `decision: userApproved`, `dialog: approved`. Sem aprovação (Cancelar, ou os 45 s
  expiraram, o que aconteceu em três tentativas sem clique): o comando é bloqueado com "o usuário recusou este comando na janela de confirmação" e o log
  registra `deny`, `dialog: rejected`. A janela espera só 45 s no Codex: janela atrás de outra, ou 45 s sem olhar, vira recusa.
- **`--dangerously-bypass-hook-trust`** roda hooks sem a confiança persistida; não é usado pelo Iron Brake.
- **Fora do escopo:** `--harden` (usa regras de permissão do Claude). O `iron watch --agent=codex` lê o rollout (o comando está dentro de um trecho de
  JavaScript; limites na seção 23).

## 22. Limites da detecção de agentes e do iron status

- **Detecção** (`iron init`, `iron status`): a pasta do agente em `~/` (`.claude`, `.kiro`, `.gemini/antigravity-cli`, `.codex` ou o `CODEX_HOME`) ou o programa
  no PATH (`claude`, `kiro-cli`, `agy`, `codex`). É um indício de que o agente existe, não de que funciona; a pasta `~/.gemini` sozinha (Gemini CLI antigo) não conta como
  Antigravity. Um agente que o Iron Brake não conhece não é detectado.
- **`iron init` sem `--agent`** pergunta por cada outro agente detectado e ainda não instalado na pasta, **só num terminal de verdade** (`/dev/null` e pipe contam como sem
  terminal e só mostram o comando). Verificado num terminal real (tmux) e em Linux; a resposta em branco é "não". Se um instalador falhar, os outros continuam e o código
  de saída é o maior.
- **`iron status` só lê arquivos.** Não executa o hook, não grava no log, e **não vê** o que o agente faz de fato: "instalado" quer dizer que o arquivo de configuração tem o
  hook do Iron Brake nesta pasta. No Codex ele confere a confiança pela presença no `config.toml` (o hash não é recalculado); hook de projeto não confiado conta como
  "NÃO PROTEGE". A "última decisão" vem dos últimos 30 dias do log e não diferencia pastas (o log é do usuário, não do projeto). As versões mostradas em "verificado com"
  são as versões em que cada agente foi testado, não a que está instalada aqui.
- **Testes e o log real:** os testes do `cmd/iron` que chamam `run(["hook", ...])` gravavam no `~/.iron/audit.log` real de quem rodava a suíte; passaram a usar um `HOME`
  temporário (as entradas antigas continuam no log, que é encadeado por hash e não se edita).

## 23. Limites do iron watch para Kiro e Codex

Leitores verificados com transcripts reais (Kiro 2.26.1, Codex 0.159.3) em 2026-10-01; detalhes dos formatos em [watch.md](watch.md).

- **Kiro v2 não grava horário de término**, e quase só o prompt tem horário. A chamada vale a partir do prompt e a decisão do hook pode vir até 10 min depois, então
  uma lacuna só é acusada depois desse prazo (no `--once` e no doctor não importa; no modo contínuo o aviso demora). Duas chamadas da mesma classe e sessão nesse prazo
  podem se cobrir sem querer.
- **Kiro v3:** só `execute_bash`/`shell`/`execute_cmd`; chamadas `denied` (que não executaram) ficam de fora. O `args.cwd` vem vazio na maioria das chamadas: vale o
  `workspacePaths` do `session.json`. O hook grava a decisão milissegundos antes de o `tool_call` ser escrito, o que a folga de 2 s cobre.
- **Codex lê o comando de dentro de JavaScript.** Só `tools.exec_command({cmd: "..."})` com `cmd` em string literal (aspas duplas, simples ou crase sem `${}`) seguida
  de `,` ou `}`. Concatenação, variável ou outro caminho de execução **não são vistos** (a ausência de lacuna não prova que nada foi executado). Todas as 50 chamadas de
  shell dos testes eram desse formato com o modelo `gpt-6-luna`; outros modelos não foram testados.
- **Codex: o resultado pode ser gravado antes da decisão.** No `codex exec`, uma chamada cujo hook espera o clique da janela teve o resultado registrado 31 s depois
  e a decisão só aos 45 s. Por isso uma decisão vale até 60 s depois do resultado, e a lacuna é acusada com esse atraso. Isso afrouxa a conferência (uma decisão da mesma
  classe nesses 60 s pode cobrir a chamada errada).
- **As sessões de todos os projetos ficam numa pasta só** (como no Antigravity): só contam as chamadas cujo diretório está dentro da pasta atual; `--all` tira o filtro.
- **Não vê** o que o agente executa fora do transcript, nem o que o transcript não registra (o agente escreve esses arquivos; um agente comprometido poderia alterá-los).

