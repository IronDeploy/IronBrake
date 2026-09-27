// Package audit mantém o log de auditoria (~/.iron/audit.log): uma linha JSON
// por decisão, cada uma com o SHA-256 da linha anterior. Verify confere a
// corrente. O log nunca contém o comando, só a classe dele.
package audit
