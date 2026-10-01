// Package watch confere, de fora do hook, se o Iron Brake está mesmo vendo o
// que o agente executa: lê o transcript do agente (que registra cada chamada
// de shell) e o log de auditoria do Iron Brake, e acusa o comando que rodou
// sem nenhuma decisão registrada. Só observa: não bloqueia nada.
//
// O transcript é um arquivo que o próprio agente escreve; um agente comprometido
// poderia alterá-lo. O watch serve para achar hook que não dispara ou mal
// configurado, não para pegar quem burla de propósito.
package watch
