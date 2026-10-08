package detect

import (
	"os"
	"sort"
	"testing"
)

// TestScanRealRepository measures what envmove would do to a real checkout.
//
// Skipped unless ENVMOVE_SCAN points at a repository, because the point of the
// exclusion list is that it is tuned against real ones: a repository with forty
// thousand ignored files is where a filename-matching design falls over.
//
//	ENVMOVE_SCAN=~/dev/project go test ./internal/detect/ -run Real -v
func TestScanRealRepository(t *testing.T) {
	root := os.Getenv("ENVMOVE_SCAN")
	if root == "" {
		t.Skip("set ENVMOVE_SCAN to a repository path")
	}
	if testing.Short() {
		t.Skip("scans the filesystem")
	}

	found, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}

	var carried, skipped []string
	reasons := map[string]int{}
	for _, c := range found {
		if c.Class == Carry {
			carried = append(carried, c.Path)
		} else {
			skipped = append(skipped, c.Path)
			reasons[c.Reason]++
		}
	}
	sort.Strings(carried)

	t.Logf("%d gitignored: %d carried, %d skipped", len(found), len(carried), len(skipped))
	t.Log("carried:")
	for _, p := range carried {
		t.Log("   " + p)
	}
	t.Log("skipped by reason:")
	keys := make([]string, 0, len(reasons))
	for r := range reasons {
		keys = append(keys, r)
	}
	sort.Slice(keys, func(i, j int) bool { return reasons[keys[i]] > reasons[keys[j]] })
	for _, r := range keys {
		t.Logf("   %-34s %d", r, reasons[r])
	}

	// Whatever the repository, nothing in the exclusion list may ever travel.
	for _, c := range carried {
		if _, bad := exclude(c, &ignoreList{}); bad {
			t.Errorf("%q carried but the exclusion list says otherwise", c)
		}
	}
}
