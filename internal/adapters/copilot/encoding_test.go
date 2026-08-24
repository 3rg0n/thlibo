package copilot

import (
	"strings"
	"testing"
)

// TestPS1HooksSetUTF8Encoding pins the #134 fix for both Copilot hooks.
// PowerShell 5.1 defaults [Console]::InputEncoding and
// [Console]::OutputEncoding to the OEM code page (IBM437 on a default box)
// and $OutputEncoding to ASCII, while the hook envelope is UTF-8.
//
// The consequence differs per hook and both are real. hook-pre.ps1 returns
// modifiedArgs, so a corrupted command is the command Copilot runs.
// hook-post.ps1 pipes tool output to `thlibo compress`, and measured
// before the fix, "cafe"+acute reached the child as `63 61 66 3f 3f` —
// one "?" per source byte.
//
// Assert on the embedded bytes: a dropped line is silent on any test input
// that happens to be ASCII.
func TestPS1HooksSetUTF8Encoding(t *testing.T) {
	hooks := map[string][]byte{
		"hook-pre.ps1":  preHookPS1,
		"hook-post.ps1": postHookPS1,
	}
	want := []string{
		"$OutputEncoding = [System.Text.UTF8Encoding]::new($false)",
		"try { [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false) } catch { }",
		"[Console]::OpenStandardInput()",
	}
	for name, body := range hooks {
		s := string(body)
		for _, w := range want {
			if !strings.Contains(s, w) {
				t.Errorf("%s is missing the UTF-8 line %q", name, w)
			}
		}
		if strings.Contains(s, "[Console]::In.ReadToEnd()") {
			t.Errorf("%s still reads stdin via [Console]::In, which decodes with the OEM code page", name)
		}
	}
}
