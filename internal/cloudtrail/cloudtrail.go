package cloudtrail

import _ "embed"

//go:embed agent-alerts.yaml
var template string

// Template devolve o modelo CloudFormation (YAML) do alerta.
func Template() string { return template }
