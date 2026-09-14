// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/dr2chase/diffcov"
)

func fail(format string, args ...any) {
	flag.Usage()
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, format, args...)
	os.Exit(1)
}

// diffcov whatever.diff
func main() {
	var verbose Count
	var coverprofile string
	var diffDir string
	var diffFile string
	var modDir string
	var strip int
	var staged bool
	var showTested bool
	var useGit bool
	var useJJ bool
	var vcsFlag string

	flag.Var(&verbose, "v", "Says more and more")
	flag.StringVar(&coverprofile, "c", coverprofile, "name of test -coverprofile output file")
	flag.StringVar(&diffFile, "d", diffFile, "name of (git or jj) diff output file")
	flag.StringVar(&diffDir, "D", diffDir, "diff directory root (typically parent of git or jj repo root)")
	flag.StringVar(&modDir, "M", modDir, "directory containing go.mod")
	flag.IntVar(&strip, "S", strip, "number of leading directories to strip from files in diff (useful w/ packages differently named from directory)")
	flag.BoolVar(&staged, "staged", staged, "if run no-args, use git --staged to obtain diff (git only)")
	flag.BoolVar(&staged, "cached", staged, "if run no-args, use git --staged to obtain diff (git only)")
	flag.BoolVar(&showTested, "t", showTested, "also show the tested lines")
	flag.BoolVar(&useGit, "git", false, "use git to obtain diff")
	flag.BoolVar(&useJJ, "jj", false, "use jj (jujutsu) to obtain diff")
	flag.StringVar(&vcsFlag, "vcs", "", "revision control system to use ('git' or 'jj')")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Running '%[1]s' with no options tends to work, so try that first and ignore the rest of this documentation.
'%[1]s' uses diff and test coverage data to point out any new or modified code in the diff that is not exercised by tests.

'%[1]s [options] -c coverprofile [-d] diffFile' reports the new statements in diffFile that do not appear in the coverprofile.
'%[1]s [options] [-d] diffFile' reports all the new statements in diffFile.
'%[1]s [no options]' will attempt to run 'git diff' or 'jj diff --git' (autodetected) and 'go test -coverprofile' to automatically generate -c/-d files.
If -M, -D, -S are not provided, %[1]s searches in parent directories for clues.
`, os.Args[0])
	}

	flag.Parse()

	var err error
	var diffBytes []byte

	if len(flag.Args()) > 1 {
		fail("too many arguments provided; expected at most one diff file\n")
	} else if len(flag.Args()) == 1 {
		if diffFile != "" {
			fail("cannot specify diff file both with -d and as positional argument\n")
		}
		diffFile = flag.Args()[0]
	}

	if useGit && useJJ {
		fail("cannot specify both -git and -jj\n")
	}
	vcs := ""
	if useGit {
		vcs = "git"
	} else if useJJ {
		vcs = "jj"
	}
	if vcsFlag != "" {
		if vcsFlag != "git" && vcsFlag != "jj" {
			fail("unsupported revision control system %q: must be 'git' or 'jj'\n", vcsFlag)
		}
		if vcs != "" && vcs != vcsFlag {
			fail("conflicting revision control flags: -%s and -vcs=%s\n", vcs, vcsFlag)
		}
		vcs = vcsFlag
	}

	if diffFile == "" {
		// With no diff file, attempt to automatically do the right thing; run diff, run the test, use those.
		if vcs == "" {
			vcs, err = diffcov.DetectVCS(".", diffDir)
			if err != nil {
				fail("failed to autodetect revision control system: %v\n", err)
			}
		}

		if staged && vcs == "jj" {
			fail("--staged/--cached cannot be used with jj (jujutsu does not have a staging area)\n")
		}

		var cmd *exec.Cmd
		var cmdDesc string
		if vcs == "jj" {
			jjArgs := []string{"diff", "--git"}
			cmd = exec.Command("jj", jjArgs...)
			cmdDesc = "jj diff"
			if verbose > 0 {
				fmt.Fprintf(os.Stderr, "Running jj, jjArgs=%v\n", jjArgs)
			}
		} else { // git
			gitArgs := []string{"diff"}
			if staged {
				gitArgs = append(gitArgs, "--staged")
			}
			cmd = exec.Command("git", gitArgs...)
			cmdDesc = "git diff"
			if verbose > 0 {
				fmt.Fprintf(os.Stderr, "Running git, gitArgs=%v\n", gitArgs)
			}
		}

		diffBytes, err = cmd.CombinedOutput()
		if err != nil {
			fail("%s\nfailed to run %s, err=%v, output was\n", string(diffBytes), cmdDesc, err)
		}
		if len(diffBytes) == 0 {
			fail("%s returned empty output, perhaps there is a problem with the directory or the flags?\n", cmdDesc)
		}

		if coverprofile == "" {
			coverDir, err := os.MkdirTemp("", "diffcov")
			if err != nil {
				fail("failed to create temporary dir, err=%v\n", err)
			}

			coverprofile = filepath.Join(coverDir, "coverprofile.out")

			testArgs := []string{"test", "-coverprofile", coverprofile, "."}
			testCmd := exec.Command("go", testArgs...)
			if verbose > 0 {
				fmt.Fprintf(os.Stderr, "Running go test, args=%v\n", testArgs)
			}
			coverOut, err := testCmd.CombinedOutput()
			if err != nil {
				fail("%s\nfailed to run go test ..., err=%v\n", string(coverOut), err)
			}
			if verbose > 0 {
				fmt.Fprintf(os.Stderr, "%s\n", string(coverOut))
			}
		}
	} else {
		if staged {
			checkVCS := vcs
			if checkVCS == "" {
				checkVCS, _ = diffcov.DetectVCS(".", diffDir)
			}
			if checkVCS == "jj" {
				fail("--staged/--cached cannot be used with jj (jujutsu does not have a staging area)\n")
			}
		}
		diffBytes, err = os.ReadFile(diffFile)
		if err != nil {
			fail("could not read diff from %s, error was %v\n", diffFile, err)
		}
	}

	diffcov.DoDiffs(diffBytes, coverprofile, diffDir, modDir, strip, int(verbose), showTested)
}

// Count is a flag.Value that is like a flag.Bool and a flag.Int.
// If used as -name, it increments the Count, but -name=x sets the Count.
// Used for verbose flag -v.
type Count int

func (c *Count) String() string {
	return fmt.Sprint(int(*c))
}

func (c *Count) Set(s string) error {
	switch s {
	case "true":
		*c++
	case "false":
		*c = 0
	default:
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("invalid count %q", s)
		}
		*c = Count(n)
	}
	return nil
}

func (c *Count) Get() interface{} {
	return int(*c)
}

func (c *Count) IsBoolFlag() bool {
	return true
}

func (c *Count) IsCountFlag() bool {
	return true
}

type T struct {
	A, B int
}

func F(a, b int) *T {
	return &T{a, b}
}
