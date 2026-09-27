package setup

import (
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSettingsPath(t *testing.T) {
	want := filepath.Join("/projeto", ".claude", "settings.json")

	if got := SettingsPath("/projeto"); got != want {
		t.Errorf("esperava %q, obtive %q", want, got)
	}
}

func TestFindHooksMissingFile(t *testing.T) {
	path := newSettingsPath(t, "")

	_, err := FindHooks(path)

	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("esperava um erro fs.ErrNotExist, obtive %v", err)
	}
}

func TestFindHooksInvalidFile(t *testing.T) {
	path := newSettingsPath(t, `{"hooks": `)

	if _, err := FindHooks(path); err == nil {
		t.Error("esperava um erro para JSON inválido")
	}
}

func TestFindHooks(t *testing.T) {
	// Só o que o init instala conta como "hook do Iron Brake": matcher Bash,
	// binário chamado iron e args ["hook"].
	cases := []struct {
		name    string
		content string
		want    []Hook
	}{
		{"arquivo sem hooks", `{}`, nil},
		{
			"hook de outra ferramenta",
			`{"hooks":{"PreToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"/x/fmt.sh"}]}]}}`,
			nil,
		},
		{
			"iron no matcher errado",
			`{"hooks":{"PreToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"/x/iron","args":["hook"]}]}]}}`,
			nil,
		},
		{
			"binário com outro nome",
			`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/bin/sh","args":["hook"]}]}]}}`,
			nil,
		},
		{
			"iron com outros argumentos",
			`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/x/iron","args":["init"]}]}]}}`,
			nil,
		},
		{
			"dois hooks, na ordem do arquivo",
			`{"hooks":{"PreToolUse":[
				{"matcher":"Bash","hooks":[{"type":"command","command":"/old/iron","args":["hook"]}]},
				{"matcher":"Bash","hooks":[{"type":"command","command":"/new/iron","args":["hook"],"timeout":30}]}
			]}}`,
			[]Hook{{Command: "/old/iron"}, {Command: "/new/iron", Timeout: 30}},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := newSettingsPath(t, c.content)

			got, err := FindHooks(path)

			if err != nil {
				t.Fatalf("erro inesperado: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("esperava %v, obtive %v", c.want, got)
			}
		})
	}
}

func TestFindHooksRecognizesWhatInitInstalls(t *testing.T) {
	path := newSettingsPath(t, existingSettings)
	if _, err := InstallHook(path, testExe); err != nil {
		t.Fatal(err)
	}

	got, err := FindHooks(path)

	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if want := []Hook{{Command: testExe}}; !reflect.DeepEqual(got, want) {
		t.Errorf("esperava %v, obtive %v", want, got)
	}
}
