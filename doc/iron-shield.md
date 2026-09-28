# Iron Shield

O Iron Shield é o raio-X de credenciais do Iron Deploy: ele lista o que um
agente de IA rodando na sua máquina **conseguiria alcançar** e ajuda a tirar
essas credenciais do caminho. Comandos:

- `iron scan` — mostra as credenciais ao alcance (só leitura, sem rede).
- `iron scan --manage` — a mesma varredura, numa tela interativa: setas
  navegam entre as categorias achadas, **Enter oculta/mostra** cada uma na
  hora, **Esc ou `q`** sai. Ver seção própria abaixo.
- `iron init --harden` — grava regras que **bloqueiam a leitura** desses lugares.
- `iron shield status` — mostra o que está travado e o que não está.
- `iron shield lock` — trava a leitura das credenciais (o mesmo que `init --harden`,
  mas funciona sozinho, sem precisar de `iron init` antes).
- `iron shield unlock` — destrava: remove exatamente as regras do Iron Shield,
  sem tocar em nenhuma outra regra `deny`/`allow` do arquivo. As credenciais
  voltam a ficar visíveis ao agente até o próximo `lock`.

`lock` e `unlock` são um par simétrico e idempotente: rodar de novo sem
mudança não dá erro, e cada um só mexe nas regras que ele mesmo controla.
Os dois (e cada toggle do `--manage`) ficam registrados em
`~/.iron/audit.log` (classe `iron shield`, decisão `lock`/`unlock`) —
destravar credenciais é uma decisão de risco e precisa estar no mesmo
rastro de auditoria que as do hook.

## Regra de ouro

O scan **nunca lê nem imprime o valor de um segredo.** Cada achado traz só três
coisas: **de onde veio**, um **rótulo seguro** (nome da variável, caminho do
arquivo, nome do perfil/remote) e **por que importa**. Os testes garantem que
nenhum valor vaza para a saída.

## O que o `iron scan` avalia

| Fonte | Onde procura | O que marca |
|---|---|---|
| Variáveis de ambiente | ambiente do processo | chaves de nuvem, tokens de plataforma/deploy, credenciais de banco, e qualquer nome que cheire a segredo |
| AWS | `~/.aws/credentials` | perfil com chave de longa duração; perfil com nome de admin/produção |
| AWS SSO | `~/.aws/sso/cache/*.json` | token de sessão SSO em cache (ativo até expirar) |
| Azure | `~/.azure` | cache de tokens do CLI (`accessTokens.json`, `msal_token_cache.json`); credenciais de service principal; assinatura ativa com nome de produção |
| GCP | `~/.config/gcloud` | credenciais padrão de aplicação (ADC); banco local de contas (`credentials.db`); credenciais legadas por conta; projeto ativo com nome de produção |
| Arquivos `.env` | pasta do comando e pastas-pai (até 5 níveis) | `.env`, `.env.local`, `.env.development`, `.env.production`, `.env.prod` com chave de segredo em texto puro |
| SSH | `~/.ssh` | chave privada **sem senha** (a com senha é ignorada) |
| Kubernetes | `~/.kube/config` | contexto de produção; credencial (token/chave) embutida no arquivo |
| Terraform | `~/.terraform.d/credentials.tfrc.json`; `terraform.tfstate`, `.backup`, `.terraform/terraform.tfstate` | token do Terraform Cloud/HCP; state com segredos em texto puro |
| npm | `~/.npmrc`, `./.npmrc` | token de registro (`_authToken`/`_password`) |
| PyPI | `~/.pypirc`, `./.pypirc` | senha/token de upload salvo em texto puro |
| Docker | `~/.docker/config.json` | credencial de registro salva |
| GitHub CLI | `~/.config/gh/hosts.yml` | token do `gh` (`oauth_token`) |
| git | `.git/config` (pasta e pais) | token embutido na URL de um remote https (`https://TOKEN@host`) — a URL nunca é impressa |
| `.netrc` | `~/.netrc` | host com senha em texto puro (usado automaticamente por curl/git/wget) |
| `.pgpass` | `~/.pgpass` | senha(s) de Postgres em texto puro, usadas automaticamente por `psql` |

### Gravidade

Ordenada do mais grave ao menos, no mesmo código de cores da ficha de risco:

| | Nível | Exemplos |
|---|---|---|
| 🔴 | **crítico** | chave de nuvem de longa duração; perfil admin/produção |
| 🟠 | **alto** | token de plataforma/deploy; credencial de banco; segredo em `.env`; state com segredo; tokens de npm/Docker/gh/git |
| 🟡 | **médio** | credencial AWS de sessão; chave SSH sem senha; contexto de produção no kube; state sem segredo aparente; segredo genérico por nome |
| ⚪ | **baixo** | reservado para tokens sabidamente só-leitura |

### Exemplo de saída

```
Iron Shield — raio-X das credenciais ao alcance do agente

🔴 CRÍTICO
   • AWS · ~/.aws/credentials [perfil default] — perfil AWS com chave de longa duração — não expira
🟠 ALTO
   • .env · ~/projeto/.env — segredo em texto puro no arquivo (ex.: GITHUB_TOKEN)
   • git · ~/projeto/.git/config [remote origin] — token embutido na URL do remote git

2 credencial(is) ao alcance. Nenhum valor de segredo foi lido ou impresso.
```

## `iron scan --manage`: gerenciar visibilidade por categoria

Diferente de `iron shield lock`/`unlock`, que trava ou destrava **tudo de
uma vez**, o `--manage` opera **por categoria** (AWS, SSH, Kubernetes, GCP,
Azure, Terraform, npm, PyPI, Docker, GitHub CLI, `.netrc`, `.pgpass`, `.env`)
— a mesma agrupação que `internal/shield.Categories` usa para ligar cada
achado do scan às regras `deny` que o escondem. Só entram na tela categorias
com **pelo menos um achado nesta máquina agora**; sem achado nenhum, mostra
"Nenhuma credencial de risco encontrada" e não há nada para gerenciar.

Cada linha mostra: gravidade do pior achado daquela categoria (🔴🟠🟡⚪, igual
ao `iron scan`), status (`⛔ OCULTA` ou `✅ VISÍVEL`), o nome da categoria e
quantos achados. **Enter** troca o estado na hora — grava ou apaga a(s)
regra(s) `deny` daquela categoria no `.claude/settings.json` **imediatamente**,
sem "salvar ao sair": uma queda de terminal no meio não perde o que já foi
alternado. **Esc** ou **`q`** sai sem mais perguntas.

Duas fontes que o `iron scan` relata **não têm como ser ocultadas por essa
tela** (aparecem como `N/D`, com o motivo embaixo quando selecionadas):

- **Variáveis de ambiente** — não são arquivo; a regra `Read(...)` do Claude
  Code não alcança. A defesa aqui é rodar o agente num shell/sandbox sem
  essas variáveis.
- **git** (token embutido na URL do remote) — o segredo mora dentro do
  `.git/config`; bloquear a leitura desse arquivo quebraria comandos git
  legítimos do agente.

Precisa de um terminal de verdade (tty) — não roda dentro de um pipe/script.

## O que o `iron init --harden` faz

Além de instalar o hook do Iron Brake, grava regras `deny` de **leitura** em
`permissions.deny` do `.claude/settings.json`, cobrindo os mesmos lugares que o
scan inspeciona:

```
Read(~/.aws/**)            Read(~/.config/gcloud/**)   Read(**/.env)
Read(~/.ssh/**)            Read(~/.azure/**)           Read(**/.env.local)
Read(~/.kube/**)           Read(~/.terraform.d/**)     Read(**/.env.production)
Read(~/.npmrc)             Read(~/.docker/config.json) Read(**/terraform.tfstate)
Read(~/.pypirc)            Read(**/.pypirc)             Read(**/terraform.tfstate.backup)
Read(~/.config/gh/**)      Read(**/.env.development)   Read(~/.netrc)
Read(~/.pgpass)
```

`~/.aws/**` já cobre o cache de sessão SSO (`~/.aws/sso/cache`), sem regra extra.

Templates sem segredo (`.env.example`) ficam de fora de propósito. As regras
são idempotentes (rodar de novo não duplica) e o resto do arquivo é preservado.

## Limites

- O scan é **heurístico por nome/local**, não valida o escopo real da credencial
  (isso é opt-in numa fase futura, chamando a API do provedor).
- O `--harden` cobre a ferramenta **Read**. Leitura via Bash (`cat ~/.aws/...`)
  depende do Iron Brake e de rodar o agente com privilégio mínimo.
- Emitir **credenciais temporárias por tarefa** (o broker) é da fase 2 — exige
  integração com o IAM de cada nuvem.
