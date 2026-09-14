# diffcov

`diffcov` checks whether tests exercise recently added or modified Go code, such as staged changes before pushing to a shared repository.

It compares a diff against a test coverage profile and reports any new or modified statements not exercised by tests.

## Installation

```sh
go install github.com/dr2chase/diffcov/cmd/diffcov@latest
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

### Include tested lines in output

```sh
diffcov -t
```

### Use existing coverage or diff files

```sh
# Using an existing coverage profile
diffcov -c cover.out

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
| `-staged`, `-cached` | Use `git diff --staged` to obtain diff (Git only). |
| `-t` | Also show tested lines. |
| `-c <file>` | Path to coverage profile (`go test -coverprofile` output). |
| `-d <file>` | Path to diff file (or pass as a single positional argument). |
| `-git` | Use Git to obtain diff. |
| `-jj` | Use Jujutsu (`jj`) to obtain diff. |
| `-vcs <git\|jj>` | Revision control system to use (`git` or `jj`). |
| `-v` | Verbose output; repeat for increased verbosity. |
| `-D <dir>` | Diff directory root (defaults to parent search). |
| `-M <dir>` | Directory containing `go.mod` (defaults to parent search). |
| `-S <n>` | Number of leading directories to strip from diff file paths. |
