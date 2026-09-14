// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	buildOnce   sync.Once
	diffcovPath string
	buildErr    error
)

func getDiffcovBin(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "diffcov-bin-*")
		if err != nil {
			buildErr = err
			return
		}
		diffcovPath = filepath.Join(tmpDir, "diffcov")
		cmd := exec.Command("go", "build", "-o", diffcovPath, ".")
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = err
			t.Logf("go build output:\n%s", string(out))
		}
	})
	if buildErr != nil {
		t.Fatalf("failed to build diffcov: %v", buildErr)
	}
	return diffcovPath
}

func TestFlagUsage(t *testing.T) {
	bin := getDiffcovBin(t)
	cmd := exec.Command(bin, "-h")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run() // flag -h exits with 2

	out := stderr.String()
	for _, expected := range []string{
		"-git",
		"-jj",
		"-vcs",
		"-staged",
		"jj diff --git",
	} {
		if !strings.Contains(out, expected) {
			t.Errorf("usage output missing %q, output:\n%s", expected, out)
		}
	}
}

func TestConflictingFlags(t *testing.T) {
	bin := getDiffcovBin(t)

	// Both -git and -jj
	cmd := exec.Command(bin, "-git", "-jj")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected error when passing -git and -jj, got success")
	}
	if !strings.Contains(string(out), "cannot specify both -git and -jj") {
		t.Errorf("expected conflict error, got:\n%s", string(out))
	}

	// Invalid -vcs
	cmd = exec.Command(bin, "-vcs", "invalid")
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected error when passing invalid -vcs, got success")
	}
	if !strings.Contains(string(out), "unsupported revision control system") {
		t.Errorf("expected unsupported vcs error, got:\n%s", string(out))
	}

	// Conflicting -git and -vcs=jj
	cmd = exec.Command(bin, "-git", "-vcs=jj")
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected error when passing conflicting -git and -vcs=jj, got success")
	}
	if !strings.Contains(string(out), "conflicting revision control flags") {
		t.Errorf("expected conflicting flags error, got:\n%s", string(out))
	}

	// Too many arguments
	cmd = exec.Command(bin, "diff1", "diff2")
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected error with too many arguments, got success")
	}
	if !strings.Contains(string(out), "too many arguments") {
		t.Errorf("expected too many arguments error, got:\n%s", string(out))
	}

	// Both -d and positional argument
	cmd = exec.Command(bin, "-d", "diff1", "diff2")
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected error with both -d and positional arg, got success")
	}
	if !strings.Contains(string(out), "cannot specify diff file both") {
		t.Errorf("expected duplicate diff file error, got:\n%s", string(out))
	}
}

func TestStagedWithJJDiagnosed(t *testing.T) {
	bin := getDiffcovBin(t)

	// Explicit -jj with --staged
	cmd := exec.Command(bin, "-jj", "--staged")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected error with -jj and --staged, got success")
	}
	if !strings.Contains(string(out), "--staged/--cached cannot be used with jj (jujutsu does not have a staging area)") {
		t.Errorf("expected staging area misuse diagnosis, got:\n%s", string(out))
	}

	// Explicit -jj with --cached
	cmd = exec.Command(bin, "-jj", "--cached")
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected error with -jj and --cached, got success")
	}
	if !strings.Contains(string(out), "--staged/--cached cannot be used with jj (jujutsu does not have a staging area)") {
		t.Errorf("expected staging area misuse diagnosis, got:\n%s", string(out))
	}
}

func TestGitWorkflow(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	bin := getDiffcovBin(t)
	tmpDir := t.TempDir()

	// Initialize git repository
	runCmd(t, tmpDir, "git", "init")
	runCmd(t, tmpDir, "git", "config", "user.name", "Test User")
	runCmd(t, tmpDir, "git", "config", "user.email", "test@example.com")

	// Create go.mod and a simple go file
	goMod := "module example.com/testpkg\n\ngo 1.20\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}
	mainGo := `package testpkg

func Add(a, b int) int {
	return a + b
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0644); err != nil {
		t.Fatal(err)
	}
	testGo := `package testpkg

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fail()
	}
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main_test.go"), []byte(testGo), 0644); err != nil {
		t.Fatal(err)
	}

	runCmd(t, tmpDir, "git", "add", ".")
	runCmd(t, tmpDir, "git", "commit", "-m", "initial commit")

	// Add an untested function to main.go
	modifiedGo := `package testpkg

func Add(a, b int) int {
	return a + b
}

func Sub(a, b int) int {
	return a - b
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(modifiedGo), 0644); err != nil {
		t.Fatal(err)
	}

	// Run diffcov (autodetect git)
	cmd := exec.Command(bin)
	cmd.Dir = tmpDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("diffcov failed in git repo: %v, output:\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "Untested: main.go") || !strings.Contains(string(out), "return a - b") {
		t.Errorf("expected untested report for Sub, got:\n%s", string(out))
	}

	// Stage the change and run with --staged
	runCmd(t, tmpDir, "git", "add", "main.go")
	cmd = exec.Command(bin, "--staged")
	cmd.Dir = tmpDir
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("diffcov --staged failed in git repo: %v, output:\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "Untested: main.go") || !strings.Contains(string(out), "return a - b") {
		t.Errorf("expected untested report for Sub with --staged, got:\n%s", string(out))
	}
}

func TestJJWorkflow(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj not found in PATH")
	}

	bin := getDiffcovBin(t)
	tmpDir := t.TempDir()

	// Initialize jj repo
	runCmd(t, tmpDir, "jj", "git", "init", ".")

	// Create go.mod and a simple go file
	goMod := "module example.com/testjj\n\ngo 1.20\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}
	mainGo := `package testjj

func Mul(a, b int) int {
	return a * b
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0644); err != nil {
		t.Fatal(err)
	}
	testGo := `package testjj

import "testing"

func TestMul(t *testing.T) {
	if Mul(2, 3) != 6 {
		t.Fail()
	}
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main_test.go"), []byte(testGo), 0644); err != nil {
		t.Fatal(err)
	}

	// Commit initial state in jj
	runCmd(t, tmpDir, "jj", "commit", "-m", "initial commit")

	// Add an untested function in working copy
	modifiedGo := `package testjj

func Mul(a, b int) int {
	return a * b
}

func Div(a, b int) int {
	return a / b
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(modifiedGo), 0644); err != nil {
		t.Fatal(err)
	}

	// Run diffcov with autodetection (should autodetect jj)
	cmd := exec.Command(bin)
	cmd.Dir = tmpDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("diffcov failed in jj repo: %v, output:\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "Untested: main.go") || !strings.Contains(string(out), "return a / b") {
		t.Errorf("expected untested report for Div in jj repo, got:\n%s", string(out))
	}

	// Explicit -jj flag should also work
	cmd = exec.Command(bin, "-jj")
	cmd.Dir = tmpDir
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("diffcov -jj failed in jj repo: %v, output:\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "Untested: main.go") || !strings.Contains(string(out), "return a / b") {
		t.Errorf("expected untested report for Div with -jj, got:\n%s", string(out))
	}

	// --staged in jj repo should diagnose misuse even without explicit -jj flag
	cmd = exec.Command(bin, "--staged")
	cmd.Dir = tmpDir
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected diffcov --staged to fail in jj repo, but got success")
	}
	if !strings.Contains(string(out), "--staged/--cached cannot be used with jj (jujutsu does not have a staging area)") {
		t.Errorf("expected staging area misuse diagnosis in jj repo, got:\n%s", string(out))
	}

	// --cached in jj repo should also diagnose misuse
	cmd = exec.Command(bin, "--cached")
	cmd.Dir = tmpDir
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected diffcov --cached to fail in jj repo, but got success")
	}
	if !strings.Contains(string(out), "--staged/--cached cannot be used with jj (jujutsu does not have a staging area)") {
		t.Errorf("expected staging area misuse diagnosis in jj repo, got:\n%s", string(out))
	}
}

func runCmd(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %s %v in %s failed: %v, output:\n%s", name, args, dir, err, string(out))
	}
}
