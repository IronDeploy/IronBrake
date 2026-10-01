package rules

import "slices"

const awsCredentialsDanger = "extrair credenciais da AWS (export-credentials, chave secreta, token de sessão, assume-role) entrega ao agente uma credencial que passa por cima do portão de credenciais do Iron Brake."

var awsCredentialExport = dangerRule{name: "aws-credential-export", match: matchAWSCredentialExport}

// awsSecretKeys são os valores de "aws configure get" que são segredo.
var awsSecretKeys = []string{"aws_secret_access_key", "aws_session_token", "aws_security_token"}

// awsCredentialCalls são os comandos que imprimem uma credencial (AccessKeyId,
// SecretAccessKey, SessionToken) em vez de usá-la.
var awsCredentialCalls = map[string][]string{
	"sts": {"assume-role", "assume-role-with-saml", "assume-role-with-web-identity", "get-session-token", "get-federation-token"},
	"sso": {"get-role-credentials"},
}

func matchAWSCredentialExport(tokens []string) (string, bool) {
	if len(tokens) == 0 || programName(tokens[0]) != "aws" {
		return "", false
	}
	pos := awsPositionals(tokens[1:])
	if len(pos) >= 2 && slices.Contains(awsCredentialCalls[pos[0]], pos[1]) {
		return awsCredentialsDanger, true
	}
	if len(pos) < 2 || pos[0] != "configure" {
		return "", false
	}

	switch {
	case pos[1] == "export-credentials":
		return awsCredentialsDanger, true
	case pos[1] == "get" && len(pos) >= 3 && slices.Contains(awsSecretKeys, pos[2]):
		return awsCredentialsDanger, true
	}
	return "", false
}
