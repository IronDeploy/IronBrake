package shieldui

// Key é uma tecla que a tela interativa entende. Setas esquerda/direita
// contam como cima/baixo — algumas configurações de terminal mapeiam Tab e
// setas de forma diferente, e não custa aceitar as duas.
type Key int

const (
	KeyOther Key = iota
	KeyUp
	KeyDown
	KeyEnter
	KeyEsc
	KeyQuit // 'q' — saída alternativa para quem não confia no Esc do terminal
)

// DecodeKey lê os bytes crus de uma tecla (já não-canônicos, sem terminador)
// e devolve a ação e quantos bytes ela consumiu de buf. Sequências de seta são
// ESC '[' 'A'..'D' (padrão ANSI/VT100, o que todo terminal de verdade manda).
// Um ESC sozinho (sem mais bytes ainda disponíveis) é KeyEsc — quem chama
// decide como esperar o resto da sequência.
func DecodeKey(buf []byte) (Key, int) {
	if len(buf) == 0 {
		return KeyOther, 0
	}

	switch buf[0] {
	case '\r', '\n':
		return KeyEnter, 1
	case 'q', 'Q':
		return KeyQuit, 1
	case 0x1b: // ESC
		if len(buf) >= 3 && buf[1] == '[' {
			switch buf[2] {
			case 'A', 'D': // cima, esquerda
				return KeyUp, 3
			case 'B', 'C': // baixo, direita
				return KeyDown, 3
			}
			return KeyOther, 3
		}
		return KeyEsc, 1
	default:
		return KeyOther, 1
	}
}
