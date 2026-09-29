package rules

// registry lista as regras que o CheckAll roda. O terraform apply fica fora
// porque precisa ler o plano (InspectTerraformApply).
var registry = []Rule{
	gitForcePush,
	gitPushDelete,
	gitPushVar,
	gitDiscard,
	terraformDestroy,
	terraformState,
	kubectlDelete,
	kubectlDeleteFile,
	kubectlDeleteWorkload,
	helmDestructive,
	cloudDelete,
	sqlDestructive,
	fsCatastrophic,
	fsDangerous,
	rmOutside,
	unresolvedCommand,
	scriptContent,
	remoteCode,
	sdkDelete,
	containerDelete,
	systemDestructive,
	publishDestructive,
}
