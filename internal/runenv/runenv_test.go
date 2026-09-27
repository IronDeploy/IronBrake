package runenv

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"
)

func fakeFiles(files map[string]string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		content, ok := files[path]
		if !ok {
			return nil, fs.ErrNotExist
		}
		return []byte(content), nil
	}
}

func fakeEnv(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func TestCollectEverything(t *testing.T) {
	files := fakeFiles(map[string]string{
		"/proj/infra/.terraform/environment": "prod-eu\n",
		"/home/ana/.kube/config":             "apiVersion: v1\ncurrent-context: eks-prd\ncontexts: []\n",
	})
	env := fakeEnv(map[string]string{
		"HOME":         "/home/ana",
		"TF_WORKSPACE": "blue",
		"AWS_PROFILE":  "empresa-production",
	})

	got := Collect("/proj/infra", env, files)

	for _, want := range []string{"/proj/infra", "prod-eu", "eks-prd", "blue", "empresa-production"} {
		if !slices.Contains(got, want) {
			t.Errorf("faltou %q em %q", want, got)
		}
	}
}

func TestCollectKubeconfigFromEnv(t *testing.T) {
	files := fakeFiles(map[string]string{
		"/b/config": "current-context: prd-cluster\n",
	})
	env := fakeEnv(map[string]string{"KUBECONFIG": "/a/nao-existe:/b/config"})

	if got := Collect("/proj", env, files); !slices.Contains(got, "prd-cluster") {
		t.Errorf("faltou o contexto do kubectl: %q", got)
	}
}

func TestCollectWithNothingAvailable(t *testing.T) {
	got := Collect("/proj", fakeEnv(nil), fakeFiles(nil))

	if !slices.Equal(got, []string{"/proj"}) {
		t.Errorf("esperava só a pasta, obtive %q", got)
	}
}

func TestCollectIgnoresBrokenKubeconfig(t *testing.T) {
	files := fakeFiles(map[string]string{"/home/ana/.kube/config": "current-context: [quebrado\n"})
	env := fakeEnv(map[string]string{"HOME": "/home/ana"})

	got := Collect("/proj", env, files)

	if !slices.Equal(got, []string{"/proj"}) {
		t.Errorf("esperava só a pasta, obtive %q", got)
	}
}

func TestCollectIgnoresReadErrors(t *testing.T) {
	failing := func(string) ([]byte, error) { return nil, errors.New("sem permissão") }
	env := fakeEnv(map[string]string{"HOME": "/home/ana"})

	if got := Collect("/proj", env, failing); !slices.Equal(got, []string{"/proj"}) {
		t.Errorf("esperava só a pasta, obtive %q", got)
	}
}

func BenchmarkCollectLargeKubeconfig(b *testing.B) {
	var config strings.Builder
	config.WriteString("apiVersion: v1\ncurrent-context: cluster-49\nclusters:\n")
	cert := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo=", 60) // ~2 KB, como um certificado
	for i := range 50 {
		fmt.Fprintf(&config, "- name: cluster-%d\n  cluster:\n    server: https://c%d.example.com\n    certificate-authority-data: %s\n", i, i, cert)
	}
	files := fakeFiles(map[string]string{"/home/ana/.kube/config": config.String()})
	env := fakeEnv(map[string]string{"HOME": "/home/ana"})
	b.SetBytes(int64(config.Len()))

	for b.Loop() {
		Collect("/proj", env, files)
	}
}
