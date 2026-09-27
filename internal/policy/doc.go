// Package policy lê o .iron/policy.yaml, que define o que é produção e quais
// tipos de recurso do terraform são críticos. O arquivo só soma à política
// padrão; qualquer erro nele é devolvido para o hook tratar tudo como produção.
package policy
