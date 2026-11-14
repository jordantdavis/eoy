# eoy

eoy stores evidence for mid-year and end-of-year reviews in a local SQLite file. Evidence can come from any source, for example Slack, email, or a company recognition tool. Each piece of evidence has a source property that tells where it came from. Merged GitHub pull requests are a special case: eoy fetches them from GitHub and keeps their repo, number, and author.

You can add many notes to one piece of evidence. You can tag evidence and notes.

Commands follow the kubectl shape: a verb, a resource type, then names and flags.

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

To sync PRs, you also need a GitHub token. eoy uses `GH_TOKEN`, then `GITHUB_TOKEN`, then the output of `gh auth token`.

## Resources

| Resource | Short names | Verbs | What it is |
|---|---|---|---|
| `evidence` | `ev` | get, describe, create, delete, tag | Evidence from any source, PRs included. |
| `pullrequests` | `pr`, `prs` | get, describe, delete, tag, sync | Evidence with the source `pr`, with PR columns. |
| `notes` | | get, describe, create, delete, tag | Notes on evidence. |
| `tags` | | get | Every tag with the number of items that have it. |
| `mannequins` | | get, create | The placeholder login of an org migration. |

Each resource also accepts its singular name. For example, `eoy get notes` and `eoy get note` are the same command. Run `eoy api-resources` to print this list.

Other commands act on eoy itself:

| Command | What it does |
|---|---|
| `eoy version` | Print the release tag and the short git commit. |
| `eoy update` | Replace the installed binary with the latest release. |
| `eoy api-resources` | List the resource types, their short names, and their verbs. |
| `eoy prime` | Print a guide for AI agents that use eoy. |
| `eoy init` | Add a Claude Code hook that runs `eoy prime`. |

Run `eoy --help` or `eoy VERB RESOURCE --help` for the flags of a command.

## Add evidence

To add one piece of evidence, give a source and a title:

```sh
eoy create evidence --source slack --date 2026-03-04 --url https://... --tag kudos Thanked by the infra team
```

You choose the source names. A source name has lowercase letters, digits, `.`, `_`, and `-`. The source `pr` is for GitHub PRs, so only `eoy sync prs` writes it. If you omit `--date`, the date is now. The date can be `YYYY-MM-DD`, `YYYY-MM-DDTHH:MM:SS`, or an RFC 3339 time. For a longer text, use `--body` or `--body-file`.

Each item gets an ID that starts with its source, for example `slack:6f1c2a9b0d3e4f51`.

### Import a file

To add many items, write them as JSON and import the file with `-f`:

```sh
eoy create evidence -f recognitions.json --source ace --tag imported
```

The file can hold one object, an array of objects, or one object per line (JSON Lines). To read standard input, use `-f -`. Each object has these fields:

| Field | Required | What it is |
|---|---|---|
| `source` | yes, or use `--source` | Where the evidence came from. |
| `title` | yes | A short summary. |
| `external_id` | no | The ID of the item in its source. |
| `body` | no | A longer text. |
| `url` | no | A link to the item. |
| `occurred_at` | no | When it happened. The default is now. |
| `tags` | no | An array of tags. |

`--source` sets the source of each object that has none. `--tag` adds tags to every object.

If an item has an `external_id`, you can import it again. eoy finds the stored item by its source and external ID, and updates it. It adds no duplicate, and the notes and tags stay. An import adds tags but never removes them. Without an `external_id`, each import adds a new item.

To import an export from another tool, for example a CSV file, convert it to this JSON format first. An AI agent can do the conversion for you.

## Sync your merged PRs

```sh
eoy sync prs --from 2026-01-01 --to 2026-06-30
```

If you omit `--from`, the sync starts on January 1 of this year. If you omit `--to`, the sync ends today. Dates are in UTC. You can run the sync again at any time. It updates changed PRs and adds no duplicates. Your notes and tags stay.

The sync searches for PRs by your GitHub handle. eoy uses the first value that it finds in this list:

1. The `--user` flag.
2. `github_user` in the configuration file.
3. The login of the active `gh` account.

To add more logins, for example an older account, use `--author LOGIN`. You can give the flag more than once. The sync also uses `mannequin` and `extra_authors` from the configuration file.

PRs are evidence with the source `pr`. Their IDs are `pr:` and the GitHub ID, for example `pr:123456789`. `eoy get evidence` shows them with all other evidence. `eoy get prs` shows only PRs, with the repo and the number.

### Find your mannequin

An org migration can replace the author of your old PRs with a mannequin. A mannequin is a placeholder account that GitHub creates during the migration. If you do not include it, the sync misses those PRs.

To find your mannequin, run this command. Set `--user` to the login that made the commits before the migration.

```sh
eoy get mannequins --user OLD_LOGIN --org YOUR_ORG
```

The command lists the mannequins that authored PRs with your commits, highest count first. If the top result is correct, save it to the configuration file:

```sh
eoy create mannequin LOGIN --org YOUR_ORG
```

Every later sync then includes it. To use a mannequin for one sync only, use `eoy sync prs --mannequin LOGIN`.

## Read your evidence

```sh
eoy get evidence --from 2026-01-01 --to 2026-06-30
eoy get evidence --source slack,email --tag kudos
eoy get prs -o wide
eoy describe evidence pr:123456789
```

`get` lists the items newest first. `--source` matches any of the sources that you give. Each `--tag` must match, so `--tag a --tag b` shows items with both tags. `describe` shows every field, tag, and note of an item.

The output flags follow kubectl:

- `-o json`: print JSON. For evidence, the JSON also has the body and the notes of each item.
- `-o wide`: add columns, for example the URL.
- `-o name`: print only the IDs, one per line.
- `--no-headers`: do not print the header row.

## Add notes and tags

```sh
eoy create note --evidence pr:123456789 Use this for the collaboration section
eoy create note -e slack:6f1c2a9b0d3e4f51 -f draft.md --tag draft
eoy get notes --evidence pr:123456789
eoy describe note 3
```

A piece of evidence can have many notes. Each note has a number as its ID. To read the text from a file, use `-f FILE`, or `-f -` for standard input.

To add or remove tags, use `eoy tag`. A tag that ends with `-` is removed, as in `kubectl label`.

```sh
eoy tag evidence pr:123456789 migration infra
eoy tag evidence pr:123456789 infra-
eoy tag note 3 draft- final
eoy get tags
```

Tags are lowercase. A tag has letters, digits, `.`, `_`, `/`, and `-`, and it cannot end with `-`.

## Delete evidence and notes

```sh
eoy delete evidence pr:123456789 slack:6f1c2a9b0d3e4f51
eoy delete prs --all
eoy delete note 3
```

Without `--force`, the command lists the matching items and changes nothing. To delete the items, run the command again with `--force`. A delete of evidence also removes its notes and tags. `eoy delete evidence --all` deletes all evidence. `eoy delete prs --all` deletes only PRs.

A later sync or import can add a deleted item back. The item comes back without its notes and tags.

## Use eoy with an AI agent

> [!NOTE]
> At this time, `eoy init` sets up only Claude Code. It does not support other AI agents. To use another agent, run `eoy prime` and give the output to the agent.

Run this command in the project directory where you work with Claude Code:

```sh
eoy init
```

The command adds a SessionStart hook to `.claude/settings.local.json`. That file is personal and is not committed. At the start of each session, the hook runs `eoy prime`. That command prints a guide that tells the agent how to use eoy and which steps need you. The hook also runs after a resume, a clear, and a compaction.

To write the hook to `.claude/settings.json`, the file that teams commit, use `eoy init --shared`. A second run changes nothing. The command keeps the rest of your configuration, but it can change the order of the keys.

`eoy init` never writes your user-level Claude Code configuration. If you run it in your home directory, it fails. The hook runs `eoy prime` by name, so `eoy` must be on your PATH. To read the guide yourself, run `eoy prime`.

## Files

> [!NOTE]
> eoy stores data only in a local SQLite file. It has no remote or shared storage, and it does not sync the database between computers. To move your data to another computer, copy the database file.

| File | Default path | Change it with |
|---|---|---|
| Database | `~/.local/share/eoy/eoy.db` | `--db` or `EOY_DB` |
| Configuration | `~/.config/eoy/config.toml` | `--config` or `EOY_CONFIG` |

If `XDG_DATA_HOME` or `XDG_CONFIG_HOME` is set, eoy uses it in place of `~/.local/share` or `~/.config`.

Example configuration file:

```toml
github_user = "my-handle"
mannequin = "4zvExampleMannequinLogin"
extra_authors = ["my-old-handle"]
org = "my-org"
```

You can query the database directly. The `evidence` table holds all evidence. The `pull_requests`, `notes`, `evidence_tags`, and `note_tags` tables refer to it.

```sh
sqlite3 ~/.local/share/eoy/eoy.db "select source, count(*) from evidence group by source"
```

Do not commit exports or the database, because they can contain the names of coworkers.

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

To run the tests, run `go test -race ./...`. The tests use a fake GitHub server and temporary databases.

## Release

The release workflow builds binaries for Linux and macOS, on amd64 and arm64. A push of a tag that starts with `v` starts it.

1. On `main`, run `git tag v1.0.0`.
2. Run `git push origin v1.0.0`.
3. When the workflow is done, open the Releases page of the repository on GitHub. The release has one `.tar.gz` archive per platform and a `checksums.txt` file.

The workflow sets the tag and the full commit hash in the binary with `-ldflags`.
