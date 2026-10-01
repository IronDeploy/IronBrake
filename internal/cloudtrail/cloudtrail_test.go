package cloudtrail

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func parse(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(Template()), &doc); err != nil {
		t.Fatalf("o modelo não é YAML válido: %v", err)
	}
	return doc
}

func child(t *testing.T, m map[string]any, keys ...string) any {
	t.Helper()
	var cur any = m
	for _, k := range keys {
		next, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("%v não é um objeto (procurando %v)", cur, keys)
		}
		if cur, ok = next[k]; !ok {
			t.Fatalf("falta a chave %q em %v", k, keys)
		}
	}
	return cur
}

func TestTemplateHasTheParts(t *testing.T) {
	doc := parse(t)

	resources := child(t, doc, "Resources").(map[string]any)
	for name, kind := range map[string]string{
		"AgentAlertsTopic":       "AWS::SNS::Topic",
		"AgentAlertsEmail":       "AWS::SNS::Subscription",
		"AgentAlertsRule":        "AWS::Events::Rule",
		"AgentAlertsTopicPolicy": "AWS::SNS::TopicPolicy",
	} {
		if got := child(t, resources, name, "Type"); got != kind {
			t.Errorf("%s: tipo %v, esperava %s", name, got, kind)
		}
	}

	pattern := child(t, resources, "AgentAlertsRule", "Properties", "EventPattern").(map[string]any)
	if got := child(t, pattern, "detail-type").([]any); len(got) != 1 || got[0] != "AWS API Call via CloudTrail" {
		t.Errorf("detail-type: %v", got)
	}
	// O source do evento é o do serviço (aws.ec2...), nunca aws.cloudtrail: filtrar
	// por ele faria o alerta não casar com nada.
	if _, has := pattern["source"]; has {
		t.Error("o padrão não pode filtrar por source")
	}
	userAgent := child(t, pattern, "detail", "userAgent").([]any)
	if len(userAgent) != 1 {
		t.Fatalf("userAgent: %v", userAgent)
	}
}

func TestTemplateWatchesTheDangerousCalls(t *testing.T) {
	text := Template()
	for _, want := range []string{
		"prefix: Delete", "prefix: Terminate", "prefix: ScheduleKeyDeletion", "StopInstances",
		"AttachRolePolicy", "PutRolePolicy", "UpdateAssumeRolePolicy", "CreateAccessKey",
		`*app/${AppIdPrefix}*`, "events.amazonaws.com", "sns:Publish",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("o modelo deveria conter %q", want)
		}
	}
	if strings.Contains(text, "aws.cloudtrail") {
		t.Error("o source do evento não é aws.cloudtrail")
	}
}

func TestTemplateIsPlainASCII(t *testing.T) {
	for i, r := range Template() {
		if r > 127 {
			t.Fatalf("caractere fora do ASCII na posição %d (%q): o CloudFormation pode recusar", i, r)
		}
	}
}

func TestDefaultPrefixMatchesWhatInitWrites(t *testing.T) {
	// O "iron init" grava iron-claude; o prefixo padrão do modelo tem de cobri-lo.
	if !strings.Contains(Template(), `Default: "iron-"`) {
		t.Error("o prefixo padrão deve ser iron-")
	}
}
