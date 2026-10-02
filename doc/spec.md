# Especificação

## Regras invioláveis

1. **Bloquear só com código de saída 2** ou com JSON `permissionDecision: "deny"`.
   Nunca usar o código 1 no caminho do hook: ele **não** bloqueia.
2. **Local-first:** nenhuma chamada de rede e nenhuma dependência de servidor.
   O Iron Brake só executa programas locais (`terraform show`, `osascript`).
3. **Nunca imprimir nem gravar valores de segredos.** As mensagens nunca
   repetem o comando recebido nem a saída de programas externos; o cartão de
   risco mostra só endereço e tipo dos recursos, nunca atributos.
4. **Teste primeiro:** para cada regra, o teste é escrito antes do código.
5. **Na dúvida, trava (fail-closed):** entrada ilegível, plano ilegível, ação
   desconhecida, janela travada ou decisão desconhecida viram bloqueio, nunca
   liberação.

## Decisões

O hook responde uma de quatro decisões:

| Decisão | Significado | Como é entregue |
|---|---|---|
| allow | Iron Brake não tem objeção | código 0, sem saída ("sem opinião": as regras de permissão do Claude Code continuam valendo) |
| ask | Você decide | código 0 + JSON `ask` no stdout |
| allow explícito | Você já aprovou na janela do Iron Brake; o Claude Code não pergunta de novo | código 0 + JSON `permissionDecision: "allow"` no stdout |
| deny | Bloqueado; o agente precisa mudar de estratégia | código 2 + motivo no stderr (vai para o Claude) |

**Quando usar cada uma:** deny para o que nunca é certo o agente fazer; ask
para o que pode ser certo, mas cujo custo é alto e o contexto é seu.

## Regra: force push (git)

| Comando | Decisão |
|---|---|
| `git push --force`, `-f`, flags agrupadas (`-fu`), refspec `+main` | deny |
| `git push --force-with-lease`, `--force-if-includes` | ask |
| `git push origin :main`, `--delete`/`-d` de branch **protegida** (main, master, production, prod, prd, release, develop, trunk) | deny |
| `git push origin :branch`, `--delete` de **outra** branch remota | ask |
| qualquer outro | allow |

## Produção e o `.iron/policy.yaml`

```yaml
# .iron/policy.yaml (opcional) — tudo aqui SOMA à política padrão
production_patterns:        # só palavras de letras
  - live
critical_resource_types:
  - google_sql_database_instance
assume_production: true     # cluster/perfil/workspace sem nome de teste conhecido vale como produção
audit_log:                  # rotação do log de auditoria (ver abaixo)
  max_size_mb: 20           # de 5 a 100; tamanho de cada arquivo antes de girar (padrão 5)
  keep: 10                  # de 5 a 100; arquivos antigos guardados (padrão 5)
```

- **Sem arquivo:** vale a padrão — `production_patterns: prod, production, prd`;
  `critical_resource_types: aws_db_instance, aws_rds_cluster, aws_s3_bucket, aws_eks_cluster`.
- **Com arquivo:** a padrão **mais** o arquivo. Os padrões embutidos nunca
  saem (um agente, ou um erro de digitação, não desliga a proteção).
- **`audit_log` só aumenta:** os dois campos são opcionais e aceitam de 5 a 100
  (o padrão é o piso). Valor menor que o padrão, acima de 100 ou que não é
  número é erro do arquivo, como os outros: senão um projeto de terceiros, ou
  um agente que edita o arquivo, encolheria o rastro de auditoria.
- **Arquivo com erro** (YAML inválido, chave desconhecida, lista virou texto,
  padrão com símbolo, `audit_log` fora dos limites, arquivo ilegível): **tudo é produção** e todo tipo de
  recurso é crítico. Comandos inofensivos continuam liberados; o motivo do
  deny avisa que o arquivo precisa ser corrigido.
- **Programas executados:** o `terraform show` roda o programa de
  `tools.terraform` (também `tofu`, `terragrunt`) do **`~/.iron/config.yaml`**,
  que é do usuário e não do projeto (o hook executa o que ele indica; um
  arquivo de projeto não pode escolher isso). Sem configuração, vale o do
  `PATH`, recusado se estiver dentro do projeto ou se o arquivo ou a pasta
  forem graváveis por qualquer usuário (com o projeto aberto na home, a regra
  do projeto não se aplica: `~/bin` estaria sempre "dentro").
- **Local:** `$CLAUDE_PROJECT_DIR/.iron/policy.yaml` (variável que o Claude
  Code passa ao hook); sem ela, a pasta do comando.

**Produção** = um padrão aparece como **palavra inteira** (letras; qualquer
outro caractere separa; sem diferenciar maiúsculas) no comando (todos os
comandos da linha) ou no contexto atual: pasta do comando, workspace do
terraform (`.terraform/environment`, na pasta do comando, na de `-chdir` e na
de `cd`, com `TF_DATA_DIR`; e `TF_WORKSPACE`), `current-context` do
kubectl (`KUBECONFIG` ou `~/.kube/config`), `AWS_PROFILE`,
`AWS_DEFAULT_PROFILE`, `CLOUDSDK_ACTIVE_CONFIG_NAME`, `CLOUDSDK_CORE_PROJECT`.
`prod-eu`, `eks_prd1` e `/envs/production/` batem; `product-api` não.

## Bloqueios por ambiente (deny em produção, ask fora)

| Categoria | Arquivo | Reconhecido como perigoso | Continua liberado (exemplos) |
|---|---|---|---|
| git (descarte/histórico) | `git.go` | `git reset --hard`; `git clean` com `-f`/`--force` (sem `-n`); `git branch -D` (ou `-d --force`); `git tag -d`; `git stash drop`/`clear`; `git reflog expire`; `git gc --prune=now`/`=all`; `git filter-branch`/`filter-repo`; `git update-ref -d`; `git restore`/`git checkout` que descartam o diretório de trabalho | `git reset --soft`, `git clean -n`, `git branch -d` (sem force), `git gc`, `git gc --prune=never`, `git restore --staged arq`, `git checkout main`, `git checkout -b nova` |
| terraform (destroy) | `terraform.go` | `terraform destroy`; `terraform apply -destroy` (com ou sem plano); idem para `tofu` | `terraform plan -destroy -out=tfplan` (o apply desse plano passa pela regra do apply) |
| terraform (state) | `terraform.go` | `terraform state rm`; `terraform taint`; `terraform workspace delete`; `terraform force-unlock`; idem `tofu` | `terraform state list/show`, `terraform untaint`, `terraform workspace list/select` |
| kubernetes | `kubernetes.go` | `kubectl delete namespace/ns ...`; `kubectl delete deploy/sts/ds/svc/ingress` e `-f` desses kinds (ask, só em produção); `kubectl delete ... --all`; `kubectl delete pvc/pv ...`; `kubectl delete -f arq.yaml` com `kind: Namespace`/`PersistentVolumeClaim`/`PersistentVolume`; `kubectl drain`; `kubectl scale --replicas=0`; `kubectl replace --force` | `kubectl get namespaces`, `kubectl delete pod web-1`, `kubectl delete -f` de outro `kind` (fora de produção; em produção, Deployment/StatefulSet/DaemonSet/Service/Ingress são ask), `kubectl scale --replicas=3`, `kubectl replace -f`, `kubectl cordon` |
| helm | `helm.go` | `helm uninstall`/`delete`; `helm rollback` | `helm install`, `helm upgrade`, `helm list`, `helm status`, `helm template` |
| nuvem (recursos) | `cloud.go` | `aws ... terminate-instances`, `aws ... delete-*`; `az ... delete`/`purge`/`delete-*`; `gcloud ... delete` | `aws s3 ls`, `aws ec2 describe-instances`, `az group list`, `gcloud compute instances list` |
| nuvem (storage em massa) | `cloud.go` | `aws s3 rm --recursive`; `aws s3 rb --force`; `gcloud storage rm`; `gsutil rm` | `aws s3 rm um-objeto`, `aws s3 sync`, `gcloud storage ls`, `gsutil ls` |
| SQL | `sql.go` | com um cliente SQL na linha (`psql`, `mysql`, `mariadb`, `sqlite3`, `sqlcmd`, `clickhouse-client`, `cockroach`): `DROP DATABASE`, `DROP SCHEMA`, `DROP TABLE`, `TRUNCATE`, `DELETE` sem `WHERE` — na linha (`-c`, heredoc, `echo ... \| psql`) **ou** no arquivo (`psql -f arq.sql`, `mysql < arq.sql`) | `SELECT`, `DELETE ... WHERE`, `DROP INDEX`; SQL sem cliente na linha (`grep "DROP TABLE"`, mensagem de commit) |

Arquivos apontados por `-f`/`<` que não dão para ler (remoto, stdin, ausente)
seguem a regra do alvo ilegível: allow fora de produção, ask em produção.
| arquivos (perigoso) | `filesystem.go` | `find ... -delete`/`-exec`; `shred` de arquivo | `find ... -print`, `find ... -name` |
| contêineres | `container.go` | `docker/podman system|volume|image|container prune`; `docker volume rm`; `docker rm -f`; `docker compose down -v` | `docker ps`, `docker rm` (sem `-f`), `docker compose down`, `docker volume ls` |
| sistema | `system.go` | `shutdown`/`reboot`/`poweroff`/`halt`/`init 0|6`; `systemctl stop`/`disable`/`mask`/`kill`; `crontab -r`; flush de firewall (`iptables -F`, `nft flush ruleset`, `ufw disable`/`reset`, `pfctl -F`) | `systemctl status`/`start`/`restart`, `crontab -l`, `iptables -L`, `ufw status` |
| rede → shell | `remotecode.go` | `curl`/`wget ... \| sh/bash/python/...` (baixar e executar); `curl -X DELETE`/`wget --method=DELETE` | `curl ... -o arquivo`, `curl -X GET/POST`, `wget arquivo.tar.gz` |
| publicação de pacote | `publish.go` | `npm/pnpm/yarn/bun publish`; `npm unpublish`; `cargo publish`; `gem push`; `twine upload` | `npm install`/`ci`/`run`, `cargo build`, `gem install` |

`git clean -f` sem `-d` também conta: ele já apaga arquivos não rastreados
sem volta.

Não dependem do ambiente: force push (deny), `--force-with-lease` (ask) e
apply sem plano / plano ilegível / `cd` antes do apply (deny).

## Bloqueios sempre (deny em qualquer ambiente)

Alguns comandos são catastróficos em produção **e** na máquina do
desenvolvedor; não faz sentido só perguntar. São deny em qualquer ambiente:

| Categoria | Arquivo | Reconhecido como perigoso | Continua liberado (exemplos) |
|---|---|---|---|
| arquivos (catastrófico) | `filesystem.go` | `rm -r` em caminho crítico (`/`, `/*`, `~`, `$HOME`, dirs de sistema como `/etc`, `/usr`); `rm --no-preserve-root`; `dd of=/dev/DISCO`; `mkfs*`; `wipefs`; `shred` de dispositivo; `chmod`/`chown -R` em caminho de sistema; redirecionar (`>`, `>>`) para `/dev/DISCO` | `rm -rf build`, `rm -rf node_modules`, `dd of=disco.img`, `dd of=/dev/null`, `chmod -R 755 ./scripts`, `echo x > saida.txt`, `echo x > /dev/null` |
| force push (git) | `git.go` | `git push --force`/`-f`/refspec `+main`/`--mirror` | `git push`, `git push -u`, `git push --follow-tags` |

**Limite conhecido:** o hook vê a linha de comando e lê os arquivos que ela
manda executar (scripts, Makefile, `python3 x.py`), mas não executa nada. Uma
variável definida fora da linha (`$RM -rf /` é ask; `git push $F` com `F` do
ambiente passa) ou montada dentro de um script não é resolvida. A defesa das
outras vias é o Iron Shield e o privilégio mínimo, não o Brake.

**Terraform apply com plano salvo:** plano que apaga ou substitui algum tipo
de `critical_resource_types` **em produção** → deny, com o cartão de risco no
motivo (a janela nem abre). Nos outros casos destrutivos, o fluxo da janela
continua igual.

### Falso positivo e falso negativo

- **Falso positivo:** bloquear algo inofensivo (ex.: barrar `kubectl get ns`).
  Custo: atrito — o agente para e você perde tempo; em excesso, as pessoas
  desligam a proteção.
- **Falso negativo:** deixar passar algo destrutivo (ex.: um comando montado em
  variável, `$RM -rf /`, ou uma exclusão via SDK da nuvem, que não aparecem na
  linha de comando). Custo: perda de dados ou de infraestrutura, às vezes sem
  volta.

Equilíbrio escolhido: nos comandos que conhecemos, **preferir falso positivo**
(na dúvida, trava), mas com regras **estreitas e testadas** com casos
parecidos e inofensivos, para o atrito ficar baixo. Cada falso negativo
conhecido é registrado em [known-issues.md](known-issues.md).

## Proteção contra loops (memória da sessão)

Contada por `session_id` (inclui subagentes da mesma sessão):

- **Repetição:** o mesmo comando **de infraestrutura** (normalizado) 3 vezes em
  5 minutos → ask; 6 vezes → deny. Comandos que não mexem em infra (ex.:
  `go test ./...`) não contam.
- **Limite por sessão:** mais de 3 terraform applies (com plano lido) ou mais
  de 20 recursos alterados na sessão → ask.
- Nunca grava o comando: só um hash com sal aleatório por sessão.

## Log de auditoria

Toda decisão do hook (inclusive evento ilegível) vira uma linha JSON em
`~/.iron/audit.log` (pasta `0700`, arquivo `0600`: só o seu usuário lê e
escreve):

```json
{"time":"2026-09-27T17:08:20-03:00","session":"…","class":"git push","decision":"deny","rule":"git-force-push","prev":"51a7…"}
```

- `class`: programa e subcomando de uma **lista fixa** (`terraform apply`,
  `git push`, `kubectl delete`, `sql`, `aws`, `outro`…); nunca argumentos.
- `decision`: `allow`, `ask`, `deny` ou `userApproved`; `rule`: a(s) regra(s)
  que opinaram; `dialog`: resposta da janela, se abriu.
- `resources`: só no terraform apply — endereço e ação de cada recurso.
- `prev`: SHA-256 da linha anterior (vazio na primeira). `iron audit verify`
  confere a corrente.
- **Nunca** o comando nem valores de segredos.
- Falha ao gravar o log **não muda a decisão** (aviso no stderr), mas deixa um
  marcador (`audit.log.error`, com a hora e o motivo) que o `iron doctor`
  mostra até a próxima gravação que der certo.
- **Rotação:** passando de 5 MB, `audit.log` vira `audit.log.1` (e os antigos
  sobem: `.1`→`.2`…); ficam 5 rotacionados, no máximo ~30 MB. A primeira
  linha do arquivo novo aponta para a última do que saiu, então a corrente
  continua. Ao apagar o mais antigo, o hash da última linha dele vai para
  `audit.log.anchor`, que a primeira linha do que sobrou deve apontar.
  `iron audit verify` confere todos os arquivos, do mais antigo ao atual.
  Tamanho e quantidade podem ser aumentados pelo `audit_log` do
  `.iron/policy.yaml` (só para mais, até 100 MB × 100 arquivos).

## Janela para todo ask

Todo ask do Iron Brake (de qualquer regra, somado aos outros da linha) abre a
janela nativa com o motivo, botões **Cancelar** (padrão) e **Executar**. Sem
janela (fora do macOS, sem tela), vira o ask do Claude Code.

## Regra: "sem plano, sem apply" (terraform)

**Por quê:** um `terraform apply` sem plano salvo calcula e executa na mesma
hora — com `-auto-approve`, ninguém vê o que vai acontecer. Com plano salvo, o
que foi revisado é exatamente o que será executado.

| Situação | Decisão |
|---|---|
| `terraform apply` sem arquivo de plano (inclui `-auto-approve`, `-destroy`, `-var ...`) | deny: "rode terraform plan -out=tfplan primeiro" |
| plano salvo que não dá para ler (inexistente, sem `terraform init`, formato estranho) | deny |
| `cd`/`pushd`/`popd` antes do apply na mesma linha | deny: "use terraform -chdir=PASTA apply tfplan" |
| plano salvo que só cria ou altera | allow |
| plano salvo que apaga ou substitui, **com janela disponível (macOS)** | janela nativa com o cartão de risco: Executar → allow explícito (se a linha tiver só esse comando; senão, sem opinião); Cancelar ou 8 min sem resposta → deny |
| plano salvo que apaga ou substitui, **sem janela** (SSH, nuvem, Linux, Windows) | ask com o cartão de risco |

"Substituir" (`[delete, create]` ou `[create, delete]`) é tão destrutivo quanto
apagar: o recurso antigo e os dados dele deixam de existir.
`create_before_destroy` só muda a ordem (evita indisponibilidade), não evita a
perda de dados.

### Cartão de risco

```
IRON BRAKE — terraform apply
Criar: 3 | Alterar: 0 | Apagar: 1 | Substituir: 1
Apagados ou substituídos:
- aws_db_instance.main (apagar)
- aws_ecs_service.api (substituir)
```

A lista só aparece quando há recursos apagados ou substituídos.

## Pendências já decididas

Decididas em 2026-10-01; **não entram na v0.3.0**.

### Cobertura de scripts configurável no `policy.yaml`

Hoje o Iron Brake lê o conteúdo de `./x.sh`, `bash x.sh`, `python3 x.py`, `make ALVO` e julga pelas mesmas regras,
mas só segue **2 scripts encadeados** (o que o comando chama e o que esse chama; o terceiro da cadeia não é aberto).
O limite é uma constante no código (`maxScriptDepth = 2`, `internal/rules/scripts.go`). A decisão é dar ao usuário
a possibilidade de **configurar esse nível no `.iron/policy.yaml`**, mantendo 2 como padrão.

Pontos a definir na implementação:

- nome da chave (por exemplo `script_depth`) e o **teto** (cada nível lê mais arquivos, até 4 MB cada, e custa tempo no hook);
- o princípio da seção "Produção e o `.iron/policy.yaml`" continua valendo: o arquivo **só soma**. Então o valor só pode
  **aumentar** a cobertura: abaixo do padrão (2), acima do teto ou que não é número é erro do arquivo, e arquivo com erro faz
  **tudo valer como produção** (como no `audit_log`). Sem isso, um projeto de terceiros (ou um agente que edita o arquivo)
  reduziria a cobertura;
- o motivo do ask/deny deve dizer quando um script não foi aberto por causa do limite, para quem quiser subir o nível;
- atualizar `doc/known-issues.md` (seção 1, linha de scripts) e o teste de profundidade.

### `iron doctor --live` (adiado)

Ideia: o doctor rodar o agente de verdade, em modo headless, num repositório temporário, pedindo um comando que **deve** ser
bloqueado, e conferir o resultado. É a única forma de pegar o caso "o hook está instalado mas não dispara" antes de um comando
real passar (por exemplo o Kiro CLI v3 em modo não interativo, onde nenhum hook roda). O `iron watch` e a verificação de cobertura do
`iron doctor` só pegam isso **depois** que um comando rodou.

Custos e cuidados que precisam de decisão: gasta tokens da conta do usuário e exige login; precisa de confirmação explícita
(opt-in); cada agente tem uma forma diferente de rodar em modo headless; não deve rodar em CI sem credencial.
**Fora da v0.3.0; a decisão de fazer ou não fica para depois.**
