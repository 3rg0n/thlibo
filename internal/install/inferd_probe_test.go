package install

import "testing"

// TestParseAdminSnapshot_V2Capabilities locks the #52-follow-up fix:
// inferd v2's admin socket writes a multi-frame snapshot that leads with
// one `capabilities` advertisement frame per loaded backend before the
// real lifecycle frame. The probe must skip `capabilities` and report
// the lifecycle status (`ready`), not surface "inferd is capabilities".
//
// The input is the *exact* 3-frame snapshot captured from a running
// inferd v0.5.0 admin pipe (embeddinggemma-300m + gemma-4-e4b caps, then
// ready).
func TestParseAdminSnapshot_V2Capabilities(t *testing.T) {
	snapshot := []byte(`{"accelerator":"cpu","audio":false,"backend":"embeddinggemma-300m","embed":true,"gpu_layers":0,"id":"admin","status":"capabilities","thinking":true,"tools":true,"type":"status","v2":true,"vision":false,"wire_version":1}
{"accelerator":"cpu","audio":true,"backend":"gemma-4-e4b","embed":false,"gpu_layers":0,"id":"admin","status":"capabilities","thinking":true,"tools":true,"type":"status","v2":true,"vision":true,"wire_version":1}
{"id":"admin","status":"ready","type":"status"}
`)
	status, _ := parseAdminSnapshot(snapshot)
	if status != "ready" {
		t.Fatalf("status = %q, want %q (capabilities frames must be skipped)", status, "ready")
	}
}

func TestParseAdminSnapshot(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantStatus string
		wantVer    string
	}{
		{
			name:       "single ready frame (v1-style)",
			in:         `{"id":"admin","type":"status","status":"ready"}` + "\n",
			wantStatus: "ready",
		},
		{
			name: "capabilities then loading_model — report the lifecycle state",
			in: `{"id":"admin","status":"capabilities","backend":"gemma-4-e4b","type":"status"}` + "\n" +
				`{"id":"admin","type":"status","status":"loading_model","phase":"download"}` + "\n",
			wantStatus: "loading_model",
		},
		{
			name:       "only capabilities frames, no lifecycle yet — empty status, still reachable",
			in:         `{"id":"admin","status":"capabilities","backend":"x","type":"status"}` + "\n",
			wantStatus: "",
		},
		{
			// Forward-compat: a hypothetical future advertisement frame
			// thlibo has never seen must NOT be reported as a status.
			// This is why the implementation allowlists lifecycle states
			// rather than denylisting "capabilities" (#54 review).
			name: "unknown future advertisement frame is skipped, ready still reported",
			in: `{"id":"admin","status":"some_future_advert","backend":"z","type":"status"}` + "\n" +
				`{"id":"admin","status":"ready","type":"status"}` + "\n",
			wantStatus: "ready",
		},
		{
			name:       "loading_model is a real lifecycle state and IS reported",
			in:         `{"status":"capabilities"}` + "\n" + `{"status":"loading_model","phase":"mmap"}` + "\n",
			wantStatus: "loading_model",
		},
		{
			name:       "version carried on a frame is captured",
			in:         `{"id":"admin","type":"status","status":"ready","version":"v0.5.0"}` + "\n",
			wantStatus: "ready",
			wantVer:    "v0.5.0",
		},
		{
			name: "trailing partial frame (no newline) is ignored",
			in: `{"id":"admin","type":"status","status":"ready"}` + "\n" +
				`{"id":"admin","status":"capa`, // truncated mid-read
			wantStatus: "ready",
		},
		{
			name:       "last lifecycle frame wins (ready after restarting)",
			in:         `{"status":"restarting"}` + "\n" + `{"status":"ready"}` + "\n",
			wantStatus: "ready",
		},
		{
			name:       "garbage line is skipped, valid frame still parsed",
			in:         "not json at all\n" + `{"status":"ready"}` + "\n",
			wantStatus: "ready",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, version := parseAdminSnapshot([]byte(tc.in))
			if status != tc.wantStatus {
				t.Errorf("status = %q, want %q", status, tc.wantStatus)
			}
			if version != tc.wantVer {
				t.Errorf("version = %q, want %q", version, tc.wantVer)
			}
		})
	}
}

func TestSplitFrames(t *testing.T) {
	got := splitFrames([]byte("a\nb\n\n  c  \ntrailing-no-newline"))
	// Expect: "a", "b", "c" (blank line dropped, whitespace trimmed,
	// trailing fragment without a newline dropped).
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %d frames %q, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Errorf("frame[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
