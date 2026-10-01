// Package cloudtrail guarda o modelo CloudFormation que alerta quando um
// agente de código chama uma API destrutiva ou de permissões na AWS. O agente é
// reconhecido pelo user agent app/iron-*, que o SDK da AWS escreve a partir de
// AWS_SDK_UA_APP_ID e o CloudTrail registra em cada chamada.
package cloudtrail
