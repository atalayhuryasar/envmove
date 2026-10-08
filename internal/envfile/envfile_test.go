package envfile

import (
	"strings"
	"testing"
)

// The single most important property: a real value must never reach the example file.
func TestBuildNeverCopiesRealValues(t *testing.T) {
	real := "SECRET=hunter2\nDB_URL=postgres://user:pw@host/db\n"
	got := Build(real, "").Content

	for _, secret := range []string{"hunter2", "pw@host"} {
		if strings.Contains(got, secret) {
			t.Fatalf("a real value leaked into the example file: %q\n%q", secret, got)
		}
	}
	if !strings.Contains(got, "SECRET=") || !strings.Contains(got, "DB_URL=") {
		t.Errorf("keys are missing:\n%s", got)
	}
	if strings.Contains(got, "SECRET=\n") == false {
		t.Errorf("values must be empty:\n%s", got)
	}
}

func TestBuildNeverCopiesCommentsFromRealFile(t *testing.T) {
	real := "# the prod master password, ask ops\nSECRET=x\n"
	got := Build(real, "").Content
	if strings.Contains(got, "prod master password") {
		t.Errorf("a comment from the real file leaked:\n%s", got)
	}
}

func TestBuildKeepsExistingCommentsAndPlaceholders(t *testing.T) {
	real := "PORT=3000\nDB_HOST=localhost\n"
	prev := "# how to run it\nPORT=8080\n\n# database target\nDB_HOST=\nSTALE=x\n"

	got := Build(real, prev)
	if !strings.Contains(got.Content, "# how to run it") {
		t.Errorf("mevcut yorum kayboldu:\n%s", got.Content)
	}
	if !strings.Contains(got.Content, "# database target") {
		t.Errorf("mevcut yorum kayboldu:\n%s", got.Content)
	}
	if !strings.Contains(got.Content, "PORT=8080") {
		t.Errorf("the placeholder in the example file must survive:\n%s", got.Content)
	}
	if strings.Contains(got.Content, "STALE") {
		t.Errorf("a variable that is not in .env must be gone:\n%s", got.Content)
	}
}

func TestBuildReportsAdditionsAndRemovals(t *testing.T) {
	got := Build("A=1\nB=2\n", "A=1\nGONE=3\n")
	if len(got.Added) != 1 || got.Added[0] != "B" {
		t.Errorf("Added = %v, beklenen [B]", got.Added)
	}
	if len(got.Removed) != 1 || got.Removed[0] != "GONE" {
		t.Errorf("Removed = %v, beklenen [GONE]", got.Removed)
	}
}

func TestDanglingCommentIsRemovedWithItsKey(t *testing.T) {
	got := Build("A=1\n", "# about the removed thing\nGONE=3\nA=1\n")
	if strings.Contains(got.Content, "about the removed thing") {
		t.Errorf("the comment of a deleted variable remained:\n%s", got.Content)
	}
}

func TestSplitPairKeepsHashInsideValues(t *testing.T) {
	_, value, _ := splitPair("PASSWORD=abc#123")
	if value != "abc#123" {
		t.Errorf("value = %q, the # must stay part of the value", value)
	}
	_, value, _ = splitPair("PORT=3000 # the port")
	if value != "3000" {
		t.Errorf("value = %q, the comment should be separated", value)
	}
	_, value, _ = splitPair(`TOKEN="a # b"`)
	if value != `"a # b"` {
		t.Errorf("value = %q, quoted text must be preserved", value)
	}
}

func TestMissing(t *testing.T) {
	got := Missing("A=1\n", "A=1\nB=2\nC=3\n")
	if len(got) != 2 || got[0] != "B" || got[1] != "C" {
		t.Errorf("Missing = %v, beklenen [B C]", got)
	}
}

func TestKeysPreserveOrderAndDeduplicate(t *testing.T) {
	got := Keys("B=1\nA=2\nB=3\n")
	if len(got) != 2 || got[0] != "B" || got[1] != "A" {
		t.Errorf("Keys = %v, order must be kept and duplicates dropped", got)
	}
}
