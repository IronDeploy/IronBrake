## O que muda e por quê

<!-- O que mudou e a razão. Se mudou o que o hook decide, diga o que antes
passava e agora não (ou o contrário). -->

## Issue relacionada

<!-- Closes #N se o PR resolve a issue por inteiro; Refs #N se for parcial. -->

Closes #

## Como foi testado

<!-- Comandos rodados e o que você viu. Se mexeu em regra, cite os casos de
teste novos, inclusive os parecidos e inofensivos que continuam liberados. -->

## Impacto em `doc/known-issues.md` e no README

<!-- Apareceu um limite novo (algo que ainda escapa ou que a regra barra sem
querer)? Mudou algo que o README descreve? Diga o que atualizou, ou "nenhum". -->

## Checklist

- [ ] `go vet ./...`, `gofmt -l .` (sem saída) e `make test` passam
- [ ] Testes novos ou ajustados cobrem a mudança (escritos antes da correção)
- [ ] Documentação atualizada (`doc/spec.md`, `doc/known-issues.md`, README), se for o caso
- [ ] Nenhuma regra foi afrouxada sem um teste e uma justificativa
- [ ] Nada de segredo, nem trecho do `audit.log`, nem o comando recebido nas mensagens
