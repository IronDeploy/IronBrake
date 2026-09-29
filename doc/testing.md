# Como testar

Simula os cenários do Iron Brake sem precisar de nuvem nem de um apply real.

## 1. Teste manual do binário

Simula o Claude Code sem abri-lo:

```bash
go build -o bin/iron ./cmd/iron

# helper: roda o hook com um comando e uma pasta
check() { jq -nc --arg c "$1" --arg d "${2:-$PWD}" \
  '{tool_name:"Bash",cwd:$d,tool_input:{command:$c}}' | bin/iron hook; echo "exit=$?"; }

check 'git push --force'                    # exit=2
check 'git push --force-with-lease'         # JSON ask, exit=0
check 'terraform apply -auto-approve'       # exit=2, "sem plano salvo"
check 'cd infra && terraform apply tfplan'  # exit=2, mensagem do cd
check 'terraform apply tfplan' /pasta/com/plano/destrutivo   # abre a janela
```

E o doctor, dentro de um projeto com o hook instalado:

```bash
/caminho/para/bin/iron doctor    # esperado: 5 × OK
```

## 2. Roteiro dentro do Claude Code

### Preparação

```bash
go build -o bin/iron ./cmd/iron

mkdir ~/iron-brake-lab && cd ~/iron-brake-lab
cat > main.tf <<'EOF'
terraform {
  required_providers {
    local  = { source = "hashicorp/local" }
    null   = { source = "hashicorp/null" }
    random = { source = "hashicorp/random" }
  }
}

resource "random_pet" "name" {}

resource "null_resource" "marker" {}

resource "local_file" "greeting" {
  filename = "greeting.txt"
  content  = "olá"
}
EOF
terraform init
/caminho/para/bin/iron init
/caminho/para/bin/iron doctor      # esperado: 5× OK
```

Abra o Claude Code nessa pasta.

### Casos

| # | O que fazer | O que você deve ver |
|---|---|---|
| 1 | Peça: "rode exatamente `terraform apply -auto-approve`" | Bloqueado antes de rodar; o Claude repete "terraform apply sem plano salvo bloqueado…" e sugere `plan -out`. `greeting.txt` não existe. |
| 2 | Peça `terraform plan -out=tfplan` e depois `terraform apply tfplan` | Nenhuma mensagem do Iron Brake. Pode aparecer a caixa normal do Claude Code. `Resources: 3 added`. |
| 3 | Apague o bloco `null_resource` do `main.tf`; peça plan e depois apply | Janela **Iron Brake** com o cartão (`Apagar: 1`, `null_resource.marker (apagar)`); o Claude fica aguardando. **Cancelar:** o Claude recebe "o usuário recusou…" e segue; `terraform state list` ainda mostra o recurso. **Executar:** `1 destroyed`. |
| 4 | Troque `content = "olá"` por `"tchau"`; plan e apply | Janela com `Substituir: 1`, `local_file.greeting (substituir)`. Executar → `1 added, 0 changed, 1 destroyed`. |
| 5 | Peça: "rode exatamente `cd . && terraform apply tfplan`" | Bloqueado com "…depois de cd na mesma linha bloqueado… use terraform -chdir=PASTA apply tfplan." |
| 6 | Peça: "rode `kubectl version --client` 3 vezes seguidas, em comandos separados" (não precisa de cluster) | 1ª e 2ª: nada. 3ª: janela "o mesmo comando de infraestrutura rodou 3 vezes em 5 minutos…". Na 6ª em 5 minutos: bloqueado sem janela. |
| 7 | Peça 4 applies de planos só de criação, com mais de 5 minutos entre eles (ou 4 planos diferentes) | O 4º abre a janela "esta sessão já fez 4 applies…". |

Se aparecer `Saved plan is stale`, o `main.tf` ou o state mudou depois do plan:
rode o plan de novo.

O estado da sessão fica em `~/Library/Caches/ironbrake/sessions/` (um `.json`
por sessão). Para recomeçar a contagem dos casos 6 e 7: apague essa pasta.

### Limpeza

```bash
cd ~/iron-brake-lab && terraform destroy
cd ~ && rm -rf ~/iron-brake-lab
```
