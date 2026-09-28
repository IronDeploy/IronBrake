package rules

// registry lista as regras que o CheckAll roda. O terraform apply fica fora
// porque precisa ler o plano (InspectTerraformApply).
var registry = []Rule{
	gitForcePush,
	gitPushDelete,
	gitDiscard,
	terraformDestroy,
	terraformState,
	kubectlDelete,
	kubectlDeleteFile,
	helmDestructive,
	cloudDelete,
	sqlDestructive,
	fsCatastrophic,
	fsDangerous,
	remoteCode,
	containerDelete,
	systemDestructive,
	publishDestructive,
}
