package rules

import (
	"testing"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

func TestIaCDestructive(t *testing.T) {
	runRuleCases(t, iacDestructive, []ruleCase{
		// cdk
		{`cdk destroy`, hook.Ask, hook.Deny},
		{`cdk destroy --force`, hook.Ask, hook.Deny},
		{`cdk destroy -f MinhaStack`, hook.Ask, hook.Deny},
		{`cdk --profile conta destroy`, hook.Ask, hook.Deny},
		{`cdk --opcao-desconhecida valor destroy`, hook.Ask, hook.Deny},
		{`npx cdk destroy`, hook.Ask, hook.Deny},
		{`npx --yes cdk destroy --all`, hook.Ask, hook.Deny},
		{`pnpx cdk destroy`, hook.Ask, hook.Deny},
		{`bunx cdk destroy`, hook.Ask, hook.Deny},
		{`CDK destroy`, hook.Ask, hook.Deny}, // o nome do programa não diferencia maiúsculas
		{`cdk deploy --require-approval never`, hook.Ask, hook.Deny},
		{`cdk deploy --require-approval=never`, hook.Ask, hook.Deny},
		{`npx cdk deploy --all --require-approval never`, hook.Ask, hook.Deny},
		{`cdk watch --require-approval never`, hook.Ask, hook.Deny},

		// cdk gc: o padrão (--action full) apaga; só print e tag não apagam.
		{`cdk gc`, hook.Ask, hook.Deny},
		{`cdk gc aws://123456789012/us-east-1`, hook.Ask, hook.Deny},
		{`cdk gc --unstable=gc`, hook.Ask, hook.Deny},
		{`cdk gc --action full`, hook.Ask, hook.Deny},
		{`cdk gc --action=delete-tagged`, hook.Ask, hook.Deny},
		{`cdk gc --type s3 --yes`, hook.Ask, hook.Deny},
		{`cdk gc --confirm=false`, hook.Ask, hook.Deny},
		{`cdk gc --action print --action full`, hook.Ask, hook.Deny},
		{`cdk gc --action`, hook.Ask, hook.Deny},
		{`cdk gc --action bogus`, hook.Ask, hook.Deny},
		{`cdk --profile conta gc`, hook.Ask, hook.Deny},
		{`npx aws-cdk@2 gc`, hook.Ask, hook.Deny},
		{`cdk gc --action print`, hook.Allow, hook.Allow},
		{`cdk gc --action=print`, hook.Allow, hook.Allow},
		{`cdk gc --action tag`, hook.Allow, hook.Allow},
		{`cdk gc --type s3 --action print`, hook.Allow, hook.Allow},
		{`cdk gc --action=tag --rollback-buffer-days 7`, hook.Allow, hook.Allow},

		// sam
		{`sam delete`, hook.Ask, hook.Deny},
		{`sam delete --stack-name app --no-prompts`, hook.Ask, hook.Deny},
		{`sam --region sa-east-1 delete`, hook.Ask, hook.Deny},

		// eksctl
		{`eksctl delete cluster --name app`, hook.Ask, hook.Deny},
		{`eksctl delete nodegroup --cluster app --name ng`, hook.Ask, hook.Deny},
		{`eksctl --region sa-east-1 delete cluster -n app`, hook.Ask, hook.Deny},

		// pulumi
		{`pulumi destroy`, hook.Ask, hook.Deny},
		{`pulumi down --yes`, hook.Ask, hook.Deny},
		{`pulumi -s prod destroy`, hook.Deny, hook.Deny}, // "prod" no comando já é produção
		{`pulumi up --yes`, hook.Ask, hook.Deny},
		{`pulumi up -y`, hook.Ask, hook.Deny},
		{`pulumi update --yes --stack dev`, hook.Ask, hook.Deny},
		{`pulumi stack rm --force`, hook.Ask, hook.Deny},
		{`pulumi stack remove dev -f`, hook.Ask, hook.Deny},
		{`pulumi state delete urn:pulumi:dev::app::aws:s3/bucket:Bucket::b`, hook.Ask, hook.Deny},

		// serverless
		{`serverless remove`, hook.Ask, hook.Deny},
		{`sls remove --stage prod`, hook.Deny, hook.Deny},
		{`npx serverless remove`, hook.Ask, hook.Deny},

		// serverless 3: opções com valor que escondiam o subcomando.
		{`sls --app deploy remove`, hook.Ask, hook.Deny},
		{`serverless --org deploy remove --stage dev`, hook.Ask, hook.Deny},
		{`sls --package deploy remove`, hook.Ask, hook.Deny},
		{`sls --stage dev remove`, hook.Ask, hook.Deny},
		{`sls param list`, hook.Allow, hook.Allow},

		// cdktf
		{`cdktf destroy`, hook.Ask, hook.Deny},
		{`cdktf deploy --auto-approve`, hook.Ask, hook.Deny},

		// Nome do pacote npm, com versão, e gerenciadores de pacote.
		{`npx aws-cdk destroy --force`, hook.Ask, hook.Deny},
		{`npx aws-cdk@2 destroy`, hook.Ask, hook.Deny},
		{`npx -y aws-cdk@2 deploy --require-approval never`, hook.Ask, hook.Deny},
		{`npx cdktf-cli destroy`, hook.Ask, hook.Deny},
		{`npx cdktf-cli@0.20 deploy --auto-approve`, hook.Ask, hook.Deny},
		{`pnpm dlx aws-cdk destroy`, hook.Ask, hook.Deny},
		{`yarn dlx aws-cdk@2 destroy`, hook.Ask, hook.Deny},
		{`npm exec -- cdk destroy`, hook.Ask, hook.Deny},
		{`npm exec aws-cdk -- destroy`, hook.Ask, hook.Deny},
		{`npm run cdk -- destroy`, hook.Ask, hook.Deny},
		{`pnpm cdk destroy`, hook.Ask, hook.Deny},
		{`yarn cdk destroy`, hook.Ask, hook.Deny},
		{`bun x cdk destroy`, hook.Ask, hook.Deny},
		{`pnpm exec pulumi destroy`, hook.Ask, hook.Deny},

		// pulumi: "dn" é alias oficial de destroy (aliases: destroy, down, dn).
		{`pulumi dn`, hook.Ask, hook.Deny},
		{`pulumi dn --yes`, hook.Ask, hook.Deny},
		{`pulumi --stack dev dn -y`, hook.Ask, hook.Deny},
		{`pulumi dn --preview-only`, hook.Allow, hook.Allow},

		// Flags com valor que as ferramentas reais têm: o valor não pode esconder o subcomando.
		{`pulumi --exclude up destroy`, hook.Ask, hook.Deny},
		{`pulumi --config up destroy`, hook.Ask, hook.Deny},
		{`cdk --region deploy destroy`, hook.Ask, hook.Deny},
		{`cdk -m deploy destroy`, hook.Ask, hook.Deny},
		{`eksctl --cfn-role-arn get delete cluster -n app`, hook.Ask, hook.Deny},

		// Via npx com --package (o que o npm exec roda de verdade).
		{`npx --yes --package aws-cdk cdk destroy`, hook.Ask, hook.Deny},
		{`npm exec --yes --package=aws-cdk -- cdk destroy`, hook.Ask, hook.Deny},

		// pulumi: flags com valor e agrupadas.
		{`pulumi up --yes=true`, hook.Ask, hook.Deny},
		{`pulumi stack rm -fy`, hook.Ask, hook.Deny},
		{`pulumi stack rm -yf`, hook.Ask, hook.Deny},
		{`pulumi up -vy`, hook.Ask, hook.Deny},

		// Inofensivos.
		{`pulumi up --yes=false`, hook.Allow, hook.Allow},
		{`pulumi destroy --preview-only=true`, hook.Allow, hook.Allow},
		{`pulumi destroy --preview-only=false`, hook.Ask, hook.Deny},
		{`cdktf deploy --auto-approve=false`, hook.Allow, hook.Allow},
		{`npx aws-cdk synth`, hook.Allow, hook.Allow},
		{`npx aws-cdk@2 deploy`, hook.Allow, hook.Allow},
		{`pnpm install`, hook.Allow, hook.Allow},
		{`pnpm run build`, hook.Allow, hook.Allow},
		{`yarn add aws-cdk`, hook.Allow, hook.Allow},
		{`npm exec -- prettier --write .`, hook.Allow, hook.Allow},
		{`npm install -g aws-cdk`, hook.Allow, hook.Allow},
		{`pulumi stack rm -h`, hook.Allow, hook.Allow},
		{`cdk deploy`, hook.Allow, hook.Allow},
		{`cdk deploy --require-approval broadening`, hook.Allow, hook.Allow},
		{`cdk deploy --require-approval=any-change`, hook.Allow, hook.Allow},
		{`cdk deploy StackDestroy`, hook.Allow, hook.Allow},
		{`cdk synth`, hook.Allow, hook.Allow},
		{`cdk diff`, hook.Allow, hook.Allow},
		{`cdk ls`, hook.Allow, hook.Allow},
		{`cdk bootstrap`, hook.Allow, hook.Allow},
		{`cdk synth destroy`, hook.Allow, hook.Allow},
		{`sam build`, hook.Allow, hook.Allow},
		{`sam deploy --guided`, hook.Allow, hook.Allow},
		{`sam local invoke`, hook.Allow, hook.Allow},
		{`eksctl get cluster`, hook.Allow, hook.Allow},
		{`eksctl create cluster --name app`, hook.Allow, hook.Allow},
		{`eksctl utils write-kubeconfig --cluster app`, hook.Allow, hook.Allow},
		{`pulumi up`, hook.Allow, hook.Allow},
		{`pulumi preview`, hook.Allow, hook.Allow},
		{`pulumi destroy --preview-only`, hook.Allow, hook.Allow},
		{`pulumi stack rm`, hook.Allow, hook.Allow},
		{`pulumi stack ls`, hook.Allow, hook.Allow},
		{`pulumi state ls`, hook.Allow, hook.Allow},
		{`sls deploy`, hook.Allow, hook.Allow},
		{`serverless invoke -f hello`, hook.Allow, hook.Allow},
		{`cdktf synth`, hook.Allow, hook.Allow},
		{`cdktf deploy`, hook.Allow, hook.Allow},
		{`echo cdk destroy`, hook.Allow, hook.Allow},
		{`npx prettier --write .`, hook.Allow, hook.Allow},
		{`npx`, hook.Allow, hook.Allow},
	})
}

func TestIaCClassAndInfra(t *testing.T) {
	for command, want := range map[string]string{
		`npx cdk destroy`:        "cdk",
		`pulumi up --yes`:        "pulumi",
		`sam delete`:             "sam",
		`eksctl delete cluster`:  "eksctl",
		`sls remove`:             "sls",
		`cdktf destroy`:          "cdktf",
		`serverless remove`:      "serverless",
		`./deploy-s3cr3t.sh cdk`: "outro",
	} {
		if got := Classify(command); got != want {
			t.Errorf("Classify(%q) = %q, esperava %q", command, got, want)
		}
	}
	for _, command := range []string{`cdk destroy`, `npx cdk deploy`, `pulumi up`, `sam delete`, `eksctl get cluster`} {
		if !TouchesInfra(command) {
			t.Errorf("TouchesInfra(%q) deveria ser verdadeiro", command)
		}
	}
}
