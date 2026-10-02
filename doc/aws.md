# AWS: o portão de credenciais

O hook decide **o que o agente executa**. O portão decide **quando o agente
recebe credenciais da AWS**. É o ponto por onde passam todas as ferramentas que
falam com a AWS (aws, terraform, cdk, sam, boto3...), então protege também o que
o hook não enxerga: um script, um SDK dentro de um programa, ou um agente (como o
Kiro no IDE) que não entrega o comando ao hook.

Ele é a ponta do `credential_process` do `~/.aws/config`: o SDK roda um programa
sempre que precisa de credenciais, e esse programa é o `iron aws-creds`.

## Configurar

Um perfil "de fachada" que o agente usa, apontando para o `iron aws-creds`, que
por sua vez chama quem entrega as credenciais de verdade:

```ini
# ~/.aws/config
[profile prod]
credential_process = /Users/voce/.local/bin/iron aws-creds --env production --require-tty --label prod -- /opt/homebrew/bin/aws configure export-credentials --profile prod-real --format process

[profile staging]
credential_process = /Users/voce/.local/bin/iron aws-creds --label staging -- /opt/homebrew/bin/aws configure export-credentials --profile staging-real --format process
```

Use **caminhos absolutos** (o `PATH` do agente não é de confiança). O comando depois
de `--` precisa imprimir o JSON do `credential_process` (`Version: 1`,
`AccessKeyId`, `SecretAccessKey`, `SessionToken`, `Expiration`).

**Escreva sempre o `--label`.** O SDK não passa ao `credential_process` o perfil escolhido
com `--profile`, e o `AWS_PROFILE` do ambiente quem controla é o agente. Por isso, no modo
`auto`, **sem `--label` o pedido vale como produção** (a janela avisa "a linha do
credential_process não tem --label"): sem o nome não há como saber qual conta é, e errar
para o lado de liberar seria o defeito perigoso. Os modos `production`, `strict` e `dev` não
dependem do nome do ambiente.

Opções do `iron aws-creds`:

| Opção | Efeito |
|---|---|
| `--env auto` (padrão) | produção se o **nome do perfil** tiver `prod`, `production`, `prd`... (palavra inteira) |
| `--env production` | sempre produção |
| `--env strict` | produção, salvo nome de teste (`dev`, `staging`, `qa`, `sandbox`...) |
| `--env dev` | nunca produção |
| `--label NOME` | nome na janela e no log, e o que o `--env auto` lê. Escreva sempre |
| `--require-tty` | sem agente e **sem terminal de controle** (processo desligado do pai, cron, CI), trata como agente (`sem-terminal`). Recomendado em perfil de produção de uso humano; **não use em perfil de CI** |
| `--approve-for 15m` | quanto vale a liberação depois do "Executar" |
| `--wait 45s` | quanto a janela espera (o SDK desiste em cerca de 1 minuto) |

## Como decide

O `iron aws-creds` sobe pelos processos ancestrais (`ps`) e procura um agente:
`claude`, `kiro-cli`, `gemini`, `codex`, `cursor-agent`, pelo nome do programa. Quando o agente
roda como script (`node`, `bun`, `deno`, `python3.x`), vale o nome do script ou da pasta do pacote
(`.../@anthropic-ai/claude-code/cli.js`), pulando opções como `--inspect`.

| Quem pediu | Conta | O que acontece |
|---|---|---|
| Humano (nenhum agente nos ancestrais) | qualquer | entrega, sem registrar nada |
| Agente | não é produção | entrega e **registra** no log de auditoria |
| Agente | produção, sem liberação | **janela** "O agente X pediu as credenciais do perfil Y, que é PRODUÇÃO": Executar libera por 15 min; Cancelar, fechar ou esperar nega |
| Agente | produção, liberação vigente | entrega e registra (`dialog: window`) |

A liberação vale para o par agente + perfil: outro agente ou outro perfil pergunta de
novo. Pedidos simultâneos (o agente pode rodar vários `aws` ao mesmo tempo) **não empilham
janelas**: uma trava deixa só uma pergunta aberta, e os outros esperam e reaproveitam a
liberação; se a janela de outro pedido continuar aberta além do prazo, o pedido é negado. Negar (ou a janela não estar disponível) não cria liberação, e a mensagem de erro
chega ao agente pelo stderr. Se o `ps` falhar, o pedido é tratado como de um agente
("desconhecido"). No Windows o comando recusa funcionar, em vez de liberar sem checar.

O log (`~/.iron/audit.log`) ganha linhas `class: "aws credentials"`, `rule: "aws-gate"`, o
agente, a decisão e o nome do perfil em `resources`. Nunca a credencial.

## Etiqueta do agente e CloudTrail

O portão decide quando entregar credenciais. A etiqueta responde, depois, **quem fez o quê**: o
`iron init` grava `AWS_SDK_UA_APP_ID=iron-claude` no `env` do `.claude/settings.json`. O SDK da AWS
acrescenta `app/iron-claude` ao user agent de cada chamada, e o CloudTrail registra o user agent. Assim
dá para separar, no CloudTrail, o que o agente fez do que você fez com o mesmo perfil.

```bash
iron init                 # grava o hook e a etiqueta
iron init --no-aws-tag    # só o hook
```

O `init` não sobrescreve: se o projeto já define `AWS_SDK_UA_APP_ID` com outro valor, ele avisa e deixa
como está (e o CloudTrail não mostrará `app/iron-claude`). Um `env` em `.claude/settings.json` só vale
depois que você confia na pasta (`/trust`), segundo a documentação do Claude Code.

**Alerta no EventBridge.** O `iron aws-alerts` imprime um modelo CloudFormation que avisa por SNS (e-mail
opcional) quando uma chamada com `app/iron-*` no user agent é destrutiva (`Delete*`, `Terminate*`,
`Deregister*`, `Disable*`, `Detach*`, `ScheduleKeyDeletion`, `StopInstances`, `PutBucketPolicy`) ou mexe em
permissões (`Put*Policy`, `Attach*Policy`, `UpdateAssumeRolePolicy`, `CreateAccessKey`,
`CreateLoginProfile`). O alerta sai também para a tentativa que a AWS negou (o CloudTrail registra a
chamada com erro).

```bash
iron aws-alerts > agent-alerts.yaml
aws cloudformation deploy --template-file agent-alerts.yaml --stack-name iron-brake-agent-alerts \
  --parameter-overrides AlertEmail=voce@empresa.com
```

Crie a pilha **em cada região** em que o agente atua. Exige **ao menos um trail do CloudTrail com
logging ativo** na conta, porque os eventos `AWS API Call via CloudTrail` só chegam ao EventBridge assim
(documentação do EventBridge). Uma regra no estado padrão só recebe eventos de **escrita**; chamadas de
leitura exigem outro estado e não estão no modelo. Para consultar o histórico, filtre o `userAgent` por
`%app/iron-%` (no CloudTrail Lake: `SELECT eventTime, eventName, userIdentity.arn FROM <data-store-id>
WHERE userAgent LIKE '%app/iron-%'`; consulta não testada).

### Limites da etiqueta

- **É uma declaração do ambiente do agente.** O agente (ou um comando seu) pode zerar a variável
  (`AWS_SDK_UA_APP_ID= aws ...`) e a chamada sai sem etiqueta. Serve para atribuição e alerta, não para
  segurança.
- **Só chama quem o SDK acompanha a variável.** Verificado só com o AWS CLI v2. Terraform, CDK, boto3 e
  outros não foram testados.
- **Só o Claude Code por enquanto** (o `env` do `settings.json`). Os outros agentes ainda não têm onde
  gravá-la.
- **O modelo não foi implantado na AWS.** Foi validado com o `cfn-lint` 1.57.1 e por testes, mas ninguém
  o executou numa conta: confira o e-mail de confirmação do SNS e teste com uma chamada inofensiva.
  Eventos de serviços globais (IAM, por exemplo) chegam só na região `us-east-1`, segundo a documentação do
  CloudTrail (não verificado neste projeto). O atraso do alerta não foi medido.
- **O tópico SNS não é criptografado** com chave própria: a chave padrão do SNS impede o EventBridge de
  publicar.

## Fechando o desvio óbvio

Um agente que roda `aws configure export-credentials --profile prod-real` passa por
cima do portão. Por isso o hook ganhou a regra `aws-credential-export`: esse comando, e
`aws configure get aws_secret_access_key`/`aws_session_token`, e também `aws sts assume-role*`,
`get-session-token`, `get-federation-token` e `aws sso get-role-credentials` (que imprimem a credencial),
pedem confirmação (em produção, bloqueiam). Uma credencial pode ser obtida de outros jeitos que
nenhuma regra de comando cobre; o que fecha isso é a permissão da credencial. Combine com o Iron Shield (`iron init --harden`), que tira do agente
a leitura de `~/.aws/**`.

## Limites (leia antes de confiar)

- **O agente roda com o seu usuário.** Se ele conseguir ler as credenciais reais por
  outro caminho (arquivo, cache do SSO em `~/.aws/sso/cache`, outro perfil), o portão não
  vê. O portão pega o uso do perfil como configurado, o engano e o loop, não um agente que
  tenta burlar. A proteção que não depende do agente é a permissão da própria
  credencial: dê ao agente um perfil só-leitura.
- **A liberação pode ser forjada.** Ela fica num arquivo do seu usuário
  (`<cache>/ironbrake/awsgate/grants.json`, só com hashes); um agente que escreve nesse
  arquivo se libera sozinho. O arquivo é um atalho contra perguntas repetidas, não uma prova.
- **Processo desligado do pai** (daemon, `setsid`) perde a cadeia e, sem `--require-tty`,
  parece humano e recebe credenciais de produção sem janela e sem registro (verificado).
  Com `--require-tty` ele passa a contar como agente. O agente que arranja um pseudo-terminal
  (por exemplo com `script`) volta a parecer humano.
- **A identificação é por nome.** Agente fora da lista (ou renomeado, ou empacotado com outro
  nome) passa como humano.
- **Decide por pedido de credencial, não por comando.** Com credenciais de 1 hora, tudo o que
  o agente fizer nesse tempo usa a mesma liberação.
- **Custo:** cerca de 30 ms por pedido (medido: 33 ms contra 3,6 ms do script de credenciais
  sozinho), quase todo da lista de processos. O SDK roda o portão uma vez por processo.
- **CI e cron** (sem agente nos ancestrais) passam direto, a não ser que o perfil use
  `--require-tty`; por isso essa opção não vai em perfil de CI.
- **O comando real é executado como está no `~/.aws/config`**, que é o seu arquivo. Quem
  edita esse arquivo controla o que roda.

## Verificado

Em 2026-10-01, no macOS 26.6.2, com o AWS CLI 2.37.5 de verdade e credenciais **falsas** (nenhuma
chamada à AWS: `aws configure list` resolve a credencial localmente), em config e `HOME` temporários:

- o SDK chamou o portão pelo `credential_process` e recebeu a credencial (`custom-process`);
- o agente `claude` foi identificado pela cadeia real de processos; a linha do log traz
  `agent: claude`, `class: aws credentials`, `rule: aws-gate`;
- **produção, janela real:** aprovado por mim em ~10 s (`userApproved`); o pedido seguinte usou a
  liberação sem janela (`dialog: window`); outro perfil de produção perguntou de novo e foi
  recusado, e o AWS CLI repassou a mensagem do Iron Brake no erro; a recusa não criou liberação;
- `grants.json` com permissão 0600, só hashes;
- processo desligado do pai (ppid 1): recebeu credenciais de **produção** sem janela e sem
  registro; com `--require-tty` ficou registrado como `sem-terminal`; dentro de um
  pseudo-terminal (`script`) passou como humano;
- Claude Code real (headless): o `aws configure list` passou pelo hook e pelo portão, e
  `aws configure export-credentials --profile prod` foi bloqueado pela regra
  `aws-credential-export`.

- **Etiqueta:** o `iron init` gravou `AWS_SDK_UA_APP_ID=iron-claude` no `.claude/settings.json`; o Claude Code
  real (headless) viu `TAG=iron-claude` no Bash e o `User-Agent` que o AWS CLI mandou a um servidor HTTP
  **local** trazia `app/iron-claude` (o mesmo comando fora do agente não trazia). Nenhuma chamada saiu da
  máquina.
- **Modelo CloudFormation:** `cfn-lint` sem erros (e o lint reprova um modelo estragado de propósito).

**Não verificado:** terraform, cdk, boto3 e outros SDKs (o `credential_process` é o mesmo, mas só
o AWS CLI foi testado); agentes que não são o Claude Code; credenciais reais e SSO; Linux;
o comportamento do `aws configure export-credentials --format process` com SSO.
