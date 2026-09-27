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
| qualquer outro | allow |

## Produção e o `.iron/policy.yaml`

```yaml
# .iron/policy.yaml (opcional) — tudo aqui SOMA à política padrão
production_patterns:        # só palavras de letras
  - live
critical_resource_types:
  - google_sql_database_instance
```

- **Sem arquivo:** vale a padrão — `production_patterns: prod, production, prd`;
  `critical_resource_types: aws_db_instance, aws_rds_cluster, aws_s3_bucket, aws_eks_cluster`.
- **Com arquivo:** a padrão **mais** o arquivo. Os padrões embutidos nunca
  saem (um agente, ou um erro de digitação, não desliga a proteção).
- **Arquivo com erro** (YAML inválido, chave desconhecida, lista virou texto,
  padrão com símbolo, arquivo ilegível): **tudo é produção** e todo tipo de
  recurso é crítico. Comandos inofensivos continuam liberados; o motivo do
  deny avisa que o arquivo precisa ser corrigido.
- **Local:** `$CLAUDE_PROJECT_DIR/.iron/policy.yaml` (variável que o Claude
  Code passa ao hook); sem ela, a pasta do comando.

**Produção** = um padrão aparece como **palavra inteira** (letras; qualquer
outro caractere separa; sem diferenciar maiúsculas) no comando (todos os
comandos da linha) ou no contexto atual: pasta do comando, workspace do
terraform (`.terraform/environment` e `TF_WORKSPACE`), `current-context` do
kubectl (`KUBECONFIG` ou `~/.kube/config`), `AWS_PROFILE`,
`AWS_DEFAULT_PROFILE`, `CLOUDSDK_ACTIVE_CONFIG_NAME`, `CLOUDSDK_CORE_PROJECT`.
`prod-eu`, `eks_prd1` e `/envs/production/` batem; `product-api` não.

## Bloqueios por ambiente (deny em produção, ask fora)

| Categoria | Arquivo | Reconhecido como perigoso | Continua liberado (exemplos) |
|---|---|---|---|
| git | `git.go` | `git reset --hard`; `git clean` com `-f`/`--force` (sem `-n`) | `git reset --soft`, `git reset HEAD arq`, `git clean -n`, `git clean -fdn` |
| terraform | `terraform.go` | `terraform destroy`; `terraform apply -destroy` (com ou sem plano) | `terraform plan -destroy -out=tfplan` (o apply desse plano passa pela regra do apply e mostra o cartão) |
| kubernetes | `kubernetes.go` | `kubectl delete namespace/ns/namespaces ...`; `kubectl delete ... --all`; `kubectl drain` | `kubectl get namespaces`, `kubectl delete pod web-1`, `kubectl delete pods -l app=x`, `kubectl cordon` |
| nuvem | `cloud.go` | `aws ... terminate-instances`, `aws ... delete-*`; `az ... delete`; `gcloud ... delete` | `aws s3 ls`, `aws ec2 describe-instances`, `az group list`, `gcloud compute instances list` |
| SQL | `sql.go` | com um cliente SQL na linha (`psql`, `mysql`, `mariadb`, `sqlite3`, `sqlcmd`, `clickhouse-client`, `cockroach`): `DROP DATABASE`, `DROP TABLE`, `TRUNCATE`, `DELETE` sem `WHERE` | `SELECT`, `DELETE ... WHERE`, `DROP INDEX`; SQL sem cliente na linha (`grep "DROP TABLE"`, mensagem de commit) |

`git clean -f` sem `-d` também conta: ele já apaga arquivos não rastreados
sem volta.

Não dependem do ambiente: force push (deny), `--force-with-lease` (ask) e
apply sem plano / plano ilegível / `cd` antes do apply (deny).

**Terraform apply com plano salvo:** plano que apaga ou substitui algum tipo
de `critical_resource_types` **em produção** → deny, com o cartão de risco no
motivo (a janela nem abre). Nos outros casos destrutivos, o fluxo da janela
continua igual.

### Falso positivo e falso negativo

- **Falso positivo:** bloquear algo inofensivo (ex.: barrar `kubectl get ns`).
  Custo: atrito — o agente para e você perde tempo; em excesso, as pessoas
  desligam a proteção.
- **Falso negativo:** deixar passar algo destrutivo (ex.: liberar
  `aws s3 rm --recursive`). Custo: perda de dados ou de infraestrutura, às
  vezes sem volta.

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
- Falha ao gravar o log **não muda a decisão** (aviso no stderr).

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

