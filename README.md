# diffcov

`diffcov` checks whether tests exercise recently added or modified Go code, such as staged changes before pushing to a shared repository.

It compares a diff against a test coverage profile and reports any new or modified statements not exercised by tests.

## Installation

```sh
go install dr2cha.se/diffcov/cmd/diffcov@latest
```

## Quick Start

Run with no arguments in your repository or package directory:

```sh
diffcov
```

With no options, `diffcov` automatically:
1. Detects the revision control system (`git` or `jj`).
2. Runs `git diff` (or `jj diff --git`).
3. Runs `go test -coverprofile ... .`.
4. Reports untested statements from the diff.

## Common Usage

### Check staged changes before pushing

```sh
diffcov -staged
```

(`-cached` is supported as an alias).

### Check entire source files instead of a diff

```sh
# Check all non-test .go files in the current directory
diffcov -a

# Check specific source files or directories
diffcov -a foo.go bar.go
```

### Include tested lines in output

```sh
diffcov -t
```

### Use existing coverage or diff files

```sh
# Using an existing coverage profile
diffcov -c cover.out

# Using application coverage data (GOCOVERDIR)
diffcov -C coverdir

# Combining multiple coverage sources (-c may be repeated and used with -C)
diffcov -c unit.out -c integration.out -C coverdir

# Using an existing diff file
diffcov changes.diff

# Using both
diffcov -c cover.out changes.diff
```

## Revision Control Systems

`diffcov` supports Git and Jujutsu (`jj`).

VCS is autodetected by searching upward for `.git` or `.jj`. When both exist (colocated repository), `jj` is preferred if available in `PATH`.

Force a specific VCS with:
- `-git`: Use Git.
- `-jj`: Use Jujutsu.
- `-vcs git` or `-vcs jj`: Explicitly select VCS.

Note: `-staged` and `-cached` require Git; Jujutsu does not have a staging area.

## Command-Line Options

| Flag | Description |
| --- | --- |
| `-a` | Check coverage of entire source file(s) instead of using diff input. |
| `-staged`, `-cached` | Use `git diff --staged` to obtain diff (Git only). |
| `-t` | Also show tested lines. |
| `-c <file>` | Path to coverage profile (`go test -coverprofile` output; `.gz`, `.xz`, `.zst`/`.zstd` auto-decompressed); may be repeated and combined with `-C`. |
| `-C <dir>` | Path to application coverage directory (`GOCOVERDIR`), converted via `go tool covdata`; may be combined with `-c`. |
| `-coverpkg <pattern>` | Package pattern for `go test -coverpkg` (e.g. `all` or `./...`). |
| `-d <file>` | Path to diff file (or pass as a single positional argument). |
| `-g <cmd>` | Go command to use for running tests (default `go`). |
| `-git` | Use Git to obtain diff. |
| `-jj` | Use Jujutsu (`jj`) to obtain diff. |
| `-vcs <git\|jj>` | Revision control system to use (`git` or `jj`). |
| `-v` | Verbose output; repeat for increased verbosity. |
| `-D <dir>` | Diff directory root (defaults to parent search). |
| `-M <dir>` | Directory containing `go.mod` (defaults to parent search). |
| `-S <n>` | Number of leading directories to strip from diff file paths. |
