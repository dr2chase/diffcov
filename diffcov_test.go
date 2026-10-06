// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package diffcov_test

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dr2chase/diffcov"
)

func TestDetectVCS(t *testing.T) {
	tmpDir := t.TempDir()

	// Empty dir: should fail to detect VCS
	_, err := diffcov.DetectVCS(tmpDir)
	if err == nil {
		t.Errorf("expected error for empty dir, got nil")
	}

	// Git repo: only .git exists
	gitDir := filepath.Join(tmpDir, "gitrepo")
	if err := os.MkdirAll(filepath.Join(gitDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	vcs, err := diffcov.DetectVCS(gitDir)
	if err != nil {
		t.Fatalf("unexpected error for git dir: %v", err)
	}
	if vcs != "git" {
		t.Errorf("expected 'git', got %q", vcs)
	}

	// Subdirectory inside Git repo
	subGit := filepath.Join(gitDir, "a", "b", "c")
	if err := os.MkdirAll(subGit, 0755); err != nil {
		t.Fatal(err)
	}
	vcs, err = diffcov.DetectVCS(subGit)
	if err != nil {
		t.Fatalf("unexpected error for sub git dir: %v", err)
	}
	if vcs != "git" {
		t.Errorf("expected 'git', got %q", vcs)
	}

	// JJ repo: only .jj exists
	jjDir := filepath.Join(tmpDir, "jjrepo")
	if err := os.MkdirAll(filepath.Join(jjDir, ".jj"), 0755); err != nil {
		t.Fatal(err)
	}
	vcs, err = diffcov.DetectVCS(jjDir)
	if err != nil {
		t.Fatalf("unexpected error for jj dir: %v", err)
	}
	if vcs != "jj" {
		t.Errorf("expected 'jj', got %q", vcs)
	}

	// Subdirectory inside JJ repo
	subJJ := filepath.Join(jjDir, "x", "y")
	if err := os.MkdirAll(subJJ, 0755); err != nil {
		t.Fatal(err)
	}
	vcs, err = diffcov.DetectVCS(subJJ)
	if err != nil {
		t.Fatalf("unexpected error for sub jj dir: %v", err)
	}
	if vcs != "jj" {
		t.Errorf("expected 'jj', got %q", vcs)
	}

	// Colocated repo: both .jj and .git exist
	colocatedDir := filepath.Join(tmpDir, "colocated")
	if err := os.MkdirAll(filepath.Join(colocatedDir, ".jj"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(colocatedDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	vcs, err = diffcov.DetectVCS(colocatedDir)
	if err != nil {
		t.Fatalf("unexpected error for colocated dir: %v", err)
	}
	// If jj executable is in PATH, it should prefer jj; otherwise git.
	expected := "git"
	if _, err := exec.LookPath("jj"); err == nil {
		expected = "jj"
	}
	if vcs != expected {
		t.Errorf("expected %q for colocated repo, got %q", expected, vcs)
	}

	// Git repo with .git as file (e.g. worktree or submodule)
	worktreeDir := filepath.Join(tmpDir, "worktree")
	if err := os.MkdirAll(worktreeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreeDir, ".git"), []byte("gitdir: /some/path"), 0644); err != nil {
		t.Fatal(err)
	}
	vcs, err = diffcov.DetectVCS(worktreeDir)
	if err != nil {
		t.Fatalf("unexpected error for worktree dir: %v", err)
	}
	if vcs != "git" {
		t.Errorf("expected 'git' for worktree .git file, got %q", vcs)
	}
}

func TestDoDiffs(t *testing.T) {
	diffBytes, err := os.ReadFile("testdata/sample.diff")
	if err != nil {
		t.Fatal(err)
	}
	// Run DoDiffs with sample data
	diffcov.DoDiffs(diffBytes, "testdata/sample.cover", ".", ".", 0, 0, false, false)
}

func TestDoDiffsNestedModulesAndStd(t *testing.T) {
	// Simulate Go repo layout:
	// <root>/src/go.mod (module std)
	// <root>/src/simd/simd.go
	// <root>/src/cmd/go.mod (module cmd)
	// <root>/src/cmd/compile/internal/midway/rewrite.go
	rootDir := t.TempDir()
	simdDir := filepath.Join(rootDir, "src", "simd")
	midwayDir := filepath.Join(rootDir, "src", "cmd", "compile", "internal", "midway")
	if err := os.MkdirAll(simdDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(midwayDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "src", "go.mod"), []byte("module std\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "src", "cmd", "go.mod"), []byte("module cmd\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rewriteGo := `package midway

func Rewrite(x int) int {
	if x > 0 {
		return x + 1
	} else {
		return x - 1
	}
}
`
	if err := os.WriteFile(filepath.Join(midwayDir, "rewrite.go"), []byte(rewriteGo), 0644); err != nil {
		t.Fatal(err)
	}

	simdGo := `package simd

func Helper(y int) int {
	if y > 0 {
		return y * 2
	}
	return 0
}
`
	if err := os.WriteFile(filepath.Join(simdDir, "simd.go"), []byte(simdGo), 0644); err != nil {
		t.Fatal(err)
	}

	diffText := `diff --git a/src/cmd/compile/internal/midway/rewrite.go b/src/cmd/compile/internal/midway/rewrite.go
index 1111111..2222222 100644
--- a/src/cmd/compile/internal/midway/rewrite.go
+++ b/src/cmd/compile/internal/midway/rewrite.go
@@ -1,2 +1,9 @@
 package midway
+
+func Rewrite(x int) int {
+	if x > 0 {
+		return x + 1
+	} else {
+		return x - 1
+	}
+}
diff --git a/src/simd/simd.go b/src/simd/simd.go
index 3333333..4444444 100644
--- a/src/simd/simd.go
+++ b/src/simd/simd.go
@@ -1,2 +1,8 @@
 package simd
+
+func Helper(y int) int {
+	if y > 0 {
+		return y * 2
+	}
+	return 0
+}
`

	// In coverprofile:
	// - rewrite.go line 4-5 (if x > 0 { return x + 1 }) is covered (count 1), line 7 (return x - 1) is uncovered (count 0)
	// - simd.go line 4-5 (if y > 0 { return y * 2 }) is covered (count 1), line 7 (return 0) is uncovered (count 0)
	coverText := `mode: set
cmd/compile/internal/midway/rewrite.go:4.2,4.11 1 1
cmd/compile/internal/midway/rewrite.go:5.3,6.1 1 1
cmd/compile/internal/midway/rewrite.go:7.3,8.1 1 0
simd/simd.go:4.2,4.11 1 1
simd/simd.go:5.3,6.1 1 1
simd/simd.go:7.2,7.10 1 0
`
	coverPath := filepath.Join(simdDir, "asdf.out")
	if err := os.WriteFile(coverPath, []byte(coverText), 0644); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origWd)
	if err := os.Chdir(simdDir); err != nil {
		t.Fatal(err)
	}

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	diffcov.DoDiffs([]byte(diffText), "asdf.out", "", "", 0, 0, false, true)

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if strings.Contains(out, "return x + 1") {
		t.Errorf("expected 'return x + 1' to be tested, got output:\n%s", out)
	}
	if !strings.Contains(out, "return x - 1") {
		t.Errorf("expected 'return x - 1' to be untested, got output:\n%s", out)
	}
	if strings.Contains(out, "func Rewrite") || strings.Contains(out, "} else {") {
		t.Errorf("expected func/else headers not to be flagged as untested, got output:\n%s", out)
	}
	if strings.Contains(out, "return y * 2") {
		t.Errorf("expected 'return y * 2' to be tested, got output:\n%s", out)
	}
	if !strings.Contains(out, "return 0") {
		t.Errorf("expected 'return 0' to be untested, got output:\n%s", out)
	}
}
