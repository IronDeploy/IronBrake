package toolpath

import (
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// No Windows não há bits de modo: quem pode alterar o arquivo está na ACL
// (lista de controle de acesso). Um programa só é confiável se nenhum grupo
// amplo (Todos, Usuários Autenticados, Usuários) puder gravar nele nem na
// pasta dele. O dono, o SYSTEM e os Administradores podem: é o mesmo
// "só o dono (ou o administrador) altera" do Unix.
const (
	fileWriteData   = 0x00000002 // gravar no arquivo; em pasta, criar arquivo
	fileAppendData  = 0x00000004
	fileWriteEA     = 0x00000010
	fileDeleteChild = 0x00000040 // em pasta, apagar ou renomear o que há dentro
	deleteRight     = 0x00010000
	writeDAC        = 0x00040000 // mudar a própria ACL
	writeOwner      = 0x00080000
	genericWrite    = 0x40000000
	genericAll      = 0x10000000

	writeMask = fileWriteData | fileAppendData | fileWriteEA | fileDeleteChild |
		deleteRight | writeDAC | writeOwner | genericWrite | genericAll

	inheritOnlyACE = 0x08 // a regra vale só para o que está dentro, não para o objeto
)

// executableExts são as extensões que o Windows executa direto.
var executableExts = map[string]bool{".exe": true, ".com": true, ".bat": true, ".cmd": true}

// checkWindows é o checkFile do Windows: programa executável, que nem ele
// nem a pasta dele podem ser alterados por grupos amplos.
func checkWindows(tool, real string) (string, error) {
	if !executableExts[strings.ToLower(filepath.Ext(real))] {
		return "", &UntrustedError{tool, real, "não é um programa executável (.exe, .com, .bat ou .cmd)"}
	}
	if wide, err := writableByWideGroups(real); err != nil {
		return "", &UntrustedError{tool, real, "não consegui ler as permissões do arquivo"}
	} else if wide {
		return "", &UntrustedError{tool, real, "qualquer usuário pode alterar o arquivo"}
	}
	if wide, err := writableByWideGroups(filepath.Dir(real)); err != nil {
		return "", &UntrustedError{tool, real, "não consegui ler as permissões da pasta"}
	} else if wide {
		return "", &UntrustedError{tool, real, "qualquer usuário pode trocar o arquivo (a pasta é gravável por todos)"}
	}
	return real, nil
}

// writableByWideGroups diz se a ACL de path dá gravação a Todos, Usuários
// Autenticados ou Usuários. Sem ACL (DACL nula) o Windows libera tudo a todos.
func writableByWideGroups(path string) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return false, err
	}
	if dacl == nil {
		return true, nil
	}

	var wide []*windows.SID
	for _, kind := range []windows.WELL_KNOWN_SID_TYPE{windows.WinWorldSid, windows.WinAuthenticatedUserSid, windows.WinBuiltinUsersSid} {
		sid, err := windows.CreateWellKnownSid(kind)
		if err != nil {
			return false, err
		}
		wide = append(wide, sid)
	}

	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return false, err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
			ace.Header.AceFlags&inheritOnlyACE != 0 ||
			uint32(ace.Mask)&writeMask == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		for _, w := range wide {
			if windows.EqualSid(sid, w) {
				return true, nil
			}
		}
	}
	return false, nil
}
