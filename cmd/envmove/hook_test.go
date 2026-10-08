package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A hook has to survive a Homebrew upgrade. Homebrew deletes the previous Cellar
// directory, so a hook pointing at /opt/homebrew/Cellar/envmove/0.3.1/bin/envmove is
// broken the moment 0.3.2 is installed, and the failure shows up as commits that no
// longer carry context, not as an error anyone sees.
//
// Getting this backwards is easy and was exactly what happened: keeping the baked path
// and merely deprioritising it leaves it in the list, still ahead of nothing, still
// there to be used if the others fail.
func TestHookScriptDropsTheCellarPath(t *testing.T) {
	body := hookScript("/opt/homebrew/Cellar/envmove/0.3.1/bin/envmove", "")

	for _, want := range []string{
		"command -v envmove", // PATH fallback
		"exit 0",             // never block a commit
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the hook is missing %q", want)
		}
	}
	if strings.Contains(body, "/opt/homebrew/Cellar/") {
		t.Errorf("the hook still carries a Cellar path, which an upgrade deletes:\n%s", body)
	}
	if opt := homebrewOptPath(); opt != "" && !strings.Contains(body, opt) {
		t.Errorf("the hook does not use the stable opt path %q", opt)
	}
}

// A build from source has to keep using itself. Silently handing the work to an older
// brew install means a fix you just built never runs, and you cannot tell from outside.
func TestHookPrefersANonCellarInstall(t *testing.T) {
	self := "/Users/someone/dev/envmove/bin/envmove"
	body := hookScript(self, "")
	if !strings.Contains(body, self) {
		t.Errorf("the hook dropped its own install path: %s", body)
	}
	if strings.Contains(body, "/Cellar/") {
		t.Errorf("the hook mentions a Cellar path: %s", body)
	}
	if opt := homebrewOptPath(); opt != "" && strings.Index(body, self) > strings.Index(body, opt) {
		t.Errorf("the opt path is tried before the binary that wrote the hook: %s", body)
	}
}

// The whole point of the fallback chain is that it runs. Test it against a real shell
// rather than trusting that the string looks right.
func TestHookFallsBackToPath(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "envmove")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho ran\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(dir, "hook")
	body := hookScriptWithCandidates("", []string{"/nonexistent/envmove", `$(command -v envmove 2>/dev/null)`})
	if err := os.WriteFile(hook, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sh", hook)
	cmd.Env = append(os.Environ(), "PATH="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the hook failed when envmove was only on PATH: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "ran") {
		t.Errorf("the hook did not run envmove from PATH; it printed: %s", out)
	}
}

// A missing envmove must not make the repository uncommittable. A hook that blocks
// every commit gets removed by the person it is blocking, and then the sync is lost for
// good. Degrading loudly is strictly better.
func TestHookWithNoEnvmoveAnywhereStillExitsZero(t *testing.T) {
	dir := t.TempDir()
	hook := filepath.Join(dir, "hook")
	if err := os.WriteFile(hook, []byte(hookScriptWithCandidates("", []string{"/nonexistent/envmove"})), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sh", hook)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("a missing envmove blocked the commit: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "not found") {
		t.Errorf("the hook should say what is wrong, it printed: %s", out)
	}
}

// pre-push has to stop itself from recursing: its own sync runs git push, which runs
// pre-push again.
func TestPrePushHookStopsRecursion(t *testing.T) {
	body := hookScript("/usr/bin/envmove", "ENVMOVE_NO_PUSH=1 ")
	if !strings.Contains(body, "ENVMOVE_NO_PUSH=1") {
		t.Error("the pre-push hook must set ENVMOVE_NO_PUSH")
	}
	if !strings.Contains(body, "exec \"$self\"") {
		t.Error("the hook must exec the resolved binary")
	}
}

func TestResolveHookBinary(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "envmove")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	notExec := filepath.Join(dir, "plainfile")
	if err := os.WriteFile(notExec, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "first executable wins",
			body: "for candidate in /nonexistent/a " + real + " /nonexistent/b; do",
			want: real,
		},
		{
			name: "quoted candidates",
			body: `for candidate in "/nope/a" "` + real + `"; do`,
			want: real,
		},
		{
			name: "a non-executable file is not a candidate",
			body: "for candidate in " + notExec + " " + real + "; do",
			want: real,
		},
		{
			name: "nothing executable",
			body: "for candidate in /nope/a /nope/b; do",
			want: "",
		},
		{
			name: "not a generated hook",
			body: "exec envmove sync",
			want: "",
		},
		{
			name: "empty",
			body: "",
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveHookBinary(tc.body); got != tc.want {
				t.Errorf("resolveHookBinary() = %q, want %q", got, tc.want)
			}
		})
	}
}
