package pathx

import (
	"runtime"
	"testing"
)

func TestIsAbs(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"", false},
		{"build", false},
		{"./build", false},
		{"../outro", false},
		{"/data/prod", true}, // com raiz: absoluto em Unix e "com raiz" no Windows
		{"a/b", false},
		{"C:/dados", true}, // letra de unidade: absoluto em qualquer sistema
		{`c:\dados`, true},
		{"C:", false},
		{"C:dados", false},
	}
	for _, c := range cases {
		if got := IsAbs(c.path); got != c.want {
			t.Errorf("IsAbs(%q) = %v, quero %v", c.path, got, c.want)
		}
	}
	if runtime.GOOS == "windows" {
		for path, want := range map[string]bool{`C:\x`: true, `C:/x`: true, `\x`: true, `C:x`: false, `x\y`: false} {
			if got := IsAbs(path); got != want {
				t.Errorf("IsAbs(%q) = %v, quero %v", path, got, want)
			}
		}
	}
}
