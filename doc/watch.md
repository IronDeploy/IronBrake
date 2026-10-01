# iron watch: conferir de fora se o hook está vendo tudo

O hook decide **antes** de o comando rodar, mas não tem como saber se ele
**deixou de rodar**: um agente que ignora o hook, uma configuração errada, ou um
modo do agente em que o hook não carrega (por exemplo o Kiro CLI v3 não
interativo) fazem o comando passar sem ninguém dizer
nada. O `iron watch` fecha esse buraco.

Ele lê o **transcript** do agente (que registra cada chamada de shell) e o **log de
auditoria** do Iron Brake (`~/.iron/audit.log`). Se um comando rodou e o log não
tem nenhuma decisão para ele, avisa. Só observa: não bloqueia nada.

```bash
iron watch                 # acompanha esta pasta e avisa na hora (Ctrl+C sai)
iron watch --notify        # também manda uma notificação do sistema (macOS)
iron watch --once          # confere a última hora e sai (código 1 se houver lacuna)
iron watch --once --since 24h
iron watch --all           # todas as pastas de ~/.claude/projects
iron watch --dir PASTA     # outra pasta de transcripts
```

Saída:

```
iron watch: SEM DECISÃO: git push às 11:16:11 (sessão b47bfd8f). o hook pode não ter rodado.
iron: 80 comando(s) de shell nos últimos 4h0m0s: 2 com decisão, 78 SEM decisão.
      nenhum foi visto pelo Iron Brake: o hook não está instalado ou não dispara neste agente.
```

O comando **nunca** aparece na saída, só a classe (`git push`, `terraform
destroy`...), como no log de auditoria.

No modo contínuo, cada lacuna também vira uma linha no log de auditoria
(`decision: "gap"`, `rule: "watch"`), na mesma corrente de hashes. O `--once`
só relata: rodá-lo várias vezes não repete entradas.

## Como a conferência funciona

Para cada chamada de shell **com resultado** no transcript, procura no log uma
decisão da mesma sessão, da mesma classe (`rules.Classify`, a mesma que o hook
grava) e gravada entre a emissão da chamada e a volta do resultado (com 2 s de
folga de relógio). Cada decisão cobre uma chamada só: dois `git push` iguais
precisam de duas decisões. O aviso sai 3 s depois do resultado (`--grace`) e a
leitura é a cada 1 s: na prática, uns 5 s depois do comando.

## Limites

- **Só o Claude Code por enquanto** (`~/.claude/projects/<pasta>/<sessão>.jsonl`).
  Os transcripts dos outros agentes ainda não foram verificados.
- **O transcript é escrito pelo próprio agente.** Um agente comprometido poderia
  alterá-lo. O watch acha hook que não dispara ou mal configurado, não quem burla
  de propósito (mesmo limite do log de auditoria, [known-issues.md](known-issues.md)).
- **Só observa.** Quando o aviso chega, o comando já rodou.
- **Só chamadas de shell (`Bash`)**, como o hook. Chamadas de subagentes
  (`isSidechain`) ficam de fora: não dá para saber se o hook as registra com a mesma
  sessão.
- **Decisão sem `session_id` vale para qualquer sessão.** Se o evento não traz a
  sessão, o hook grava `session: ""`, e o watch aceita essa decisão para qualquer
  chamada da mesma classe e hora, o que pode esconder uma lacuna.
- **A conferência é por classe e hora, não pelo comando.** O log não guarda o
  comando de propósito, então não dá para provar que a decisão foi daquele comando
  exato.
- **Precisa estar rodando** para acompanhar ao vivo: não é um serviço (nada de
  launchd/systemd). Use `--once` num agendador ou no CI para conferir depois.
- **Sem hook instalado**, todo comando é lacuna. O watch avisa no começo se o hook
  não está na pasta nem no usuário (`~/.claude/settings.json`).

## Verificado

Em 2026-10-01, com o binário contra o transcript e o log reais da sessão que
desenvolve o Iron Brake (sem hook instalado): acusou 78 de 80 comandos, as 2
exceções eram entradas de teste manual com `session: ""` casando por acaso; no
modo contínuo, o aviso apareceu em ~5 s e a linha `gap` entrou na auditoria com a
corrente íntegra. O caso "com decisão" foi exercitado só nos testes automatizados
(`internal/watch`, `cmd/iron/watch_test.go`), não com um hook real ativo.
