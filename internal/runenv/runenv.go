package runenv

import (
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

var envVars = []string{
	"TF_WORKSPACE", "AWS_PROFILE", "AWS_DEFAULT_PROFILE",
	"CLOUDSDK_ACTIVE_CONFIG_NAME", "CLOUDSDK_CORE_PROJECT",
}

// Collect devolve os textos do contexto atual. Fonte ausente ou quebrada só
// não entra na lista.
func Collect(cwd string, getenv func(string) string, readFile func(string) ([]byte, error)) []string {
	texts := []string{cwd}

	// terraform workspace select grava o nome aqui.
	if data, err := readFile(filepath.Join(cwd, ".terraform", "environment")); err == nil {
		texts = appendText(texts, string(data))
	}

	if context := kubeContext(getenv, readFile); context != "" {
		texts = append(texts, context)
	}

	for _, name := range envVars {
		texts = appendText(texts, getenv(name))
	}
	return texts
}

// kubeContext lê o current-context do primeiro arquivo do KUBECONFIG que
// tiver um, ou de ~/.kube/config.
func kubeContext(getenv func(string) string, readFile func(string) ([]byte, error)) string {
	paths := filepath.SplitList(getenv("KUBECONFIG"))
	if len(paths) == 0 && getenv("HOME") != "" {
		paths = []string{filepath.Join(getenv("HOME"), ".kube", "config")}
	}

	for _, path := range paths {
		data, err := readFile(path)
		if err != nil {
			continue
		}
		var config struct {
			CurrentContext string `yaml:"current-context"`
		}
		if yaml.Unmarshal(data, &config) == nil && config.CurrentContext != "" {
			return config.CurrentContext
		}
	}
	return ""
}

func appendText(texts []string, text string) []string {
	if text = strings.TrimSpace(text); text != "" {
		return append(texts, text)
	}
	return texts
}
