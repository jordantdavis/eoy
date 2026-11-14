package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// primeCommand is the hook command that eoy init adds.
const primeCommand = "eoy prime"

const primeGuide = `# eoy: evidence for performance reviews

eoy stores evidence for mid-year and end-of-year reviews in a local SQLite file.
Each piece of evidence has a source that says where it came from, for example
slack, email, or a company recognition tool. Merged GitHub PRs are evidence with
the source "pr". Evidence can have many notes. Evidence and notes can have tags.

Commands follow kubectl: eoy VERB RESOURCE [NAME...] [flags]. Run eoy
api-resources to see the resources and their verbs.

## Read evidence

Use -o json when you read output in a program or an agent.

- eoy get evidence --from 2026-01-01 --to 2026-06-30 -o json   (with bodies and notes)
- eoy get evidence --source slack,email --tag kudos
- eoy get prs -o wide                    (PR evidence with repo and author)
- eoy describe evidence ID               (every field, tag, and note)
- eoy get notes --evidence ID
- eoy get tags                           (every tag with counts)

## Add and annotate evidence

- eoy sync prs --from YYYY-MM-DD --to YYYY-MM-DD
- eoy create evidence --source SOURCE [--date D] [--url U] [--body B] [--external-id X] TITLE...
- eoy create evidence -f items.json      (one object, an array, or JSON Lines)
- eoy create note --evidence ID TEXT...  (an item can have many notes)
- eoy tag evidence ID TAG... and eoy tag note NOTE_ID TAG...  (TAG- removes a tag)

Each object in an import file has the fields source, title, external_id, body,
url, occurred_at, and tags. Only source and title are required. Set external_id
to the ID of the item in its source. Then you can import the file again, and eoy
updates each item and adds no duplicates. Notes and tags stay after a re-import.
To import an export from another tool, such as a CSV file, convert it to this
JSON format.

If you omit --from, a sync starts on January 1 of this year. If you omit
--to, it ends today.

## Steps that only the user can do

- Exports from company tools usually need the user's login. Ask the user to
  export the data and give you the file paths.
- If eoy sync prs misses old PRs from before an org migration, the user can
  have a mannequin login. Suggest eoy get mannequins --user OLD_LOGIN --org ORG.

## Rules

- eoy delete without --force only lists the items and changes nothing. Ask the
  user before you add --force. A delete of evidence also removes its notes and tags.
- Ask the user before you run eoy create mannequin, because it writes the
  configuration file.
- Never commit exports or the database. They can contain the names of coworkers.

## Workflow for a review

1. Run eoy sync prs for the review period.
2. Ask the user for exports from other sources. Convert them and run
   eoy create evidence -f.
3. Run eoy get evidence --from ... --to ... -o json to read the evidence.
4. With the user, tag evidence by theme and add notes.
5. Draft the review from eoy get evidence -o json.

For all flags, run eoy VERB RESOURCE --help.
`

func newPrimeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prime",
		Short: "Print instructions for AI agents that use eoy",
		Long: `Print a guide that tells an AI agent how to use eoy. The hook from
"eoy init" runs this command at the start of each Claude Code session.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), primeGuide)
			return err
		},
	}
}

func newInitCmd() *cobra.Command {
	var shared bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Add a Claude Code SessionStart hook that runs eoy prime in this project",
		Long: `Add a SessionStart hook to the Claude Code settings of the project in the
current directory. The hook runs "eoy prime", so each session starts with the
eoy guide. eoy must be on your PATH.

By default, the hook goes into .claude/settings.local.json, which is personal
and not committed. With --shared, it goes into .claude/settings.json.

eoy init never writes your user-level settings in ~/.claude. A second run
changes nothing. eoy init keeps all other settings, but it can change the
order of the keys in the file.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			name := "settings.local.json"
			if shared {
				name = "settings.json"
			}
			path := filepath.Join(cwd, ".claude", name)
			if err := refuseUserLevel(cwd); err != nil {
				return err
			}
			settings, err := readSettingsFile(path)
			if err != nil {
				return err
			}
			changed, err := addPrimeHook(settings)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if !changed {
				fmt.Fprintf(cmd.OutOrStdout(), "SessionStart hook already present in %s\n", path)
				return nil
			}
			if err := writeSettingsFile(path, settings); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added SessionStart hook to %s\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&shared, "shared", false, "write .claude/settings.json, the file that teams commit")
	return cmd
}

// refuseUserLevel fails when dir is the home directory, because
// ~/.claude/settings.json is the user-level Claude Code settings file.
func refuseUserLevel(dir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if samePath(dir, home) {
		return errors.New("the current directory is your home directory, so the file is your user-level Claude Code settings. Run eoy init in a project directory")
	}
	return nil
}

// samePath reports whether a and b name the same directory after symlinks.
func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// readSettingsFile returns the JSON object in path, or an empty object when
// the file does not exist.
func readSettingsFile(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var settings map[string]any
	if err := json.Unmarshal(b, &settings); err != nil {
		return nil, fmt.Errorf("%s: not valid JSON, so eoy init changed nothing: %w", path, err)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	return settings, nil
}

func writeSettingsFile(path string, settings map[string]any) error {
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// addPrimeHook adds a SessionStart command hook for "eoy prime" to settings.
// It has no matcher, so it runs on startup, resume, clear, and compact. It
// returns false when the hook is already present. It fails, and changes
// nothing, when hooks or hooks.SessionStart has an unexpected type.
func addPrimeHook(settings map[string]any) (bool, error) {
	hooks := map[string]any{}
	if raw, ok := settings["hooks"]; ok {
		m, isObject := raw.(map[string]any)
		if !isObject {
			return false, errors.New(`"hooks" is not a JSON object, so eoy init changed nothing`)
		}
		hooks = m
	}
	var entries []any
	if raw, ok := hooks["SessionStart"]; ok {
		list, isArray := raw.([]any)
		if !isArray {
			return false, errors.New(`"hooks.SessionStart" is not a JSON array, so eoy init changed nothing`)
		}
		entries = list
	}
	if hasPrimeHook(entries) {
		return false, nil
	}
	entry := map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": primeCommand}},
	}
	hooks["SessionStart"] = append(entries, entry)
	settings["hooks"] = hooks
	return true, nil
}

func hasPrimeHook(entries []any) bool {
	for _, e := range entries {
		entry, _ := e.(map[string]any)
		list, _ := entry["hooks"].([]any)
		for _, h := range list {
			hook, _ := h.(map[string]any)
			if hook["type"] == "command" && hook["command"] == primeCommand {
				return true
			}
		}
	}
	return false
}
