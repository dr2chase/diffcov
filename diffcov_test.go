// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package diffcov_test

import (
	"os"
	"os/exec"
	"path/filepath"
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
	diffcov.DoDiffs(diffBytes, "testdata/sample.cover", ".", ".", 0, 0, false)
}
