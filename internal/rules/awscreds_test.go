package rules

import (
	"testing"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

func TestAWSCredentialExport(t *testing.T) {
	runRuleCases(t, awsCredentialExport, []ruleCase{
		{`aws configure export-credentials`, hook.Ask, hook.Deny},
		{`aws configure export-credentials --format env`, hook.Ask, hook.Deny},
		{`aws configure export-credentials --profile staging --format process`, hook.Ask, hook.Deny},
		{`aws --profile staging configure export-credentials`, hook.Ask, hook.Deny},
		{`AWS_PROFILE=dev aws configure export-credentials`, hook.Ask, hook.Deny},
		{`eval $(aws configure export-credentials --format env)`, hook.Ask, hook.Deny},
		{`aws configure get aws_secret_access_key`, hook.Ask, hook.Deny},
		{`aws configure get aws_session_token --profile staging`, hook.Ask, hook.Deny},
		{`aws configure get --profile staging aws_secret_access_key`, hook.Ask, hook.Deny},
		{`sudo aws configure export-credentials`, hook.Ask, hook.Deny},
		{`aws configure export-credentials --profile prod-real`, hook.Deny, hook.Deny}, // "prod" no comando

		{`aws sts assume-role --role-arn arn:aws:iam::1:role/x --role-session-name s`, hook.Ask, hook.Deny},
		{`aws sts get-session-token`, hook.Ask, hook.Deny},
		{`aws sts get-session-token --serial-number arn --token-code 123456`, hook.Ask, hook.Deny},
		{`aws --profile staging sts assume-role-with-web-identity --role-arn x --role-session-name s --web-identity-token t`, hook.Ask, hook.Deny},
		{`aws sts get-federation-token --name x`, hook.Ask, hook.Deny},
		{`aws sso get-role-credentials --role-name x --account-id 1 --access-token t`, hook.Ask, hook.Deny},
		{`aws sts assume-role --role-arn arn:aws:iam::1:role/prod-deploy --role-session-name s`, hook.Deny, hook.Deny},

		// Inofensivos.
		{`aws sts get-caller-identity`, hook.Allow, hook.Allow},
		{`aws sts decode-authorization-message --encoded-message x`, hook.Allow, hook.Allow},
		{`aws sso list-accounts --access-token t`, hook.Allow, hook.Allow},
		{`aws configure list`, hook.Allow, hook.Allow},
		{`aws configure get region`, hook.Allow, hook.Allow},
		{`aws configure get aws_access_key_id`, hook.Allow, hook.Allow},
		{`aws configure set region sa-east-1`, hook.Allow, hook.Allow},
		{`aws configure`, hook.Allow, hook.Allow},
		{`aws sts get-caller-identity`, hook.Allow, hook.Allow},
		{`aws s3 ls`, hook.Allow, hook.Allow},
		{`echo aws configure export-credentials`, hook.Allow, hook.Allow},
	})
}
