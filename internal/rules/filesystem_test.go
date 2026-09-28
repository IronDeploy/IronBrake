package rules

import (
	"testing"

	"github.com/IronDeploy/IronBrake/internal/hook"
)

func TestCatastrophicFS(t *testing.T) {
	runRuleCases(t, fsCatastrophic, []ruleCase{
		// rm recursivo em caminho crítico: deny em qualquer ambiente.
		{`rm -rf /`, hook.Deny, hook.Deny},
		{`rm -rf /*`, hook.Deny, hook.Deny},
		{`rm -rf --no-preserve-root /`, hook.Deny, hook.Deny},
		{`rm -fr ~`, hook.Deny, hook.Deny},
		{`rm -rf $HOME`, hook.Deny, hook.Deny},
		{`rm -rf ${HOME}/`, hook.Deny, hook.Deny},
		{`rm -rf /etc`, hook.Deny, hook.Deny},
		{`rm -rf /usr/`, hook.Deny, hook.Deny},
		{`rm -rf /var/*`, hook.Deny, hook.Deny},
		{`rm --recursive --force /System`, hook.Deny, hook.Deny},
		{`rm -r -- /`, hook.Deny, hook.Deny},
		{`rm -rf .`, hook.Deny, hook.Deny},

		// dd escrevendo em disco.
		{`dd if=/dev/zero of=/dev/sda bs=1M`, hook.Deny, hook.Deny},
		{`dd if=img.iso of=/dev/disk2`, hook.Deny, hook.Deny},
		{`dd if=/dev/urandom of=/dev/nvme0n1`, hook.Deny, hook.Deny},

		// Formatar / destruir dispositivo.
		{`mkfs.ext4 /dev/sdb1`, hook.Deny, hook.Deny},
		{`mkfs -t xfs /dev/sda1`, hook.Deny, hook.Deny},
		{`wipefs -a /dev/sda`, hook.Deny, hook.Deny},
		{`shred -n 3 /dev/sda`, hook.Deny, hook.Deny},

		// chmod/chown recursivo em caminho de sistema.
		{`chmod -R 777 /`, hook.Deny, hook.Deny},
		{`chown -R nobody /etc`, hook.Deny, hook.Deny},

		// Redirecionar a saída direto para um disco.
		{`echo x > /dev/sda`, hook.Deny, hook.Deny},
		{`cat imagem.iso > /dev/disk2`, hook.Deny, hook.Deny},
		{`echo 0 >/dev/nvme0n1`, hook.Deny, hook.Deny},
		{`cat img >> /dev/sdb`, hook.Deny, hook.Deny},
		{`dd if=in of=out 2>/dev/sda`, hook.Deny, hook.Deny},

		// Inofensivos: allow em qualquer ambiente.
		{`rm -rf build`, hook.Allow, hook.Allow},
		{`rm -rf node_modules`, hook.Allow, hook.Allow},
		{`rm -rf ./dist`, hook.Allow, hook.Allow},
		{`rm -rf /tmp/cache`, hook.Allow, hook.Allow},
		{`rm arquivo.txt`, hook.Allow, hook.Allow},
		{`rm -r /home/ana/loja/tmp`, hook.Allow, hook.Allow},
		{`dd if=/dev/zero of=disco.img bs=1M count=100`, hook.Allow, hook.Allow},
		{`dd if=in.bin of=/dev/null`, hook.Allow, hook.Allow},
		{`chmod -R 755 ./scripts`, hook.Allow, hook.Allow},
		{`chmod 777 /etc/hosts`, hook.Allow, hook.Allow}, // sem -R não é catastrófico
		{`echo rm -rf /`, hook.Allow, hook.Allow},
		{`echo x > saida.txt`, hook.Allow, hook.Allow},
		{`echo x > /dev/null`, hook.Allow, hook.Allow},
		{`cat log 2>&1`, hook.Allow, hook.Allow},
		{`echo x > /tmp/dev/sda`, hook.Allow, hook.Allow}, // não é /dev/
	})
}

func TestDangerousFS(t *testing.T) {
	runRuleCases(t, fsDangerous, []ruleCase{
		// find SEM filtro de nome apaga tudo sob o caminho: perigoso.
		{`find . -delete`, hook.Ask, hook.Deny},
		{`find /var/log -type f -delete`, hook.Ask, hook.Deny},
		{`find build -exec rm {} ;`, hook.Ask, hook.Deny},
		{`shred segredo.txt`, hook.Ask, hook.Deny},

		// find COM filtro de nome/caminho é limpeza rotineira: allow.
		{`find . -name '*.log' -delete`, hook.Allow, hook.Allow},
		{`find . -name '*.pyc' -delete`, hook.Allow, hook.Allow},
		{`find . -path './cache/*' -delete`, hook.Allow, hook.Allow},
		{`find . -name '*.tmp' -exec rm {} ;`, hook.Allow, hook.Allow},

		{`find . -name '*.go'`, hook.Allow, hook.Allow},
		{`find . -type f -print`, hook.Allow, hook.Allow},
	})
}
