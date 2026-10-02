# Como contribuir

O Iron Brake é um hook que decide, antes de um agente de código executar um
comando, se ele passa, pede confirmação ou é bloqueado. Um erro aqui custa em
um de dois sentidos: deixar passar algo destrutivo, ou travar quem trabalha.
Este guia diz como mudar o projeto sem piorar nenhum dos dois.

Antes de mexer em regras, leia as **regras invioláveis** em
[doc/spec.md](doc/spec.md). Elas valem para qualquer mudança:

1. bloquear só com código de saída 2 (ou JSON `deny`);
2. nada de rede: só programas locais;
3. nunca imprimir nem gravar valores de segredos, e nunca repetir o comando
   recebido nas mensagens;
4. teste primeiro;
5. na dúvida, trava (fail-closed).

## Compilar e testar

Precisa do Go da versão indicada no [go.mod](go.mod).

```bash
make test     # go test -count=1 ./...
make build    # bin/iron para esta máquina
make dist     # dist/: 6 binários + SHA256SUMS (só para testar o release)
```

Antes de abrir o PR, rode também o que o CI roda:

```bash
go vet ./...
gofmt -l .    # não pode listar nada
make test
```

O CI roda isso em **Linux, macOS e Windows**. Um teste que só passa no seu
sistema quebra o PR dos outros (veja [Testes portáveis](#testes-portáveis)).

Para um mapa de onde fica cada coisa, veja
[doc/how-it-works.md](doc/how-it-works.md). Para testar à mão, sem abrir o
agente, veja [doc/testing.md](doc/testing.md).

## Commits e pull requests

- Mensagem em português, no formato `tipo(escopo): resumo` ou `tipo: resumo`.
  Os tipos em uso: `feat`, `fix`, `docs` e `test`. Exemplos reais:
  `fix(rules): caminho com raiz é absoluto no Windows`,
  `docs: kiro verificado em Linux`.
- Um commit por mudança lógica. Se o PR mistura correção, testes e
  documentação de assuntos diferentes, separe.
- A `main` não aceita push direto, force push nem histórico com merge: o PR é
  obrigatório e entra por squash ou rebase.
- O CI precisa estar verde nos três sistemas.
- Se o PR resolve uma issue, ponha `Closes #N` na descrição, senão ela fica
  aberta depois do merge.
- No PR, diga o que mudou, **por que**, e como você verificou. Se mudou o que
  o hook decide, diga o que antes passava e agora não (ou o contrário).

## Propor uma regra nova

Na ordem:

1. **Escreva o teste primeiro.** As regras têm testes em tabela
   (`internal/rules/*_test.go`): o comando, a decisão fora de produção e a
   decisão em produção. Inclua casos **parecidos e inofensivos** que devem
   continuar liberados (`kubectl get ns`, `rm -rf build`): é isso que mantém
   o atrito baixo.
2. **Escolha a decisão certa.** `deny` para o que nunca é certo o agente
   fazer; `ask` para o que pode ser certo mas custa caro. Quando o ambiente
   decide (deny em produção, ask fora), use `dangerRule`.
3. **Implemente a regra** em `internal/rules/` e registre em
   [registry.go](internal/rules/registry.go).
4. **Cuide da mensagem.** O motivo vai para o agente e para o log: nunca
   repita o comando, caminhos, nomes de recursos ou saída de programas.
5. **Documente.** Acrescente a regra em [doc/spec.md](doc/spec.md) e, se ela
   tem um limite conhecido (o que ainda escapa, o que ela barra sem querer),
   uma linha em [doc/known-issues.md](doc/known-issues.md).

### Afrouxar uma regra

**Nenhuma regra é afrouxada sem um teste que explique o motivo.** Se uma regra
barra algo legítimo, escreva primeiro o teste com o comando legítimo, mostre
que ele falha, e só então ajuste a regra. Um PR que apenas apaga um caso de
teste ou muda `deny` para `ask` sem esse teste não é aceito.

## Suportar um agente novo

Um agente só é chamado de "suportado" depois de verificado **com o agente de
verdade**, e não só pela documentação dele. Documentação diverge do
comportamento (já aconteceu mais de uma vez: bloqueio que não bloqueia, hook
não confiado que não avisa). O que se espera:

- adaptador em `internal/hook/`, instalador em `internal/setup/` e verificação
  em `internal/doctor/`;
- amostras de eventos **reais**, capturadas do agente, em
  `testdata/events/<agente>/` (troque só caminhos e identificadores);
- `iron init --agent=NOME` e `iron doctor --agent=NOME` funcionando de ponta a
  ponta;
- uma seção em [doc/agents.md](doc/agents.md) com a versão testada, o que foi
  provado (force push bloqueado, comando liberado, motivo chegando ao modelo)
  e, **explicitamente, o que não foi feito** (outros sistemas, outras
  versões). Em [doc/known-issues.md](doc/known-issues.md), os limites.

## Testes portáveis

Como o CI roda no Windows, escreva os testes assim:

- **Home do usuário:** no Windows o Go lê `USERPROFILE`, não `HOME`. Use um
  helper que defina os dois (veja `setHome` em `cmd/iron/main_test.go`); senão
  o teste grava no home real da máquina.
- **Caminhos:** monte com `filepath.Join`. Ao comparar texto que a pessoa lê,
  compare com `filepath.ToSlash`. Para decidir se um caminho dentro de um
  *comando* é absoluto, use `internal/pathx.IsAbs`, não `filepath.IsAbs`
  (no Windows, `/data/prod` não é absoluto para o `filepath`, e tratá-lo como
  relativo o faria parecer "dentro do projeto").
- **Hook ou programa falso:** não escreva um script `/bin/sh`; o Windows não o
  executa. Use `internal/testutil/fakesh`.
- **Permissões Unix** (`0600`, `0700`): o Windows não tem esses bits. Pule o
  teste com `runtime.GOOS == "windows"` e um motivo na mensagem do `t.Skip`.
- **Fim de linha:** o repositório usa LF em todo lugar (`.gitattributes`).

## Verificar com o agente real

Para mudanças que afetam o que o hook vê ou responde, o teste automatizado não
basta. Rode o agente de verdade, numa pasta de teste, com o `iron init`
feito, e confirme o que mudou. Há um roteiro de exemplo em
[doc/testing.md](doc/testing.md). Anote no PR a versão do agente e o resultado.

## Licença

Ao contribuir, você concorda que sua contribuição fica sob a
[Apache 2.0](LICENSE), a mesma do projeto.
