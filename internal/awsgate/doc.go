// Package awsgate decide se um agente de código pode receber credenciais da
// AWS. Ele é a ponta do "credential_process" do ~/.aws/config: o SDK (aws,
// terraform, boto3, cdk...) roda "iron aws-creds -- <comando real>" toda vez
// que precisa de credenciais, e o Iron Brake só as entrega depois de descobrir,
// pelos processos ancestrais, se quem pediu foi um agente.
//
// Humano (nenhum agente entre os ancestrais) passa direto. Agente em conta que
// não é produção passa e fica registrado. Agente em produção só recebe com a
// confirmação do usuário na janela, que vale por um tempo.
//
// Limite: o agente roda com o mesmo usuário. Ele pode ler as credenciais reais
// por outro caminho (arquivos, cache do SSO, "aws configure export-credentials")
// e pode forjar a janela de aprovação. O portão pega o uso do perfil como
// configurado, o engano e o loop, não um agente que tenta burlar.
package awsgate
