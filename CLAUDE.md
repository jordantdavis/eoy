# CLAUDE.md

Claude Code (claude.ai/code) reads this file for guidance on the code in this repository.

## Project

eoy is a single-binary command line tool written in Go with Cobra. Its commands follow kubectl: `eoy VERB RESOURCE [NAME...] [flags]`. `README.md` is the user documentation. If you change behavior, update `README.md` too.

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

## Architecture

- `main.go` calls `cli.Execute` with `cli.NewRootCmd(cli.Resources())`. `Execute` returns the exit code: 0 success, 1 error, 2 usage, 3 `update --check` found a newer release. These codes are public. Do not change them.
- `internal/cli/resource.go` holds the kubectl layer. A `Resource` has a singular name, a plural name, short names and a map from verb to a leaf command builder. `newVerbCmds` makes one parent command per verb in `Verbs` and adds each resource as a subcommand. The plural name is the `Use`, and the other names are aliases. A verb with no resources is not added. `api-resources` lists the resources.
- To add a resource, write a file in `internal/cli` that returns a `Resource`, and add it to `Resources()` in `resources.go`. The leaf `Use` gives only the arguments, for example `[NAME...]`. Test it with `run()` in `cli_test.go`.
- Usage errors: return `usageErrorf(...)` from a command for a bad command line. Flag errors and cobra's argument validators also map to exit 2. Parent commands use `unknownSubcommand` as `Args`, because cobra otherwise prints help and exits 0 for an unknown subcommand.
- `version` and `update` act on eoy itself, like `kubectl version`. They are top-level commands, not resources.
- `internal/version` resolves the version. The `-ldflags` values come first. Then it uses the build info that Go embeds: `vcs.revision`, `vcs.modified`, and the module version for `go install ...@vX.Y.Z`. It ignores pseudo-versions, so a local build reports `dev`. `Info.String()` must start with the tag, because `update` checks that.
- `internal/update` does the same steps as `install.sh`. It finds the latest tag from the `releases/latest` redirect. It compares the SHA-256 sum with `checksums.txt`. Then it runs `<new binary> version` and renames the new binary over `os.Executable()`. The asset names (`eoy_<tag>_<os>_<arch>.tar.gz` with folder `eoy_<tag>_<os>_<arch>/eoy`) must match `release.yml` and `install.sh`. Every release must keep the `version` command, because `update` uses it as a smoke test.

## Tests

`internal/update/update_test.go` has `fakeRelease`, a fake GitHub server, and `newTestUpdater`. `internal/cli/cli_test.go` has `fakeResource` and `run`. Tests replace the `newUpdater` variable to avoid the network. Use these helpers for new tests.
