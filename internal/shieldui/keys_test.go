package shieldui

import "testing"

func TestDecodeKeyBasic(t *testing.T) {
	cases := []struct {
		name     string
		in       []byte
		wantKey  Key
		wantUsed int
	}{
		{"enter CR", []byte{'\r'}, KeyEnter, 1},
		{"enter LF", []byte{'\n'}, KeyEnter, 1},
		{"q", []byte{'q'}, KeyQuit, 1},
		{"Q maiúsculo", []byte{'Q'}, KeyQuit, 1},
		{"seta cima", []byte{0x1b, '[', 'A'}, KeyUp, 3},
		{"seta baixo", []byte{0x1b, '[', 'B'}, KeyDown, 3},
		{"seta direita conta como baixo", []byte{0x1b, '[', 'C'}, KeyDown, 3},
		{"seta esquerda conta como cima", []byte{0x1b, '[', 'D'}, KeyUp, 3},
		{"esc sozinho", []byte{0x1b}, KeyEsc, 1},
		{"tecla qualquer", []byte{'x'}, KeyOther, 1},
		{"vazio", nil, KeyOther, 0},
		{"escape sequence desconhecida", []byte{0x1b, '[', 'Z'}, KeyOther, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key, used := DecodeKey(c.in)
			if key != c.wantKey || used != c.wantUsed {
				t.Errorf("DecodeKey(%v) = %v, %d; esperava %v, %d", c.in, key, used, c.wantKey, c.wantUsed)
			}
		})
	}
}
