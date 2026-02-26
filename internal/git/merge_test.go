package git

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFakeBinaryForGitMergeTests(t *testing.T, command string, content string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, command)
	if err := os.WriteFile(scriptPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake command %s: %v", command, err)
	}
	return dir, scriptPath
}

func TestMergePR(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "gh-args.log")
	binDir, _ := writeFakeBinaryForGitMergeTests(t, "gh", "#!/bin/sh\n"+
		"echo \"$@\" >> \"$GH_MERGE_ARGS\"\n"+
		"exit 0\n")
	t.Setenv("GH_MERGE_ARGS", logPath)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	t.Run("supports supported methods", func(t *testing.T) {
		cases := []struct {
			method string
			flag   string
		}{
			{"squash", "--squash"},
			{"merge", "--merge"},
			{"rebase", "--rebase"},
		}
		for _, tt := range cases {
			t.Run(tt.method, func(t *testing.T) {
				_ = os.WriteFile(logPath, []byte{}, 0o644)
				if err := MergePR(t.TempDir(), 123, tt.method); err != nil {
					t.Fatalf("MergePR failed: %v", err)
				}
				got := strings.TrimSpace(readFileOrT(t, logPath))
				want := "pr merge 123 " + tt.flag
				if got != want {
					t.Fatalf("unexpected gh args: %q, want %q", got, want)
				}
			})
		}
	})

	t.Run("rejects empty workspace", func(t *testing.T) {
		if err := MergePR("", 123, "merge"); err == nil || !strings.Contains(err.Error(), "workspace is required") {
			t.Fatalf("expected workspace validation error, got: %v", err)
		}
	})

	t.Run("rejects invalid method", func(t *testing.T) {
		if err := MergePR(t.TempDir(), 123, "invalid"); err == nil || !strings.Contains(err.Error(), "unsupported merge method") {
			t.Fatalf("expected unsupported merge method error, got: %v", err)
		}
	})

	t.Run("rejects invalid PR number", func(t *testing.T) {
		if err := MergePR(t.TempDir(), 0, "merge"); err == nil || !strings.Contains(err.Error(), "invalid PR number") {
			t.Fatalf("expected invalid PR number error, got: %v", err)
		}
	})
}

func TestMergePR_FailureMode_ReturnsDescriptiveError(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "gh-args.log")
	binDir, _ := writeFakeBinaryForGitMergeTests(t, "gh", "#!/bin/sh\n"+
		"echo \"$@\" >> \"$GH_MERGE_ARGS\"\n"+
		"echo merge failed >&2\n"+
		"exit 2\n")
	t.Setenv("GH_MERGE_ARGS", logPath)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	err := MergePR(t.TempDir(), 555, "merge")
	if err == nil {
		t.Fatal("expected merge command failure")
	}

	gotErr := err.Error()
	for _, want := range []string{
		"failed to merge PR #555 using \"merge\"",
		"merge failed",
		"exit status 2",
	} {
		if !strings.Contains(gotErr, want) {
			t.Fatalf("expected error to contain %q, got %q", want, gotErr)
		}
	}

	got := strings.TrimSpace(readFileOrT(t, logPath))
	if got != "pr merge 555 --merge" {
		t.Fatalf("unexpected gh args: %q", got)
	}
}

func TestRevertMerge(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "git-args.log")
	binDir, _ := writeFakeBinaryForGitMergeTests(t, "git", "#!/bin/sh\n"+
		"echo \"$@\" >> \"$GIT_REVERT_ARGS\"\n"+
		"echo ok\n"+
		"exit 0\n")
	t.Setenv("GIT_REVERT_ARGS", logPath)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	if err := RevertMerge(t.TempDir(), "abc123"); err != nil {
		t.Fatalf("RevertMerge failed: %v", err)
	}

	got := strings.Split(strings.TrimSpace(readFileOrT(t, logPath)), "\n")
	if len(got) < 2 {
		t.Fatalf("expected revert + push commands, got %v", got)
	}
	if got[0] != "revert abc123 --no-edit" {
		t.Fatalf("expected first command %q, got %q", "revert abc123 --no-edit", got[0])
	}
	if got[1] != "push" {
		t.Fatalf("expected second command %q, got %q", "push", got[1])
	}

	t.Run("rejects empty workspace", func(t *testing.T) {
		if err := RevertMerge("", "abc123"); err == nil || !strings.Contains(err.Error(), "workspace is required") {
			t.Fatalf("expected workspace validation error, got: %v", err)
		}
	})

	t.Run("rejects empty commit SHA", func(t *testing.T) {
		if err := RevertMerge(t.TempDir(), "   "); err == nil || !strings.Contains(err.Error(), "commit SHA is required") {
			t.Fatalf("expected commit SHA validation error, got: %v", err)
		}
	})
}

func TestRevertMerge_FailureModes(t *testing.T) {
	t.Run("returns revert failure before push", func(t *testing.T) {
		logPath := filepath.Join(t.TempDir(), "git-args.log")
		binDir, _ := writeFakeBinaryForGitMergeTests(t, "git", "#!/bin/sh\n"+
			"echo \"$@\" >> \"$GIT_REVERT_ARGS\"\n"+
			"if [ \"$1\" = \"revert\" ] && [ \"$2\" = \"abc123\" ]; then\n"+
			"  echo revert failed >&2\n"+
			"  exit 3\n"+
			"fi\n"+
			"echo ok\n"+
			"exit 0\n")
		t.Setenv("GIT_REVERT_ARGS", logPath)
		t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

		err := RevertMerge(t.TempDir(), "abc123")
		if err == nil {
			t.Fatal("expected revert command failure")
		}
		if !strings.Contains(err.Error(), "failed to revert commit abc123") || !strings.Contains(err.Error(), "revert failed") || !strings.Contains(err.Error(), "exit status 3") {
			t.Fatalf("unexpected error: %v", err)
		}

		got := strings.Split(strings.TrimSpace(readFileOrT(t, logPath)), "\n")
		if len(got) != 1 {
			t.Fatalf("expected only revert command before failure, got %v", got)
		}
		if got[0] != "revert abc123 --no-edit" {
			t.Fatalf("expected first command %q, got %q", "revert abc123 --no-edit", got[0])
		}
	})

	t.Run("returns push failure after revert", func(t *testing.T) {
		logPath := filepath.Join(t.TempDir(), "git-args.log")
		binDir, _ := writeFakeBinaryForGitMergeTests(t, "git", "#!/bin/sh\n"+
			"echo \"$@\" >> \"$GIT_REVERT_ARGS\"\n"+
			"if [ \"$1\" = \"push\" ]; then\n"+
			"  echo push failed >&2\n"+
			"  exit 4\n"+
			"fi\n"+
			"exit 0\n")
		t.Setenv("GIT_REVERT_ARGS", logPath)
		t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

		err := RevertMerge(t.TempDir(), "abc123")
		if err == nil {
			t.Fatal("expected push command failure")
		}
		if !strings.Contains(err.Error(), "failed to push revert of commit abc123") || !strings.Contains(err.Error(), "push failed") || !strings.Contains(err.Error(), "exit status 4") {
			t.Fatalf("unexpected error: %v", err)
		}

		got := strings.Split(strings.TrimSpace(readFileOrT(t, logPath)), "\n")
		if len(got) != 2 {
			t.Fatalf("expected revert + push commands, got %v", got)
		}
		if got[1] != "push" {
			t.Fatalf("expected second command %q, got %q", "push", got[1])
		}
	})
}

func TestRunPostMergeChecks(t *testing.T) {
	binDir, _ := writeFakeBinaryForGitMergeTests(t, "go", "#!/bin/sh\n"+
		"if [ \"$1\" = \"test\" ] && [ \"$2\" = \"./...\" ]; then\n"+
		"  exit 0\n"+
		"fi\n"+
		"if [ \"$1\" = \"vet\" ] && [ \"$2\" = \"./...\" ]; then\n"+
		"  exit 1\n"+
		"fi\n"+
		"echo \"unknown\" >&2\n"+
		"exit 2\n")
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	workspace := t.TempDir()

	result, err := RunPostMergeChecks(workspace, []string{
		"go test ./...",
		"go vet ./...",
	})
	if err != nil {
		t.Fatalf("RunPostMergeChecks failed: %v", err)
	}
	if result.Passed {
		t.Fatal("expected checks to fail due second command")
	}
	if len(result.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(result.Checks))
	}
	if got := []bool{result.Checks[0].Passed, result.Checks[1].Passed}; !reflect.DeepEqual(got, []bool{true, false}) {
		t.Fatalf("check pass states = %v, want [true false]", got)
	}
	if len(result.Failures) != 1 {
		t.Fatalf("expected one failure, got %d", len(result.Failures))
	}
	if !result.Checks[1].Passed && result.Checks[1].ExitCode == 0 {
		t.Fatalf("expected second check exit code non-zero, got %d", result.Checks[1].ExitCode)
	}
}

func TestRunPostMergeChecks_UsesShell(t *testing.T) {
	result, err := RunPostMergeChecks(t.TempDir(), []string{
		"printf \"shell-ok\"",
	})
	if err != nil {
		t.Fatalf("RunPostMergeChecks failed: %v", err)
	}
	if !result.Passed {
		t.Fatalf("expected checks to pass: %v", result.Failures)
	}
	if len(result.Checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(result.Checks))
	}
	if !result.Checks[0].Passed {
		t.Fatalf("expected check to pass")
	}
	if result.Checks[0].Output != "shell-ok" {
		t.Fatalf("expected command output %q, got %q", "shell-ok", result.Checks[0].Output)
	}
}

func TestAugmentPATH_WhenMissingPath_UsesSystemFallback(t *testing.T) {
	env := []string{"FOO=bar"}
	got := augmentPATH(env)

	var pathVal string
	for _, e := range got {
		if strings.HasPrefix(e, "PATH=") {
			pathVal = strings.TrimPrefix(e, "PATH=")
			break
		}
	}
	if pathVal == "" {
		t.Fatal("expected PATH to be added when missing")
	}

	for _, want := range []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin", "/usr/local/bin", "/usr/local/go/bin"} {
		if !strings.Contains(pathVal, want) {
			t.Fatalf("expected PATH fallback to include %q, got %q", want, pathVal)
		}
	}
}

func readFileOrT(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
