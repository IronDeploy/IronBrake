package rules

import (
	"testing"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

func TestCloudDelete(t *testing.T) {
	runRuleCases(t, cloudDelete, []ruleCase{
		// aws: terminate-instances e qualquer operação delete-*.
		{`aws ec2 terminate-instances --instance-ids i-123`, hook.Ask, hook.Deny},
		{`aws rds delete-db-instance --db-instance-identifier prod`, hook.Deny, hook.Deny},
		{`aws s3api delete-bucket --bucket dados`, hook.Ask, hook.Deny},
		{`aws --profile prod --region us-east-1 dynamodb delete-table --table-name t`, hook.Deny, hook.Deny},

		// az: qualquer comando delete.
		{`az group delete --name rg-prod --yes`, hook.Deny, hook.Deny},
		{`az vm delete -g rg -n vm-1`, hook.Ask, hook.Deny},

		// gcloud: qualquer comando delete.
		{`gcloud compute instances delete vm-1 --zone us-east1-b`, hook.Ask, hook.Deny},
		{`gcloud --project p projects delete p`, hook.Ask, hook.Deny},
		{`gcloud sql instances delete db-prod`, hook.Deny, hook.Deny},

		// Exclusão em massa de armazenamento.
		{`aws s3 rm s3://dados/ --recursive`, hook.Ask, hook.Deny},
		{`aws s3 rb s3://dados --force`, hook.Ask, hook.Deny},
		{`gcloud storage rm -r gs://dados`, hook.Ask, hook.Deny},
		{`gcloud storage rm gs://dados/obj`, hook.Ask, hook.Deny},
		{`gsutil rm -r gs://dados`, hook.Ask, hook.Deny},
		{`gsutil -m rm gs://dados/**`, hook.Ask, hook.Deny},
		{`az storage blob delete-batch --source dados`, hook.Ask, hook.Deny},

		// Parecidos e inofensivos: allow em qualquer ambiente.
		{`aws s3 rm s3://dados/obj.txt`, hook.Allow, hook.Allow}, // um objeto, sem --recursive
		{`aws s3 rb s3://dados`, hook.Allow, hook.Allow},         // rb sem --force falha se tiver conteúdo
		{`aws s3 sync ./build s3://dados`, hook.Allow, hook.Allow},
		{`gcloud storage ls gs://dados`, hook.Allow, hook.Allow},
		{`gsutil ls gs://dados`, hook.Allow, hook.Allow},
		{`aws s3 ls`, hook.Allow, hook.Allow},
		{`aws ec2 describe-instances`, hook.Allow, hook.Allow},
		{`aws --region us-east-1 ec2 describe-instances`, hook.Allow, hook.Allow},
		{`aws s3 cp relatorio.csv s3://dados/relatorio.csv`, hook.Allow, hook.Allow},
		{`az group list`, hook.Allow, hook.Allow},
		{`az group show --name delete`, hook.Allow, hook.Allow}, // "delete" é o nome do grupo
		{`az vm show -g rg -n vm-1`, hook.Allow, hook.Allow},
		{`gcloud compute instances list`, hook.Allow, hook.Allow},
		{`gcloud config set project p`, hook.Allow, hook.Allow},
		{`echo aws ec2 terminate-instances`, hook.Allow, hook.Allow},
	})
}
