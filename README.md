# eoy

eoy is a command line tool. Its commands follow the kubectl shape: a verb, a resource type, then names and flags.

```sh
eoy VERB RESOURCE [NAME...] [flags]
```

## Install

On Linux or macOS, run this command to install the latest release:

```sh
curl -fsSL https://raw.githubusercontent.com/jordantdavis/eoy/main/install.sh | sh
```

The script finds your OS and CPU architecture and downloads the matching archive. It compares the archive to `checksums.txt`. It puts `eoy` in `~/.local/bin`. If that folder is not in your `PATH`, the script tells you the line to add to your shell profile.

You can set two environment variables:

- `VERSION`: the release tag to install, for example `v1.0.0`. The default is the latest release.
- `INSTALL_DIR`: the folder for the binary. The default is `$HOME/.local/bin`.

## Commands

| Command | What it does |
|---|---|
| `eoy version` | Print the release tag and the short git commit. |
| `eoy update` | Replace the installed binary with the latest release. |
| `eoy api-resources` | List the resource types, their short names, and their verbs. |
| `eoy get RESOURCE` | Show resources. |
| `eoy describe RESOURCE` | Show details of a resource. |
| `eoy create RESOURCE` | Create a resource. |
| `eoy delete RESOURCE` | Delete resources. |

If no resource supports a verb, eoy does not show that verb.

Each resource has a plural name, a singular name, and optional short names. All of them select the same command. For example, `eoy get routes`, `eoy get route` and `eoy get rt` are the same command.

Run `eoy --help` or `eoy COMMAND --help` for the flags of a command.

## Version

```sh
eoy version
eoy version -o json
```

The output is the release tag and the short git commit, for example `v1.2.0 (8a2d7ee)`. If the binary was built from a working tree with changes, the commit ends with `-dirty`. A local build has the version `dev`. `-o json` also shows the Go version and the platform.

## Update

To replace the installed binary with the latest release, run:

```sh
eoy update
```

The command downloads the archive for your OS and CPU architecture and compares it to `checksums.txt`. It runs the new binary with `version` to make sure that it works. Then it replaces the file that you ran. The command must be able to write to the folder of the binary.

Flags for `update`:

- `--check`: print the current and the latest version, and do not change a file. If you have the latest version, the exit code is 0. If a newer release exists, the exit code is 3.
- `--version`: install this release tag, for example `v1.0.0`. You can use it to go back to an older release.
- `--force`: install the release. If you already have that version, eoy installs it again. A binary that you built from source has the version `dev`. To replace it, you must use `--force` or `--version`.

## Exit codes

- 0: the command succeeded.
- 1: the command failed.
- 2: the command line is not correct.
- 3: `eoy update --check` found a newer release.

## Build from source

1. In this folder, run `mise install` to get the Go version in `mise.toml`.
2. Run `go build -o eoy .`
3. Run `./eoy version`.

To run the tests, run `go test -race ./...`.

## Release

The release workflow builds binaries for Linux and macOS, on amd64 and arm64. A push of a tag that starts with `v` starts it.

1. On `main`, run `git tag v1.0.0`.
2. Run `git push origin v1.0.0`.
3. When the workflow is done, open the Releases page of the repository on GitHub. The release has one `.tar.gz` archive per platform and a `checksums.txt` file.

The workflow sets the tag and the full commit hash in the binary with `-ldflags`.
