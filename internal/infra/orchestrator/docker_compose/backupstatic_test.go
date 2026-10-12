package dockercompose

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeELF writes a minimal ELF64 file with one program header per given
// type.
func writeELF(t *testing.T, types ...elf.ProgType) string {
	t.Helper()
	h := elf.Header64{
		Type: uint16(elf.ET_EXEC), Machine: uint16(elf.EM_X86_64), Version: 1,
		Phoff: 64, Ehsize: 64, Phentsize: 56, Phnum: uint16(len(types)),
	}
	copy(h.Ident[:], []byte{0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)})
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, h)
	for _, pt := range types {
		binary.Write(&buf, binary.LittleEndian, elf.Prog64{Type: uint32(pt), Flags: uint32(elf.PF_R)})
	}
	path := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(path, buf.Bytes(), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// The helper runs this binary in alpine, which has no glibc loader: a
// dynamically linked build (PT_INTERP) is refused up front instead of
// failing every run with "exec /wf: no such file or directory".
func TestCheckStatic(t *testing.T) {
	if err := checkStatic(writeELF(t, elf.PT_LOAD)); err != nil {
		t.Errorf("static: %v", err)
	}
	if err := checkStatic(writeELF(t, elf.PT_INTERP, elf.PT_LOAD)); !errors.Is(err, errNotStatic) ||
		!strings.Contains(err.Error(), "CGO_ENABLED=0") {
		t.Errorf("dynamic: err = %v", err)
	}
	notELF := filepath.Join(t.TempDir(), "script")
	os.WriteFile(notELF, []byte("#!/bin/sh\n"), 0o755)
	if err := checkStatic(notELF); err == nil || !strings.Contains(err.Error(), "not a Linux executable") {
		t.Errorf("not an ELF: err = %v", err)
	}
	if err := checkStatic(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing file: no error")
	}
}

// The verdict for this process is computed once and cached.
func TestAgentExecutableCachesTheVerdict(t *testing.T) {
	exe1, err1 := agentExecutable()
	exe2, err2 := agentExecutable()
	if exe1 != exe2 || (err1 == nil) != (err2 == nil) {
		t.Fatalf("(%q, %v) then (%q, %v)", exe1, err1, exe2, err2)
	}
	if exe1 == "" {
		t.Fatal("no executable path")
	}
}
