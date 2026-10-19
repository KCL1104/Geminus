package upload

import (
	"bytes"
	"path"
	"strings"
)

// SniffBytes is how much of a file Sniff looks at.
const SniffBytes = 512

// signature is how an archive or executable format begins.
type signature struct {
	name   string
	offset int
	magic  []byte
}

// The archive and executable formats an import refuses (P0, SECURITY §6).
// Text files never start with these bytes: all but the last have bytes
// that are not printable text, and tar's mark ends in NUL.
var signatures = []signature{
	{"ZIP (also XLSX, DOCX, JAR)", 0, []byte("PK\x03\x04")},
	{"ZIP (also XLSX, DOCX, JAR)", 0, []byte("PK\x05\x06")},
	{"ZIP (also XLSX, DOCX, JAR)", 0, []byte("PK\x07\x08")},
	{"gzip", 0, []byte{0x1f, 0x8b}},
	{"bzip2", 0, []byte("BZh")}, // completed by the block mark below
	{"xz", 0, []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}},
	{"zstd", 0, []byte{0x28, 0xb5, 0x2f, 0xfd}},
	{"7z", 0, []byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}},
	{"RAR", 0, []byte("Rar!\x1a\x07")},
	{"tar", 257, []byte("ustar\x00")},
	{"tar", 257, []byte("ustar  \x00")},
	{"OLE (XLS, DOC, MSI)", 0, []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}},
	{"ELF", 0, []byte{0x7f, 'E', 'L', 'F'}},
	{"Mach-O", 0, []byte{0xfe, 0xed, 0xfa, 0xce}},
	{"Mach-O", 0, []byte{0xfe, 0xed, 0xfa, 0xcf}},
	{"Mach-O", 0, []byte{0xce, 0xfa, 0xed, 0xfe}},
	{"Mach-O", 0, []byte{0xcf, 0xfa, 0xed, 0xfe}},
	{"Mach-O or Java class", 0, []byte{0xca, 0xfe, 0xba, 0xbe}},
	{"WebAssembly", 0, []byte("\x00asm")},
}

// refusedExtensions are file name extensions of archives and executables.
var refusedExtensions = map[string]bool{
	".zip": true, ".gz": true, ".tgz": true, ".bz2": true, ".tbz2": true, ".xz": true, ".txz": true, ".zst": true, ".7z": true,
	".rar": true, ".tar": true, ".jar": true, ".war": true, ".apk": true, ".xlsx": true, ".xlsm": true, ".xls": true,
	".docx": true, ".doc": true, ".exe": true, ".dll": true, ".so": true, ".dylib": true, ".msi": true, ".com": true,
	".bat": true, ".cmd": true, ".ps1": true, ".sh": true, ".bin": true, ".elf": true, ".wasm": true, ".app": true,
	".dmg": true, ".iso": true, ".scr": true, ".vbs": true, ".js": true, ".py": true,
}

// Sniff reports which refused format a file is, from its name and its
// first SniffBytes bytes, or "" when it is none of them. It does not say
// the file is valid CSV or JSON: profiling does.
func Sniff(name string, head []byte) string {
	if ext := strings.ToLower(path.Ext(name)); refusedExtensions[ext] {
		return "a " + ext + " file"
	}
	for _, s := range signatures {
		if len(head) >= s.offset+len(s.magic) && bytes.Equal(head[s.offset:s.offset+len(s.magic)], s.magic) {
			if s.name == "bzip2" && !(len(head) >= 10 && head[3] >= '1' && head[3] <= '9' && bytes.Equal(head[4:10], []byte("1AY&SY"))) {
				continue
			}
			return s.name
		}
	}
	// A PE (Windows) executable starts "MZ" and has NUL bytes soon after.
	if bytes.HasPrefix(head, []byte("MZ")) && bytes.IndexByte(head[:min(len(head), 64)], 0) >= 0 {
		return "a Windows executable"
	}
	// A script that names its interpreter.
	if bytes.HasPrefix(head, []byte("#!/")) || bytes.HasPrefix(head, []byte("#! /")) {
		return "a script"
	}
	return ""
}
