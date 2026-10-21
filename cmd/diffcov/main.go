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
	"strings"

	"dr2cha.se/diffcov"
)

var osExit = os.Exit

func fail(format string, args ...any) {
	flag.Usage()
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, format, args...)
	osExit(1)
}

// diffcov whatever.diff
func main() {
	var all bool
	var verbose Count
	var coverprofile string
	var diffDir string
	var diffFile string
	var goCmd = "go"
	var modDir string
	var quiet bool
	var strip int
	var staged bool
	var showTested bool
	var useGit bool
	var useJJ bool
	var vcsFlag string

	flag.BoolVar(&all, "a", false, "check coverage of entire source file(s) instead of diff")
	flag.Var(&verbose, "v", "Says more and more")
	flag.StringVar(&coverprofile, "c", coverprofile, "name of test -coverprofile output file")
	flag.StringVar(&diffFile, "d", diffFile, "name of (git or jj) diff output file")
	flag.StringVar(&diffDir, "D", diffDir, "diff directory root (typically parent of git or jj repo root)")
	flag.StringVar(&goCmd, "g", goCmd, "go command to use for running tests")
	flag.StringVar(&modDir, "M", modDir, "directory containing go.mod")
	flag.IntVar(&strip, "S", strip, "number of leading directories to strip from files in diff (useful w/ packages differently named from directory)")
	flag.BoolVar(&quiet, "q", quiet, "don't complain about garbage inputs")
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

'%[1]s [options] [-c coverprofile] [[-d] diffFile]' reports the new statements in diffFile that do not appear in the coverprofile.
'%[1]s -a [options] [-c coverprofile] [files or directories...]' reports any statements in the specified Go source files (or the current directory if none are specified) that do not appear in the coverprofile.
If -d (and diffFile) is omitted (and -a is not specified), '%[1]s' will attempt to run 'git diff' or 'jj diff --git' (autodetected) to obtain the diff.
If -c is omitted, '%[1]s' will attempt to run 'go test -coverprofile' to obtain coverage data.
If -M, -D, -S are not provided, %[1]s searches in parent directories for clues.

To update: go install dr2cha.se/diffcov/...@latest
`, os.Args[0])
	}

	flag.Parse()

	var err error
	var diffBytes []byte
	var files []string

	if all {
		if diffFile != "" {
			fail("cannot specify both -a and -d\n")
		}
		if staged || useGit || useJJ || vcsFlag != "" {
			fail("cannot use diff/VCS flags (-staged, -cached, -git, -jj, -vcs) with -a\n")
		}
		addDirFiles := func(dir string) {
			entries, err := os.ReadDir(dir)
			if err != nil {
				fail("could not read directory %s, error was %v\n", dir, err)
			}
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := e.Name()
				if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
					if dir == "." {
						files = append(files, name)
					} else {
						files = append(files, filepath.Join(dir, name))
					}
				}
			}
		}
		if len(flag.Args()) == 0 {
			addDirFiles(".")
		} else {
			for _, arg := range flag.Args() {
				info, err := os.Stat(arg)
				if err != nil {
					fail("could not access %s, error was %v\n", arg, err)
				}
				if info.IsDir() {
					addDirFiles(arg)
				} else if strings.HasSuffix(arg, ".go") && !strings.HasSuffix(arg, "_test.go") {
					files = append(files, arg)
				}
			}
		}
		if len(files) == 0 {
			fail("no Go source files found\n")
		}
	} else {
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
			if verbose > 0 {
				fmt.Fprintf(os.Stderr, "diffFile=%s, coverprofile=%s, \n", diffFile, coverprofile)
			}

			diffBytes, err = os.ReadFile(diffFile)
			if err != nil {
				fail("could not read diff from %s, error was %v\n", diffFile, err)
			}
		}
	}

	if coverprofile == "" {
		coverDir, err := os.MkdirTemp("", "diffcov")
		if err != nil {
			fail("failed to create temporary dir, err=%v\n", err)
		}
		defer os.RemoveAll(coverDir)

		coverprofile = filepath.Join(coverDir, "coverprofile.out")

		testArgs := []string{"test", "-coverprofile", coverprofile}
		var testDir string
		if all {
			var dirs []string
			seen := make(map[string]bool)
			for _, f := range files {
				d := filepath.Dir(f)
				if !seen[d] {
					seen[d] = true
					dirs = append(dirs, d)
				}
			}
			if len(dirs) == 1 && filepath.IsAbs(dirs[0]) {
				testDir = dirs[0]
				testArgs = append(testArgs, ".")
			} else {
				wd, wdErr := os.Getwd()
				for _, d := range dirs {
					if filepath.IsAbs(d) && wdErr == nil {
						if rel, err := filepath.Rel(wd, d); err == nil {
							d = rel
						}
					}
					if d == "." {
						testArgs = append(testArgs, ".")
					} else if strings.HasPrefix(d, "./") || strings.HasPrefix(d, "../") {
						testArgs = append(testArgs, filepath.ToSlash(d))
					} else {
						testArgs = append(testArgs, "./"+filepath.ToSlash(d))
					}
				}
			}
		} else {
			testArgs = append(testArgs, ".")
		}
		testCmd := exec.Command(goCmd, testArgs...)
		if testDir != "" {
			testCmd.Dir = testDir
		}
		if verbose > 0 {
			fmt.Fprintf(os.Stderr, "Running %s, args=%v\n", goCmd, testArgs)
		}
		coverOut, err := testCmd.CombinedOutput()
		if err != nil {
			fail("%s\nfailed to run %s test ..., err=%v\n", string(coverOut), goCmd, err)
		}
		if verbose > 0 {
			fmt.Fprintf(os.Stderr, "%s\n", string(coverOut))
		}
	}

	if all {
		diffcov.DoFiles(files, coverprofile, modDir, strip, int(verbose), showTested, quiet)
	} else {
		diffcov.DoDiffs(diffBytes, coverprofile, diffDir, modDir, strip, int(verbose), showTested, quiet)
	}
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
