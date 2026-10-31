// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"dr2cha.se/diffcov"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz/v2"
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
	var coverprofiles stringSlice
	var coverdir string
	var coverpkg string
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
	flag.Var(&coverprofiles, "c", "name of test -coverprofile output `file` (may be repeated; .gz, .xz, .zst/.zstd auto-decompressed)")
	flag.StringVar(&coverdir, "C", coverdir, "name of directory containing application coverage data (GOCOVERDIR)")
	flag.StringVar(&coverpkg, "coverpkg", coverpkg, "package pattern for 'go test -coverpkg'")
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

'%[1]s [options] [-c coverprofile]... [-C coverdir] [[-d] diffFile]' reports the new statements in diffFile that do not appear in the coverage data.
'%[1]s -a [options] [-c coverprofile]... [-C coverdir] [files or directories...]' reports any statements in the specified Go source files (or the current directory if none are specified) that do not appear in the coverage data.
If -d (and diffFile) is omitted (and -a is not specified), '%[1]s' will attempt to run 'git diff' or 'jj diff --git' (autodetected) to obtain the diff.
If -c and -C are omitted, '%[1]s' will attempt to run 'go test -coverprofile' to obtain coverage data.
If -C is specified, '%[1]s' converts application coverage data in coverdir to a coverage profile using 'go tool covdata'.
Multiple coverage sources (-c may be repeated, and combined with -C) are combined into one.
If -coverpkg is specified, '%[1]s' also passes '-coverpkg <pattern>' to 'go test'.
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
				fmt.Fprintf(os.Stderr, "diffFile=%s, coverprofiles=%s, coverdir=%s\n", diffFile, coverprofiles.String(), coverdir)
			}

			diffBytes, err = os.ReadFile(diffFile)
			if err != nil {
				fail("could not read diff from %s, error was %v\n", diffFile, err)
			}
		}
	}

	var tmpDir string
	getTmpDir := func() string {
		if tmpDir == "" {
			tmpDir, err = os.MkdirTemp("", "diffcov")
			if err != nil {
				fail("failed to create temporary dir, err=%v\n", err)
			}
		}
		return tmpDir
	}
	defer func() {
		if tmpDir != "" {
			os.RemoveAll(tmpDir)
		}
	}()

	var coverprofile string
	profileSources := append([]string(nil), coverprofiles...)

	if coverdir != "" {
		td := getTmpDir()
		mergedDir := filepath.Join(td, "merged.data")
		if err := os.Mkdir(mergedDir, 0755); err != nil {
			fail("failed to create merged coverage dir, err=%v\n", err)
		}
		covdirProfile := filepath.Join(td, "covdata.out")

		mergeArgs := []string{"tool", "covdata", "merge", "-i=" + coverdir, "-o=" + mergedDir}
		mergeCmd := exec.Command(goCmd, mergeArgs...)
		if verbose > 0 {
			fmt.Fprintf(os.Stderr, "Running %s, args=%v\n", goCmd, mergeArgs)
		}
		mergeOut, err := mergeCmd.CombinedOutput()
		if err != nil {
			fail("%s\nfailed to run %s tool covdata merge ..., err=%v\n", string(mergeOut), goCmd, err)
		}
		if verbose > 0 && len(mergeOut) > 0 {
			fmt.Fprintf(os.Stderr, "%s\n", string(mergeOut))
		}

		textfmtArgs := []string{"tool", "covdata", "textfmt", "-i", mergedDir, "-o", covdirProfile}
		textfmtCmd := exec.Command(goCmd, textfmtArgs...)
		if verbose > 0 {
			fmt.Fprintf(os.Stderr, "Running %s, args=%v\n", goCmd, textfmtArgs)
		}
		textfmtOut, err := textfmtCmd.CombinedOutput()
		if err != nil {
			fail("%s\nfailed to run %s tool covdata textfmt ..., err=%v\n", string(textfmtOut), goCmd, err)
		}
		if verbose > 0 && len(textfmtOut) > 0 {
			fmt.Fprintf(os.Stderr, "%s\n", string(textfmtOut))
		}
		profileSources = append(profileSources, covdirProfile)
	}

	if len(profileSources) == 0 {
		coverprofile = filepath.Join(getTmpDir(), "coverprofile.out")

		testArgs := []string{"test", "-coverprofile", coverprofile}
		if coverpkg != "" {
			testArgs = append(testArgs, "-coverpkg", coverpkg)
		}
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
	} else if len(profileSources) == 1 && compressionKind(profileSources[0]) == "" {
		coverprofile = profileSources[0]
		if _, err := os.Stat(coverprofile); err != nil {
			fail("could not read coverprofile from %s, error was %v\n", coverprofile, err)
		}
	} else {
		coverprofile = filepath.Join(getTmpDir(), "combined.out")
		if verbose > 0 {
			fmt.Fprintf(os.Stderr, "Combining coverage profiles %v into %s\n", profileSources, coverprofile)
		}
		if err := combineCoverProfiles(coverprofile, profileSources); err != nil {
			fail("%v\n", err)
		}
	}

	if all {
		diffcov.DoFiles(files, coverprofile, modDir, strip, int(verbose), showTested, quiet)
	} else {
		diffcov.DoDiffs(diffBytes, coverprofile, diffDir, modDir, strip, int(verbose), showTested, quiet)
	}
}

func compressionKind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".gz":
		return "gz"
	case ".xz":
		return "xz"
	case ".zst", ".zstd":
		return "zstd"
	default:
		return ""
	}
}

func readCoverFile(src string) ([]byte, error) {
	f, err := os.Open(src)
	if err != nil {
		return nil, fmt.Errorf("could not read coverprofile from %s, error was %v", src, err)
	}
	defer f.Close()

	var r io.Reader = f
	switch compressionKind(src) {
	case "gz":
		gr, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("could not decompress coverprofile %s, error was %v", src, err)
		}
		defer gr.Close()
		r = gr
	case "xz":
		xr, err := xz.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("could not decompress coverprofile %s, error was %v", src, err)
		}
		defer xr.Close()
		r = xr
	case "zstd":
		zr, _ := zstd.NewReader(f)
		defer zr.Close()
		r = zr
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("could not read coverprofile from %s, error was %v", src, err)
	}
	return data, nil
}

// combineCoverProfiles merges one or more Go coverage profile files into dst
// using "mode: set" so a block is marked covered if covered in any input profile.
func combineCoverProfiles(dst string, srcFiles []string) error {
	var keys []string
	covered := make(map[string]int)
	var out strings.Builder
	out.WriteString("mode: set\n")

	for _, src := range srcFiles {
		buf, err := readCoverFile(src)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(buf), "\n") {
			line = strings.TrimSuffix(line, "\r")
			if line == "" || strings.HasPrefix(line, "mode: ") {
				continue
			}
			if i := strings.LastIndexByte(line, ' '); i != -1 {
				if n, err := strconv.ParseUint(line[i+1:], 10, 64); err == nil {
					key := line[:i]
					if _, ok := covered[key]; !ok {
						keys = append(keys, key)
						covered[key] = 0
					}
					if n > 0 {
						covered[key] = 1
					}
					continue
				}
			}
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	for _, key := range keys {
		fmt.Fprintf(&out, "%s %d\n", key, covered[key])
	}
	return os.WriteFile(dst, []byte(out.String()), 0644)
}

type stringSlice []string

func (s *stringSlice) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSlice) Set(val string) error {
	*s = append(*s, val)
	return nil
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
