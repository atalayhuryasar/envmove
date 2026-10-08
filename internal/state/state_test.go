package state

import "testing"

func TestContractReplacesRepoRoot(t *testing.T) {
	roots := []string{"/private/tmp/envmove-e2e/B", "/tmp/envmove-e2e/B"}
	home := "/Users/atalay"

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"resolved root", "/private/tmp/envmove-e2e/B", repoVar},
		{"symlinked root", "/tmp/envmove-e2e/B", repoVar},
		{"under root", "/tmp/envmove-e2e/B/sub/file.txt", repoVar + "/sub/file.txt"},
		{"under home", "/Users/atalay/dev/app/.env", homeVar + "/dev/app/.env"},
		{"unrelated", "/opt/other/thing", "/opt/other/thing"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Contract(tc.in, roots, home); got != tc.want {
				t.Errorf("Contract(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestExpandUsesFirstRoot(t *testing.T) {
	roots := []string{"/private/tmp/envmove-e2e/A", "/tmp/envmove-e2e/A"}
	got := Expand("REPO_ROOT="+repoVar+"\nX="+homeVar+"/x", roots, "/Users/atalay")
	want := "REPO_ROOT=/private/tmp/envmove-e2e/A\nX=/Users/atalay/x"
	if got != want {
		t.Errorf("Expand() = %q, want %q", got, want)
	}
}

func TestRoundTrip(t *testing.T) {
	srcRoots := []string{"/private/tmp/envmove-e2e/B", "/tmp/envmove-e2e/B"}
	dstRoots := []string{"/private/tmp/envmove-e2e/A", "/tmp/envmove-e2e/A"}
	in := "REPO_ROOT=/tmp/envmove-e2e/B\nSECRET=abc\n"

	out := Expand(Contract(in, srcRoots, "/Users/atalay"), dstRoots, "/Users/atalay")
	want := "REPO_ROOT=/private/tmp/envmove-e2e/A\nSECRET=abc\n"
	if out != want {
		t.Errorf("round trip = %q, want %q", out, want)
	}
}

func TestMarshalUnmarshal(t *testing.T) {
	s := &State{
		Version:    Version,
		Recipients: []string{"age1aaa", "age1bbb"},
		Files: map[string]Entry{
			".env": {Mode: "0644", SHA256: Sum([]byte("x")), Content: []byte("SECRET=1\n")},
		},
	}
	raw, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Files[".env"].Content) != "SECRET=1\n" {
		t.Errorf("content = %q", got.Files[".env"].Content)
	}
	if !got.SameRecipients([]string{"age1bbb", "age1aaa"}) {
		t.Error("SameRecipients should ignore order")
	}
	if got.SameRecipients([]string{"age1aaa"}) {
		t.Error("SameRecipients should detect a new recipient")
	}
}

func TestIsText(t *testing.T) {
	if !IsText([]byte("hello\n")) {
		t.Error("plain text should be text")
	}
	if IsText([]byte{0x00, 0x01, 0x02}) {
		t.Error("binary should not be text")
	}
}
