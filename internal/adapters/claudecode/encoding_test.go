package claudecode

import (
	"strings"
	"testing"
)

// TestPS1HooksSetUTF8Encoding pins the #134 fix. PowerShell 5.1 — the
// `powershell` these hooks are registered under — defaults every encoding
// on the hook path to a non-UTF-8 code page while the envelope is UTF-8:
//
//	[Console]::InputEncoding   decodes stdin              (OEM, IBM437)
//	[Console]::OutputEncoding  decodes a child's stdout   (same OEM page)
//	$OutputEncoding            encodes what we pipe out   (ASCII)
//
// Measured on Windows 11 26200 before the fix: an envelope carrying
// `git log --grep=café` produced `updatedInput.command` holding
// `git log --grep=caf└⌐`, so Claude Code ran a command the user never
// wrote. Fail-open cannot help — a corrupted-but-valid command is
// indistinguishable from a correct one.
//
// A dropped line here is silent on every non-ASCII-free test input, so
// assert on the embedded bytes rather than on behaviour.
func TestPS1HooksSetUTF8Encoding(t *testing.T) {
	hooks := map[string][]byte{
		"hook.ps1":       hookScriptPS1,
		"hook-read.ps1":  hookReadScriptPS1,
		"hook-write.ps1": hookWriteScriptPS1,
	}
	want := []string{
		// Encodes what we pipe to a native command.
		"$OutputEncoding = [System.Text.UTF8Encoding]::new($false)",
		// Decodes a native command's stdout. try-guarded: the assignment
		// calls SetConsoleOutputCP, which throws with no console attached.
		"try { [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false) } catch { }",
		// Decodes stdin. [Console]::In would use [Console]::InputEncoding.
		"[Console]::OpenStandardInput()",
	}
	for name, body := range hooks {
		s := string(body)
		for _, w := range want {
			if !strings.Contains(s, w) {
				t.Errorf("%s is missing the UTF-8 line %q", name, w)
			}
		}
		// The OEM-decoding read must be gone, not merely shadowed.
		if strings.Contains(s, "[Console]::In.ReadToEnd()") {
			t.Errorf("%s still reads stdin via [Console]::In, which decodes with the OEM code page", name)
		}
	}
}
