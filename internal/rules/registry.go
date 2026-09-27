package rules

// registry lista as regras que o CheckAll roda. O terraform apply fica fora
// porque precisa ler o plano (InspectTerraformApply).
var registry = []Rule{
	gitForcePush,
	gitDiscard,
	terraformDestroy,
	kubectlDelete,
	cloudDelete,
	sqlDestructive,
}
