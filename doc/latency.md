# Latência do hook

Meta: **p95 abaixo de 50 ms** — 95% das chamadas do hook terminam em menos de
50 ms.

Medido em 2026-09-27 · Apple M2 Pro (12 núcleos, 16 GB) · macOS 26.6.2 ·
Go 1.27.1 · terraform 1.16.4.

## Resultado

| Caso | Média | p50 | p95 | Máximo | Meta |
|---|---|---|---|---|---|
| `git status` (allow) | 3,1 ms | 3,0 ms | 3,7 ms | 4,9 ms | ✅ |
| `git push --force` (deny) | 3,1 ms | 3,0 ms | 3,6 ms | 3,8 ms | ✅ |
| `kubectl delete namespace prod` (deny) | 3,1 ms | 3,0 ms | 3,6 ms | 3,9 ms | ✅ |
| `kubectl get pods` (allow, grava o estado da sessão) | 4,0 ms | 4,0 ms | 4,7 ms | 5,1 ms | ✅ |
| `terraform apply -auto-approve` (deny, sem plano) | 3,1 ms | 3,0 ms | 3,5 ms | 3,9 ms | ✅ |
| **todos os casos acima juntos** | **3,3 ms** | **3,0 ms** | **4,2 ms** | **5,1 ms** | ✅ |
| `terraform apply create.tfplan` (roda `terraform show`) | 134,1 ms | 134,0 ms | 136,9 ms | 139,5 ms | ❌ |

200 execuções por caso (+5 de aquecimento, fora da conta), cada uma um
processo novo, como o Claude Code faz. Nenhum caso abre a janela: a espera
pelo seu clique é tempo humano e fica fora da meta.

Com o log de auditoria (uma linha gravada por chamada), a mesma medição deu
p95 de **4,3 ms** para todos os casos sem programa externo (antes: 4,2 ms).

### Remedição em 2026-09-28, depois do Iron Shield (scan/harden/shield/manage)

Mesma máquina e mesmas versões. O Iron Shield (`iron scan`, `iron init
--harden`, `iron shield status/lock/unlock`, `iron scan --manage`) roda como
comando separado, nunca dentro do caminho do hook — a expectativa era zero
mudança na latência do `iron hook`, e foi o que a remedição confirmou:

| Caso | Média | p50 | p95 | Máximo | Meta |
|---|---|---|---|---|---|
| `git status` (allow) | 3,3 ms | 3,2 ms | 3,9 ms | 9,2 ms | ✅ |
| `git push --force` (deny) | 3,3 ms | 3,2 ms | 3,6 ms | 7,7 ms | ✅ |
| `kubectl delete namespace prod` (deny) | 3,2 ms | 3,2 ms | 3,5 ms | 4,3 ms | ✅ |
| `kubectl get pods` (allow, grava o estado da sessão) | 4,0 ms | 4,0 ms | 4,6 ms | 5,2 ms | ✅ |
| `terraform apply -auto-approve` (deny, sem plano) | 3,2 ms | 3,2 ms | 3,6 ms | 3,9 ms | ✅ |
| **todos os casos acima juntos** | **3,4 ms** | **3,2 ms** | **4,3 ms** | **9,2 ms** | ✅ |
| `terraform apply create.tfplan` (roda `terraform show`) | 131,1 ms | 130,5 ms | 135,8 ms | 147,0 ms | ❌ (mesmo limite de sempre, ver abaixo) |

p95 de tudo sem programa externo ficou em **4,3 ms**, igual à medição com log
de auditoria de 27/09 — dentro da variação normal de ruído da máquina (o
máximo isolado subiu para 9,2 ms num caso, mas é ruído pontual, não o p95).
**Conclusão: o Iron Shield não mexeu na latência do hook.**

## Onde o tempo vai

Dentro do processo (`go test -bench`), o trabalho do Iron Brake é de
**microssegundos**:

| Parte | Tempo por chamada |
|---|---|
| `runHook` completo, sem programa externo | 1,3–2,1 µs |
| `loadEnv` (policy.yaml, workspace, kubeconfig pequeno, variáveis) | 2,1 µs |
| kubeconfig grande (50 clusters, ~110 KB) | 1,3 ms |
| interpretar o JSON de um plano (~13 KB) | 25 µs |
| estado da sessão em disco (trava + leitura + gravação) | 0,23 ms |

Os ~3 ms por chamada vêm quase todos de **iniciar o processo** (o sistema
carregar o binário e o Go iniciar). O caso do terraform é dominado pelo
próprio `terraform show -json`: **~120 ms** medidos direto no terminal. Com
`TF_LOG=trace` dá para ver o porquê: ele **inicia cada provedor do projeto**
como um processo separado (aqui 3, de ~17 MB cada) só para ler os esquemas.
Com provedores grandes (o da AWS tem centenas de MB), deve ser bem mais lento
— não medido.

## Otimizações

| Otimização | Situação |
|---|---|
| Só chamar o `terraform show` quando o plano decide: `terraform apply` **com** plano salvo que nenhuma regra já barrou (deny do registro, apply sem plano, `cd` antes). | ✅ já era assim; agora garantido por `TestRunHookReadsPlanOnlyWhenNeeded` |
| Comando que não mexe em infra nem é apply não toca no disco da sessão. | ✅ já era assim (`TestStoreSkipsIrrelevantActivity`) |
| Ler o kubeconfig procurando só a linha `current-context:` em vez de interpretar o YAML todo. | ❌ não feito: custa 1,3 ms no pior caso medido; ganharia pouco e erraria com YAML válido fora do padrão |
| Guardar em disco o resumo de planos já lidos (cache pelo hash do arquivo do plano). | ❌ **rejeitado por segurança**: o agente pode escrever na pasta de cache e forjar um resumo "só cria" para um plano que apaga. Cache em memória não existe (cada chamada é um processo novo). |
| Ler o arquivo do plano direto, sem `terraform show`. | ❌ rejeitado: é um formato interno do terraform (zip + protobuf) que muda entre versões; ler errado = decidir errado |

## Conclusão

- **Todos os comandos que não leem plano: p95 de 4,2 ms**, mais de 10× abaixo
  da meta.
- **`terraform apply` com plano salvo: p95 de ~137 ms**, acima da meta, e sem
  otimização simples e segura: o tempo é do `terraform show`. É ~0,1 s somado
  a um apply que leva segundos ou minutos.
- Numa sessão real, applies são poucos (menos de 5% dos comandos), então o p95
  de **todas** as chamadas continua em poucos milissegundos.

## Como reproduzir

```bash
# Benchmarks dentro do processo
go test -run '^$' -bench . -benchmem ./cmd/iron/ ./internal/session/ ./internal/tfplan/ ./internal/runenv/

# Binário real, 200 vezes por caso
go build -o bin/iron ./cmd/iron
go run ./scripts/latency -bin bin/iron -n 200 -tfdir /pasta/do/projeto/terraform
```

A pasta do `-tfdir` precisa de `terraform init` feito e de um
`create.tfplan` que só crie recursos (plano destrutivo abriria a janela). Sem
`-tfdir`, o caso do terraform é pulado. Um projeto pronto para isso é o do
roteiro em [testing.md](testing.md), com `terraform plan -out=create.tfplan`
antes do primeiro apply.
