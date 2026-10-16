# Política de segurança

O Iron Brake decide, antes de um agente de código executar um comando, se ele
passa, pede confirmação ou é bloqueado. Uma falha aqui pode deixar passar algo
destrutivo, então relatos de segurança são tratados com prioridade.

## Versões suportadas

Só a **última versão publicada** recebe correções de segurança. Veja a sua com
`iron version` e a mais recente em
[Releases](https://github.com/IronDeploy/IronBrake/releases/latest). Se o
problema já foi corrigido na última versão, a resposta é atualizar.

## Como relatar

**Não abra uma issue pública** para uma vulnerabilidade.

Use o relato privado do GitHub: na aba **Security** do repositório, clique em
[**Report a vulnerability**](https://github.com/IronDeploy/IronBrake/security/advisories/new).
Só os mantenedores veem o relato.

Inclua:

- a versão (`iron version`), o agente e o sistema operacional;
- o comando ou evento que reproduz o problema e o que o `iron` decidiu
  (`exit=0`, ask ou deny) contra o que devia decidir;
- o impacto: o que o comando faz de destrutivo e em que ambiente.

**Não envie segredos** (chaves, tokens, senhas) **nem o conteúdo do
`audit.log`**. Troque valores sensíveis por `XXXX`.

## O que esperar

- **Confirmação de recebimento** em até 7 dias.
- **Avaliação** (se é vulnerabilidade, a gravidade e o plano) em até 14 dias.
- **Correção** o quanto antes; para o que for confirmado, a meta é publicar
  uma versão corrigida em até 30 dias. Se precisar de mais tempo, avisamos no
  próprio relato.
- Depois da correção, o aviso é publicado e quem relatou é creditado, se
  quiser.

Pedimos que o problema não seja divulgado antes da versão corrigida sair.

## O que é uma vulnerabilidade

- Um **bypass**: um comando destrutivo que o `iron` devia barrar (ou pedir
  confirmação) e deixa passar, e que **não** está listado em
  [doc/known-issues.md](doc/known-issues.md).
- Vazamento de segredo: o `iron` imprimir ou gravar o valor de uma credencial
  ou repetir o comando recebido nas mensagens, no log de auditoria ou no
  estado da sessão.
- Uma falha que faça o `iron` **liberar em vez de travar** quando algo dá
  errado (ele deve falhar fechado).
- Contornar a janela de confirmação ou o portão de credenciais sem o clique de
  quem usa.
- Problemas na distribuição: binário, `SHA256SUMS` ou fluxo de release que
  permitam trocar o que a pessoa instala.
- Alguma chamada de rede feita pelo `iron` (ele só usa programas locais).

## O que não é

Não são vulnerabilidades (abra uma issue comum, ou um PR, se quiser melhorar):

- Os **limites já documentados** em [doc/known-issues.md](doc/known-issues.md),
  como alias e funções do shell, variável definida em outra chamada,
  `ssh host "comando"` e scripts além do limite de leitura.
- Um agente que **tenta de propósito** burlar o `iron`, por exemplo apagando o
  estado da sessão. A proteção é contra erros e loops, não contra isso.
- **Falso positivo** (comando seguro barrado) e **falso negativo** de uma
  regra que ainda não existe: use os formulários de issue.
- Resultado de um ambiente já comprometido (quem altera o binário, o
  `~/.iron` ou as configurações do agente já tem o acesso que o `iron`
  protegeria).

Na dúvida, relate em particular: é melhor receber um relato que não era
vulnerabilidade do que perder um que era.
