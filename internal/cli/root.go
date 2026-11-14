// Package cli holds the eoy command tree. Commands follow kubectl:
//
//	eoy VERB RESOURCE [NAME...] [flags]
//
// A resource registers one leaf command per verb that it supports (see
// Resource). The top-level commands "version", "update", "api-resources",
// "prime" and "init" act on eoy itself, like "kubectl version".
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordantdavis/eoy/internal/update"
)

// Exit codes. They are part of the public interface, so do not change them.
const (
	ExitOK              = 0
	ExitError           = 1 // the command failed
	ExitUsage           = 2 // the command line is not correct
	ExitUpdateAvailable = 3 // "update --check" found a newer release
)

// usageError marks an error in the command line. Execute maps it to ExitUsage.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

func usageErrorf(format string, a ...any) error {
	return usageError{fmt.Errorf(format, a...)}
}

// NewRootCmd returns the eoy command tree with the given resources.
func NewRootCmd(resources []Resource) *cobra.Command {
	root := &cobra.Command{
		Use:   "eoy",
		Short: "Store evidence for mid-year and end-of-year reviews",
		Long: "eoy stores evidence for mid-year and end-of-year reviews in a local SQLite file.\n" +
			"Evidence can come from any source. Merged GitHub PRs have their own resource.\n\n" +
			"Commands follow kubectl: eoy VERB RESOURCE [NAME...] [flags].\n" +
			"Run \"eoy api-resources\" to see the resources and their verbs.",
		SilenceErrors: true,
		SilenceUsage:  true,
		// Without RunE, cobra prints help and exits 0 for an unknown command.
		Args: unknownSubcommand("command"),
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	root.CompletionOptions.HiddenDefaultCmd = true
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	addDataFlags(root)

	if verbs := newVerbCmds(resources); len(verbs) > 0 {
		root.AddGroup(&cobra.Group{ID: groupBasic, Title: "Basic commands:"})
		for _, v := range verbs {
			v.GroupID = groupBasic
			root.AddCommand(v)
		}
	}
	root.AddGroup(&cobra.Group{ID: groupOther, Title: "Other commands:"})
	root.SetHelpCommandGroupID(groupOther)
	root.SetCompletionCommandGroupID(groupOther)
	for _, c := range []*cobra.Command{newAPIResourcesCmd(resources), newVersionCmd(), newUpdateCmd(), newPrimeCmd(), newInitCmd()} {
		c.GroupID = groupOther
		root.AddCommand(c)
	}
	return root
}

const (
	groupBasic = "basic"
	groupOther = "other"
)

// Execute runs the command tree with args and returns the process exit code.
func Execute(ctx context.Context, root *cobra.Command, args []string, stdout, stderr io.Writer) int {
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	cmd, err := root.ExecuteContextC(ctx)
	return exitCode(cmd, err, stderr)
}

func exitCode(cmd *cobra.Command, err error, stderr io.Writer) int {
	if err == nil {
		return ExitOK
	}
	if errors.Is(err, update.ErrUpdateAvailable) {
		return ExitUpdateAvailable
	}
	fmt.Fprintln(stderr, "error:", err)
	var ue usageError
	if errors.As(err, &ue) || isCobraUsageError(err) {
		if cmd != nil {
			fmt.Fprintf(stderr, "Run '%s --help' for usage.\n", cmd.CommandPath())
		}
		return ExitUsage
	}
	return ExitError
}

// isCobraUsageError matches the argument errors that cobra's built-in
// validators, such as cobra.ExactArgs, return. They have no error type.
func isCobraUsageError(err error) bool {
	s := err.Error()
	return strings.HasPrefix(s, "accepts ") || strings.HasPrefix(s, "requires at least ") ||
		strings.HasPrefix(s, "unknown command ") || strings.HasPrefix(s, "invalid argument ")
}

// unknownSubcommand returns an Args function for a parent command. It
// rejects any argument, because a valid one is resolved to a subcommand
// before Args runs. kind names the subcommand in the error, for example
// "resource type".
func unknownSubcommand(kind string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		msg := fmt.Sprintf("unknown %s %q for %q", kind, args[0], cmd.CommandPath())
		if cmd.SuggestionsMinimumDistance <= 0 {
			cmd.SuggestionsMinimumDistance = 2 // cobra's default, which it sets only on the root
		}
		if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
			msg += "; did you mean " + strings.Join(s, " or ") + "?"
		}
		return usageError{errors.New(msg)}
	}
}
