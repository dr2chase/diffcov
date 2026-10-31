// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz/v2"
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

type exitError int

func (e exitError) Error() string {
	return fmt.Sprintf("exit status %d", int(e))
}

func runDiffcov(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	origArgs := os.Args
	origCommandLine := flag.CommandLine
	origStdout := os.Stdout
	origStderr := os.Stderr
	origExit := osExit
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	os.Stderr = w

	fs := flag.NewFlagSet("diffcov", flag.ContinueOnError)
	fs.SetOutput(w)
	flag.CommandLine = fs
	os.Args = append([]string{"diffcov"}, args...)
	osExit = func(code int) {
		panic(exitError(code))
	}

	if dir != "" {
		if err := os.Chdir(dir); err != nil {
			w.Close()
			r.Close()
			os.Stdout = origStdout
			os.Stderr = origStderr
			flag.CommandLine = origCommandLine
			os.Args = origArgs
			osExit = origExit
			t.Fatal(err)
		}
	}

	var outBuf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&outBuf, r)
		close(done)
	}()

	var runErr error
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				if ee, ok := rec.(exitError); ok {
					runErr = ee
				} else {
					panic(rec)
				}
			}
		}()
		main()
	}()

	w.Close()
	<-done
	r.Close()

	if dir != "" {
		_ = os.Chdir(origWd)
	}
	os.Stdout = origStdout
	os.Stderr = origStderr
	flag.CommandLine = origCommandLine
	os.Args = origArgs
	osExit = origExit

	return outBuf.String(), runErr
}

func TestFlagUsage(t *testing.T) {
	bin := getDiffcovBin(t)
	cmd := exec.Command(bin, "-h")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run() // flag -h exits with 2

	out := stderr.String()
	for _, expected := range []string{
		"-a",
		"-C",
		"-coverpkg",
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
	// Both -git and -jj
	out, err := runDiffcov(t, "", "-git", "-jj")
	if err == nil {
		t.Errorf("expected error when passing -git and -jj, got success")
	}
	if !strings.Contains(out, "cannot specify both -git and -jj") {
		t.Errorf("expected conflict error, got:\n%s", out)
	}

	// Invalid -vcs
	out, err = runDiffcov(t, "", "-vcs", "invalid")
	if err == nil {
		t.Errorf("expected error when passing invalid -vcs, got success")
	}
	if !strings.Contains(out, "unsupported revision control system") {
		t.Errorf("expected unsupported vcs error, got:\n%s", out)
	}

	// Conflicting -git and -vcs=jj
	out, err = runDiffcov(t, "", "-git", "-vcs=jj")
	if err == nil {
		t.Errorf("expected error when passing conflicting -git and -vcs=jj, got success")
	}
	if !strings.Contains(out, "conflicting revision control flags") {
		t.Errorf("expected conflicting flags error, got:\n%s", out)
	}

	// Too many arguments
	out, err = runDiffcov(t, "", "diff1", "diff2")
	if err == nil {
		t.Errorf("expected error with too many arguments, got success")
	}
	if !strings.Contains(out, "too many arguments") {
		t.Errorf("expected too many arguments error, got:\n%s", out)
	}

	// Both -d and positional argument
	out, err = runDiffcov(t, "", "-d", "diff1", "diff2")
	if err == nil {
		t.Errorf("expected error with both -d and positional arg, got success")
	}
	if !strings.Contains(out, "cannot specify diff file both") {
		t.Errorf("expected duplicate diff file error, got:\n%s", out)
	}

	// Both -a and -d
	out, err = runDiffcov(t, "", "-a", "-d", "diff1")
	if err == nil {
		t.Errorf("expected error with both -a and -d, got success")
	}
	if !strings.Contains(out, "cannot specify both -a and -d") {
		t.Errorf("expected -a and -d conflict error, got:\n%s", out)
	}

	// Both -a and VCS/diff flags
	out, err = runDiffcov(t, "", "-a", "-git")
	if err == nil {
		t.Errorf("expected error with both -a and -git, got success")
	}
	if !strings.Contains(out, "cannot use diff/VCS flags") {
		t.Errorf("expected -a and VCS conflict error, got:\n%s", out)
	}

	// Autodetect VCS failure in empty non-repo dir
	emptyDir := t.TempDir()
	out, err = runDiffcov(t, emptyDir)
	if err == nil {
		t.Errorf("expected error when VCS autodetect fails, got success")
	}
	if !strings.Contains(out, "failed to autodetect revision control system") {
		t.Errorf("expected VCS autodetect error, got:\n%s", out)
	}

	// git diff command failure in non-git dir
	out, err = runDiffcov(t, emptyDir, "-vcs=git")
	if err == nil {
		t.Errorf("expected error when git diff fails in non-git dir, got success")
	}
	if !strings.Contains(out, "failed to run git diff") {
		t.Errorf("expected git diff failure error, got:\n%s", out)
	}

	// Missing diff file
	out, err = runDiffcov(t, emptyDir, "nonexistent.diff")
	if err == nil {
		t.Errorf("expected error for missing diff file, got success")
	}
	if !strings.Contains(out, "could not read diff from nonexistent.diff") {
		t.Errorf("expected read diff error, got:\n%s", out)
	}
}

func TestStagedWithJJDiagnosed(t *testing.T) {
	// Explicit -jj with --staged
	out, err := runDiffcov(t, "", "-jj", "--staged")
	if err == nil {
		t.Errorf("expected error with -jj and --staged, got success")
	}
	if !strings.Contains(out, "--staged/--cached cannot be used with jj (jujutsu does not have a staging area)") {
		t.Errorf("expected staging area misuse diagnosis, got:\n%s", out)
	}

	// Explicit -jj with --cached
	out, err = runDiffcov(t, "", "-jj", "--cached")
	if err == nil {
		t.Errorf("expected error with -jj and --cached, got success")
	}
	if !strings.Contains(out, "--staged/--cached cannot be used with jj (jujutsu does not have a staging area)") {
		t.Errorf("expected staging area misuse diagnosis, got:\n%s", out)
	}
}

func TestGitWorkflow(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

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

	// Clean repo should fail with empty diff output
	out, err := runDiffcov(t, tmpDir)
	if err == nil {
		t.Errorf("expected error on empty git diff, got success")
	}
	if !strings.Contains(out, "git diff returned empty output") {
		t.Errorf("expected empty diff error, got:\n%s", out)
	}

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
	out, err = runDiffcov(t, tmpDir, "-v")
	if err != nil {
		t.Fatalf("diffcov failed in git repo: %v, output:\n%s", err, out)
	}
	if !strings.Contains(out, "Untested: main.go") || !strings.Contains(out, "return a - b") {
		t.Errorf("expected untested report for Sub, got:\n%s", out)
	}

	// Stage the change and run with --staged
	runCmd(t, tmpDir, "git", "add", "main.go")
	out, err = runDiffcov(t, tmpDir, "--staged")
	if err != nil {
		t.Fatalf("diffcov --staged failed in git repo: %v, output:\n%s", err, out)
	}
	if !strings.Contains(out, "Untested: main.go") || !strings.Contains(out, "return a - b") {
		t.Errorf("expected untested report for Sub with --staged, got:\n%s", out)
	}

	// Commit the change (which adds both tested Mul and untested Sub), export diff to file,
	// and run diffcov -d head.diffs without -c: it should still run go test automatically.
	modifiedWithTested := `package testpkg

func Add(a, b int) int {
	return a + b
}

func Mul(a, b int) int {
	return a * b
}

func Sub(a, b int) int {
	return a - b
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(modifiedWithTested), 0644); err != nil {
		t.Fatal(err)
	}
	testWithMul := `package testpkg

import "testing"

func TestAddAndMul(t *testing.T) {
	if Add(1, 2) != 3 || Mul(2, 3) != 6 {
		t.Fail()
	}
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main_test.go"), []byte(testWithMul), 0644); err != nil {
		t.Fatal(err)
	}
	diffOutCmd := exec.Command("git", "diff", "HEAD")
	diffOutCmd.Dir = tmpDir
	diffBytes, err := diffOutCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git diff HEAD failed: %v", err)
	}
	diffFile := filepath.Join(tmpDir, "head.diffs")
	if err := os.WriteFile(diffFile, diffBytes, 0644); err != nil {
		t.Fatal(err)
	}

	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	out, err = runDiffcov(t, tmpDir, "-g", goPath, "-d", "head.diffs")
	if err != nil {
		t.Fatalf("diffcov -g %s -d head.diffs failed: %v, output:\n%s", goPath, err, out)
	}
	if strings.Contains(out, "return a * b") {
		t.Errorf("expected tested function Mul not to be reported as untested, got:\n%s", out)
	}
	if !strings.Contains(out, "return a - b") {
		t.Errorf("expected untested function Sub to be reported as untested, got:\n%s", out)
	}
	if strings.Contains(out, "main_test.go") {
		t.Errorf("expected changes in main_test.go to be ignored, got:\n%s", out)
	}
}

func TestJJWorkflow(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj not found in PATH")
	}

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
	out, err := runDiffcov(t, tmpDir, "-v")
	if err != nil {
		t.Fatalf("diffcov failed in jj repo: %v, output:\n%s", err, out)
	}
	if !strings.Contains(out, "Untested: main.go") || !strings.Contains(out, "return a / b") {
		t.Errorf("expected untested report for Div in jj repo, got:\n%s", out)
	}

	// Explicit -jj flag should also work
	out, err = runDiffcov(t, tmpDir, "-jj")
	if err != nil {
		t.Fatalf("diffcov -jj failed in jj repo: %v, output:\n%s", err, out)
	}
	if !strings.Contains(out, "Untested: main.go") || !strings.Contains(out, "return a / b") {
		t.Errorf("expected untested report for Div with -jj, got:\n%s", out)
	}

	// --staged in jj repo should diagnose misuse even without explicit -jj flag
	out, err = runDiffcov(t, tmpDir, "--staged")
	if err == nil {
		t.Errorf("expected diffcov --staged to fail in jj repo, but got success")
	}
	if !strings.Contains(out, "--staged/--cached cannot be used with jj (jujutsu does not have a staging area)") {
		t.Errorf("expected staging area misuse diagnosis in jj repo, got:\n%s", out)
	}

	// --cached with -d in jj repo should also diagnose misuse
	out, err = runDiffcov(t, tmpDir, "--cached", "-d", "some.diff")
	if err == nil {
		t.Errorf("expected diffcov --cached -d to fail in jj repo, but got success")
	}
	if !strings.Contains(out, "--staged/--cached cannot be used with jj (jujutsu does not have a staging area)") {
		t.Errorf("expected staging area misuse diagnosis in jj repo, got:\n%s", out)
	}
}

func TestAllFlagWorkflow(t *testing.T) {
	tmpDir := t.TempDir()

	// Note: no .git or .jj initialization needed when -a is used.
	goMod := "module example.com/testall\n\ngo 1.20\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}
	mainGo := `package testall

func Add(a, b int) int {
	return a + b
}

func Sub(a, b int) int {
	return a - b
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0644); err != nil {
		t.Fatal(err)
	}
	extraGo := `package testall

func Extra(x int) int {
	return x * 2
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "extra.go"), []byte(extraGo), 0644); err != nil {
		t.Fatal(err)
	}
	testGo := `package testall

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

	// Also create a subdirectory package to test directory traversal and relative/parent paths
	subDir := filepath.Join(tmpDir, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	subGo := `package sub

func SubHelper(x int) int {
	return x + 10
}
`
	if err := os.WriteFile(filepath.Join(subDir, "sub.go"), []byte(subGo), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Run diffcov -a with no arguments: should check all non-test .go files in current directory.
	outStr, err := runDiffcov(t, tmpDir, "-a")
	if err != nil {
		t.Fatalf("diffcov -a failed: %v, output:\n%s", err, outStr)
	}
	if strings.Contains(outStr, "return a + b") {
		t.Errorf("expected Add to be tested, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Untested: main.go") || !strings.Contains(outStr, "return a - b") {
		t.Errorf("expected untested report for Sub in main.go, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Untested: extra.go") || !strings.Contains(outStr, "return x * 2") {
		t.Errorf("expected untested report for Extra in extra.go, got:\n%s", outStr)
	}
	if strings.Contains(outStr, "main_test.go") {
		t.Errorf("expected main_test.go to be ignored, got:\n%s", outStr)
	}

	// 2. Run diffcov -a main.go: should only check main.go, not extra.go.
	outStr, err = runDiffcov(t, tmpDir, "-a", "main.go")
	if err != nil {
		t.Fatalf("diffcov -a main.go failed: %v, output:\n%s", err, outStr)
	}
	if !strings.Contains(outStr, "Untested: main.go") || !strings.Contains(outStr, "return a - b") {
		t.Errorf("expected untested report for Sub in main.go, got:\n%s", outStr)
	}
	if strings.Contains(outStr, "extra.go") {
		t.Errorf("expected extra.go not to be checked when only main.go was specified, got:\n%s", outStr)
	}

	// 3. Run diffcov -a with directory argument ("sub") and multiple dirs (abs + subdir)
	evalTmpDir, err := filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	outStr, err = runDiffcov(t, tmpDir, "-a", filepath.Join(evalTmpDir, "main.go"), "sub")
	if err != nil {
		t.Fatalf("diffcov -a <abs>/main.go sub failed: %v, output:\n%s", err, outStr)
	}
	if !strings.Contains(outStr, "return x + 10") {
		t.Errorf("expected untested report for SubHelper in sub/sub.go, got:\n%s", outStr)
	}

	// 4. Run diffcov -a with single absolute path argument
	outStr, err = runDiffcov(t, tmpDir, "-a", filepath.Join(evalTmpDir, "main.go"))
	if err != nil {
		t.Fatalf("diffcov -a <abs>/main.go failed: %v, output:\n%s", err, outStr)
	}
	if !strings.Contains(outStr, "return a - b") {
		t.Errorf("expected untested report for Sub in main.go, got:\n%s", outStr)
	}

	// 5. Run diffcov -a from subDir referencing ../sub/sub.go
	outStr, err = runDiffcov(t, subDir, "-a", "../sub/sub.go")
	if err != nil {
		t.Fatalf("diffcov -a ../sub/sub.go failed: %v, output:\n%s", err, outStr)
	}
	if !strings.Contains(outStr, "return x + 10") {
		t.Errorf("expected untested report for SubHelper in ../sub/sub.go, got:\n%s", outStr)
	}

	// 6. Error cases for -a: nonexistent file, no Go source files, unreadable directory
	outStr, err = runDiffcov(t, tmpDir, "-a", "nonexistent.go")
	if err == nil || !strings.Contains(outStr, "could not access nonexistent.go") {
		t.Errorf("expected error for nonexistent.go, got err=%v, out:\n%s", err, outStr)
	}

	outStr, err = runDiffcov(t, tmpDir, "-a", "main_test.go")
	if err == nil || !strings.Contains(outStr, "no Go source files found") {
		t.Errorf("expected no Go source files found error, got err=%v, out:\n%s", err, outStr)
	}

	unreadableDir := filepath.Join(tmpDir, "unreadable")
	if err := os.Mkdir(unreadableDir, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(unreadableDir, 0755)
	if _, readErr := os.ReadDir(unreadableDir); readErr != nil {
		outStr, err = runDiffcov(t, tmpDir, "-a", "unreadable")
		if err == nil || !strings.Contains(outStr, "could not read directory unreadable") {
			t.Errorf("expected could not read directory error, got err=%v, out:\n%s", err, outStr)
		}
	}
}

func TestCoverPkgWorkflow(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	tmpDir := t.TempDir()
	runCmd(t, tmpDir, "git", "init")
	runCmd(t, tmpDir, "git", "config", "user.name", "Test User")
	runCmd(t, tmpDir, "git", "config", "user.email", "test@example.com")

	goMod := "module example.com/multi\n\ngo 1.20\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	pkgaDir := filepath.Join(tmpDir, "pkga")
	pkgbDir := filepath.Join(tmpDir, "pkgb")
	if err := os.MkdirAll(pkgaDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pkgbDir, 0755); err != nil {
		t.Fatal(err)
	}

	bGoInitial := `package pkgb

func Base() int {
	return 1
}
`
	if err := os.WriteFile(filepath.Join(pkgbDir, "b.go"), []byte(bGoInitial), 0644); err != nil {
		t.Fatal(err)
	}

	aGo := `package pkga

import "example.com/multi/pkgb"

func CallB() int {
	return pkgb.Base()
}
`
	if err := os.WriteFile(filepath.Join(pkgaDir, "a.go"), []byte(aGo), 0644); err != nil {
		t.Fatal(err)
	}

	aTestGo := `package pkga

import (
	"example.com/multi/pkgb"
	"testing"
)

func TestCallB(t *testing.T) {
	if CallB() != 1 || pkgb.UsedByA(5) != 6 {
		t.Fail()
	}
}
`
	if err := os.WriteFile(filepath.Join(pkgaDir, "a_test.go"), []byte(aTestGo), 0644); err != nil {
		t.Fatal(err)
	}

	runCmd(t, tmpDir, "git", "add", ".")
	runCmd(t, tmpDir, "git", "commit", "-m", "initial commit")

	// Modify pkgb/b.go with UsedByA (called by pkga's test) and UnusedByA (not called).
	bGoModified := `package pkgb

func Base() int {
	return 1
}

func UsedByA(x int) int {
	return x + 1
}

func UnusedByA(x int) int {
	return x - 1
}
`
	if err := os.WriteFile(filepath.Join(pkgbDir, "b.go"), []byte(bGoModified), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Without -coverpkg, running from pkgaDir only instruments pkga,
	// so UsedByA in pkgb/b.go is reported as Untested.
	out, err := runDiffcov(t, pkgaDir, "-git")
	if err != nil {
		t.Fatalf("diffcov without -coverpkg failed: %v, output:\n%s", err, out)
	}
	if !strings.Contains(out, "return x + 1") {
		t.Errorf("expected UsedByA to be untested without -coverpkg, got:\n%s", out)
	}
	if !strings.Contains(out, "return x - 1") {
		t.Errorf("expected UnusedByA to be untested without -coverpkg, got:\n%s", out)
	}

	// 2. With -coverpkg, UsedByA is covered by pkga's test, while UnusedByA remains Untested.
	out, err = runDiffcov(t, pkgaDir, "-git", "-coverpkg=example.com/multi/...", "-v")
	if err != nil {
		t.Fatalf("diffcov -coverpkg failed: %v, output:\n%s", err, out)
	}
	if !strings.Contains(out, "-coverpkg example.com/multi/...") {
		t.Errorf("expected go test args to include '-coverpkg example.com/multi/...', got:\n%s", out)
	}
	if strings.Contains(out, "return x + 1") {
		t.Errorf("expected UsedByA to be covered with -coverpkg, got:\n%s", out)
	}
	if !strings.Contains(out, "return x - 1") {
		t.Errorf("expected UnusedByA to be untested with -coverpkg, got:\n%s", out)
	}
}

func TestCoverDirWorkflow(t *testing.T) {
	tmpDir := t.TempDir()

	goMod := "module example.com/appcov\n\ngo 1.20\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	mainGo := `package main

import "os"

func CoveredFunc(x int) int {
	return x + 42
}

func UncoveredFunc(x int) int {
	return x - 42
}

func main() {
	if CoveredFunc(1) != 43 {
		os.Exit(1)
	}
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0644); err != nil {
		t.Fatal(err)
	}

	appBin := filepath.Join(tmpDir, "app.exe")
	runCmd(t, tmpDir, "go", "build", "-cover", "-o", appBin, ".")

	coverDir := filepath.Join(tmpDir, "covdata")
	if err := os.MkdirAll(coverDir, 0755); err != nil {
		t.Fatal(err)
	}

	appCmd := exec.Command(appBin)
	appCmd.Dir = tmpDir
	appCmd.Env = append(os.Environ(), "GOCOVERDIR="+coverDir)
	if out, err := appCmd.CombinedOutput(); err != nil {
		t.Fatalf("running instrumented app failed: %v, output:\n%s", err, string(out))
	}

	// Run diffcov -a -C coverDir -v
	out, err := runDiffcov(t, tmpDir, "-a", "-C", coverDir, "-v")
	if err != nil {
		t.Fatalf("diffcov -a -C failed: %v, output:\n%s", err, out)
	}
	if !strings.Contains(out, "covdata merge") || !strings.Contains(out, "covdata textfmt") {
		t.Errorf("expected verbose output to show covdata merge and textfmt commands, got:\n%s", out)
	}
	if strings.Contains(out, "return x + 42") {
		t.Errorf("expected CoveredFunc to be covered, got:\n%s", out)
	}
	if !strings.Contains(out, "return x - 42") {
		t.Errorf("expected UncoveredFunc to be reported as untested, got:\n%s", out)
	}

	// Nonexistent coverdir should fail during covdata merge
	out, err = runDiffcov(t, tmpDir, "-a", "-C", filepath.Join(tmpDir, "nonexistent"))
	if err == nil {
		t.Errorf("expected error for nonexistent coverdir, got success")
	}
	if !strings.Contains(out, "failed to run go tool covdata merge") {
		t.Errorf("expected covdata merge error message, got:\n%s", out)
	}
}

func TestCombineCoverageSources(t *testing.T) {
	tmpDir := t.TempDir()

	goMod := "module example.com/combinecov\n\ngo 1.20\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	mainGo := `package main

import "os"

func ByProf1(x int) int {
	return x + 101
}

func ByProf2(x int) int {
	return x + 202
}

func ByAppCov(x int) int {
	return x + 303
}

func Uncovered(x int) int {
	return x - 999
}

func main() {
	if ByAppCov(1) != 304 {
		os.Exit(1)
	}
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0644); err != nil {
		t.Fatal(err)
	}

	testGo := `package main

import "testing"

func TestProf1(t *testing.T) {
	if ByProf1(1) != 102 {
		t.Fail()
	}
}

func TestProf2(t *testing.T) {
	if ByProf2(1) != 203 {
		t.Fail()
	}
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main_test.go"), []byte(testGo), 0644); err != nil {
		t.Fatal(err)
	}

	// Generate two distinct -coverprofile files (using set and count modes)
	runCmd(t, tmpDir, "go", "test", "-run=^TestProf1$", "-coverprofile=prof1.out", ".")
	runCmd(t, tmpDir, "go", "test", "-run=^TestProf2$", "-covermode=count", "-coverprofile=prof2.out", ".")

	// Generate application coverage directory for -C
	appBin := filepath.Join(tmpDir, "app.exe")
	runCmd(t, tmpDir, "go", "build", "-cover", "-o", appBin, ".")
	coverDir := filepath.Join(tmpDir, "covdata")
	if err := os.MkdirAll(coverDir, 0755); err != nil {
		t.Fatal(err)
	}
	appCmd := exec.Command(appBin)
	appCmd.Dir = tmpDir
	appCmd.Env = append(os.Environ(), "GOCOVERDIR="+coverDir)
	if out, err := appCmd.CombinedOutput(); err != nil {
		t.Fatalf("running instrumented app failed: %v, output:\n%s", err, string(out))
	}

	// 1. Single -c prof1.out
	out, err := runDiffcov(t, tmpDir, "-a", "-c", "prof1.out", "main.go")
	if err != nil {
		t.Fatalf("diffcov -a -c prof1.out failed: %v, output:\n%s", err, out)
	}
	if strings.Contains(out, "return x + 101") {
		t.Errorf("expected ByProf1 to be covered, got:\n%s", out)
	}
	for _, wantUntested := range []string{"return x + 202", "return x + 303", "return x - 999"} {
		if !strings.Contains(out, wantUntested) {
			t.Errorf("expected %q to be untested with only prof1.out, got:\n%s", wantUntested, out)
		}
	}

	// 2. Repeated -c: -c prof1.out -c prof2.out
	out, err = runDiffcov(t, tmpDir, "-a", "-c", "prof1.out", "-c", "prof2.out", "main.go")
	if err != nil {
		t.Fatalf("diffcov -a -c prof1.out -c prof2.out failed: %v, output:\n%s", err, out)
	}
	for _, wantCovered := range []string{"return x + 101", "return x + 202"} {
		if strings.Contains(out, wantCovered) {
			t.Errorf("expected %q to be covered with prof1+prof2, got:\n%s", wantCovered, out)
		}
	}
	for _, wantUntested := range []string{"return x + 303", "return x - 999"} {
		if !strings.Contains(out, wantUntested) {
			t.Errorf("expected %q to be untested with prof1+prof2, got:\n%s", wantUntested, out)
		}
	}

	// 3. -c and -C together: -c prof1.out -C covdata
	out, err = runDiffcov(t, tmpDir, "-a", "-c", "prof1.out", "-C", coverDir, "main.go")
	if err != nil {
		t.Fatalf("diffcov -a -c prof1.out -C covdata failed: %v, output:\n%s", err, out)
	}
	for _, wantCovered := range []string{"return x + 101", "return x + 303"} {
		if strings.Contains(out, wantCovered) {
			t.Errorf("expected %q to be covered with prof1+covdata, got:\n%s", wantCovered, out)
		}
	}
	for _, wantUntested := range []string{"return x + 202", "return x - 999"} {
		if !strings.Contains(out, wantUntested) {
			t.Errorf("expected %q to be untested with prof1+covdata, got:\n%s", wantUntested, out)
		}
	}

	// 4. Repeated -c and -C together in diff mode with -v
	diffText := `diff --git a/main.go b/main.go
index 1111111..2222222 100644
--- a/main.go
+++ b/main.go
@@ -5,15 +5,15 @@ import "os"
 func ByProf1(x int) int {
+	return x + 101
 }
 
 func ByProf2(x int) int {
+	return x + 202
 }
 
 func ByAppCov(x int) int {
+	return x + 303
 }
 
 func Uncovered(x int) int {
+	return x - 999
 }
`
	if err := os.WriteFile(filepath.Join(tmpDir, "changes.diff"), []byte(diffText), 0644); err != nil {
		t.Fatal(err)
	}
	out, err = runDiffcov(t, tmpDir, "-v", "-c", "prof1.out", "-c", "prof2.out", "-C", coverDir, "-d", "changes.diff")
	if err != nil {
		t.Fatalf("diffcov with -c prof1 -c prof2 -C covdata -d changes.diff failed: %v, output:\n%s", err, out)
	}
	if !strings.Contains(out, "Combining coverage profiles") {
		t.Errorf("expected verbose output to mention combining coverage profiles, got:\n%s", out)
	}
	for _, wantCovered := range []string{"return x + 101", "return x + 202", "return x + 303"} {
		if strings.Contains(out, wantCovered) {
			t.Errorf("expected %q to be covered when combining all sources, got:\n%s", wantCovered, out)
		}
	}
	if !strings.Contains(out, "return x - 999") {
		t.Errorf("expected Uncovered to be reported as untested, got:\n%s", out)
	}

	// 5. Garbage lines and CRLF in combined profile preserve -q behavior
	garbageProf := "mode: set\r\ngarbage_no_space\r\ngarbage badcount\r\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "garbage.out"), []byte(garbageProf), 0644); err != nil {
		t.Fatal(err)
	}
	out, err = runDiffcov(t, tmpDir, "-a", "-c", "prof1.out", "-c", "garbage.out", "main.go")
	if err != nil {
		t.Fatalf("diffcov with garbage profile failed: %v, output:\n%s", err, out)
	}
	if !strings.Contains(out, "failed to match cover line RE") {
		t.Errorf("expected warning on garbage cover line without -q, got:\n%s", out)
	}
	out, err = runDiffcov(t, tmpDir, "-a", "-q", "-c", "prof1.out", "-c", "garbage.out", "main.go")
	if err != nil {
		t.Fatalf("diffcov -q with garbage profile failed: %v, output:\n%s", err, out)
	}
	if strings.Contains(out, "failed to match cover line RE") {
		t.Errorf("expected -q to suppress garbage cover line warning, got:\n%s", out)
	}

	// 6. Error handling for nonexistent -c files (single and repeated)
	out, err = runDiffcov(t, tmpDir, "-a", "-c", "missing.out", "main.go")
	if err == nil || !strings.Contains(out, "could not read coverprofile from missing.out") {
		t.Errorf("expected error for single missing -c file, got err=%v, out:\n%s", err, out)
	}
	out, err = runDiffcov(t, tmpDir, "-a", "-c", "prof1.out", "-c", "missing.out", "main.go")
	if err == nil || !strings.Contains(out, "could not read coverprofile from missing.out") {
		t.Errorf("expected error for missing second -c file, got err=%v, out:\n%s", err, out)
	}

	// 7. Error handling when temporary directory creation fails
	badTmp := filepath.Join(tmpDir, "nonexistent-parent", "tmp")
	t.Setenv("TMPDIR", badTmp)
	t.Setenv("TMP", badTmp)
	t.Setenv("TEMP", badTmp)
	if _, probeErr := os.MkdirTemp("", "probe"); probeErr != nil {
		out, err = runDiffcov(t, tmpDir, "-a", "-c", "prof1.out", "-c", "prof2.out", "main.go")
		if err == nil || !strings.Contains(out, "failed to create temporary dir") {
			t.Errorf("expected failed to create temporary dir error, got err=%v, out:\n%s", err, out)
		}
	}
}

func TestCompressedCoverProfiles(t *testing.T) {
	tmpDir := t.TempDir()

	goMod := "module example.com/compcov\n\ngo 1.20\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	mainGo := `package compcov

func ByGzip(x int) int {
	return x + 11
}

func ByXZ(x int) int {
	return x + 22
}

func ByZstd(x int) int {
	return x + 33
}

func Never(x int) int {
	return x - 44
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0644); err != nil {
		t.Fatal(err)
	}

	profGzip := "mode: set\nexample.com/compcov/main.go:3.23,5.2 1 1\nexample.com/compcov/main.go:7.21,9.2 1 0\nexample.com/compcov/main.go:11.23,13.2 1 0\nexample.com/compcov/main.go:15.22,17.2 1 0\n"
	profXZ := "mode: set\nexample.com/compcov/main.go:3.23,5.2 1 0\nexample.com/compcov/main.go:7.21,9.2 1 1\nexample.com/compcov/main.go:11.23,13.2 1 0\nexample.com/compcov/main.go:15.22,17.2 1 0\n"
	profZstd := "mode: set\nexample.com/compcov/main.go:3.23,5.2 1 0\nexample.com/compcov/main.go:7.21,9.2 1 0\nexample.com/compcov/main.go:11.23,13.2 1 1\nexample.com/compcov/main.go:15.22,17.2 1 0\n"

	writeGzip := func(name, content string) {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		if _, err := gw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err := gw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, name), buf.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}

	writeXZ := func(name, content string) {
		var buf bytes.Buffer
		xw, err := xz.NewWriter(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := xw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err := xw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, name), buf.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}

	writeZstd := func(name, content string) {
		var buf bytes.Buffer
		zw, err := zstd.NewWriter(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := zw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, name), buf.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}

	for _, ext := range []string{".gz", ".GZ"} {
		name := "cov1" + ext
		writeGzip(name, profGzip)
		out, err := runDiffcov(t, tmpDir, "-a", "-c", name, "main.go")
		if err != nil {
			t.Fatalf("single -c %s failed: %v, out:\n%s", name, err, out)
		}
		if strings.Contains(out, "return x + 11") || !strings.Contains(out, "return x + 22") {
			t.Errorf("unexpected coverage for %s:\n%s", name, out)
		}
	}

	for _, ext := range []string{".xz", ".XZ"} {
		name := "cov2" + ext
		writeXZ(name, profXZ)
		out, err := runDiffcov(t, tmpDir, "-a", "-c", name, "main.go")
		if err != nil {
			t.Fatalf("single -c %s failed: %v, out:\n%s", name, err, out)
		}
		if strings.Contains(out, "return x + 22") || !strings.Contains(out, "return x + 11") {
			t.Errorf("unexpected coverage for %s:\n%s", name, out)
		}
	}

	for _, ext := range []string{".zst", ".ZST", ".zstd", ".ZSTD"} {
		name := "cov3" + ext
		writeZstd(name, profZstd)
		out, err := runDiffcov(t, tmpDir, "-a", "-c", name, "main.go")
		if err != nil {
			t.Fatalf("single -c %s failed: %v, out:\n%s", name, err, out)
		}
		if strings.Contains(out, "return x + 33") || !strings.Contains(out, "return x + 11") {
			t.Errorf("unexpected coverage for %s:\n%s", name, out)
		}
	}

	// Combine .gz, .XZ, and .zstd together
	out, err := runDiffcov(t, tmpDir, "-a", "-c", "cov1.gz", "-c", "cov2.XZ", "-c", "cov3.zstd", "main.go")
	if err != nil {
		t.Fatalf("combined compressed -c failed: %v, out:\n%s", err, out)
	}
	for _, coveredStmt := range []string{"return x + 11", "return x + 22", "return x + 33"} {
		if strings.Contains(out, coveredStmt) {
			t.Errorf("expected %q to be covered in combined compressed run, got:\n%s", coveredStmt, out)
		}
	}
	if !strings.Contains(out, "return x - 44") {
		t.Errorf("expected Never to remain untested, got:\n%s", out)
	}

	// Error cases: missing compressed file and corrupt .gz, .xz, and .zst files
	out, err = runDiffcov(t, tmpDir, "-a", "-c", "missing.gz", "main.go")
	if err == nil || !strings.Contains(out, "could not read coverprofile from missing.gz") {
		t.Errorf("expected missing.gz error, got err=%v, out:\n%s", err, out)
	}

	for _, badFile := range []string{"bad.gz", "bad.xz", "bad.zst"} {
		if err := os.WriteFile(filepath.Join(tmpDir, badFile), []byte("not valid compressed data"), 0644); err != nil {
			t.Fatal(err)
		}
		out, err = runDiffcov(t, tmpDir, "-a", "-c", badFile, "main.go")
		if err == nil {
			t.Errorf("expected error for corrupt %s, got success with out:\n%s", badFile, out)
		}
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
