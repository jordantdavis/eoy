# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

eoy is a single-binary command line tool written in Go with Cobra. `README.md` is the user documentation. If you change behavior, update `README.md` too.

## Commands

The Go version comes from `go.mod`. If Go is not on the machine, run `mise install`.

```sh
go build -o eoy .                   # build (the binary is gitignored)
go test -race ./...                 # all tests, as CI runs them
go test -race -run TestUsageErrors ./internal/cli
gofmt -l .                          # CI fails if this prints any file
go mod tidy -diff                   # CI fails if go.mod or go.sum is not tidy
go vet ./...
shellcheck install.sh               # CI runs this too
```

CI (`.github/workflows/ci.yml`) runs gofmt, `go mod tidy -diff`, vet, `go test -race`, build and shellcheck. The release workflow runs on a `v*` tag. It reuses CI, then builds linux and darwin binaries for amd64 and arm64. It sets `internal/version.version` to the tag and `internal/version.commit` to the commit with `-ldflags`.

## Command structure

Every command that acts on data follows kubectl:

```
eoy VERB RESOURCE [NAME...] [flags]
```

- VERB is one of the entries in `Verbs` in `internal/cli/resource.go`: `get`, `describe`, `create`, `delete`. Add a new verb there, not as a top-level command.
- RESOURCE is the plural name, the singular name, or a short name. All three select the same command. For example, `eoy get routes`, `eoy get route` and `eoy get rt` are one command.
- NAME is zero or more resource names. Flags can come before or after the names.
- Output flags follow kubectl: `-o` selects the format (for example `-o json`), and `--no-headers` removes the table header.

Commands that act on eoy itself are top-level and take no resource, like `kubectl version`. These are `version`, `update` and `api-resources`. Do not add `eoy VERB` commands that bypass the resource layer, and do not add resource commands as top-level commands.

To add a resource:

1. Write a file in `internal/cli` that returns a `Resource`.
2. Set `Name` (singular), `Plural`, and `ShortNames`.
3. In `Commands`, map each verb to a function that returns a `*cobra.Command`. Its `Use` gives only the arguments, for example `[NAME...]`. `newVerbCmds` puts the plural name in front and adds the other names as aliases.
4. Add the resource to `Resources()` in `internal/cli/resources.go`.
5. Test it with `run()` in `internal/cli/cli_test.go`.

A verb that no resource supports does not appear in help. Today `Resources()` is empty, so only `version`, `update` and `api-resources` exist.

## Architecture

- `main.go` calls `cli.Execute` with `cli.NewRootCmd(cli.Resources())`. `Execute` returns the exit code: 0 success, 1 error, 2 usage, 3 `update --check` found a newer release. These codes are public. Do not change them.
- Usage errors: return `usageErrorf(...)` from a command for a bad command line. Flag errors and the cobra argument validators also map to exit 2. Parent commands use `unknownSubcommand` as `Args`. Without it, cobra prints help and exits 0 for an unknown subcommand.
- `internal/version` resolves the version. The `-ldflags` values come first. Then it uses the build info that Go embeds: `vcs.revision`, `vcs.modified`, and the module version for `go install ...@vX.Y.Z`. It ignores pseudo-versions, so a local build reports `dev`. `Info.String()` must start with the tag, because `update` checks that.
- `internal/update` does the same steps as `install.sh`. It finds the latest tag from the `releases/latest` redirect. It compares the SHA-256 sum with `checksums.txt`. Then it runs `<new binary> version` and renames the new binary over `os.Executable()`.
- The asset names (`eoy_<tag>_<os>_<arch>.tar.gz` with folder `eoy_<tag>_<os>_<arch>/eoy`) must match `release.yml`, `install.sh` and `internal/update`. Every release must keep the `version` command, because `update` uses it as a smoke test.

## Tests

`internal/update/update_test.go` has `fakeRelease`, a fake GitHub server, and `newTestUpdater`. `internal/cli/cli_test.go` has `fakeResource` and `run`. Tests replace the `newUpdater` variable to avoid the network. Use these helpers for new tests.
