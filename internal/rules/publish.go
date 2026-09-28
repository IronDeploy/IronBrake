package rules

const (
	publishDanger   = "publicar um pacote é irreversível: a versão fica pública e não dá para republicar a mesma versão."
	unpublishDanger = "npm unpublish remove uma versão já publicada e quebra quem depende dela."
)

var publishDestructive = dangerRule{name: "publish", match: matchPublish}

func matchPublish(tokens []string) (string, bool) {
	if len(tokens) == 0 {
		return "", false
	}
	pos := positionals(tokens[1:], nil)
	if len(pos) == 0 {
		return "", false
	}
	sub := pos[0]

	switch programName(tokens[0]) {
	case "npm", "pnpm", "yarn", "bun":
		switch sub {
		case "publish":
			return publishDanger, true
		case "unpublish":
			return unpublishDanger, true
		}
	case "cargo":
		if sub == "publish" {
			return publishDanger, true
		}
	case "gem":
		if sub == "push" {
			return publishDanger, true
		}
	case "twine":
		if sub == "upload" {
			return publishDanger, true
		}
	}
	return "", false
}
