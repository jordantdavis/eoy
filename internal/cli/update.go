package cli

import (
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/jordantdavis/eoy/internal/update"
	"github.com/jordantdavis/eoy/internal/version"
)

// newUpdater returns the Updater for the running binary. Tests replace it
// to use a fake release server.
var newUpdater = func(cmd *cobra.Command) (*update.Updater, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &update.Updater{
		Client:  &http.Client{Timeout: 2 * time.Minute},
		BaseURL: update.RepoURL,
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
		Exe:     exe,
		Current: version.Version(),
		Out:     cmd.OutOrStdout(),
	}, nil
}

func newUpdateCmd() *cobra.Command {
	var o update.Options
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Replace eoy with the latest release",
		Long: "Download the release archive for this OS and CPU architecture, compare it to checksums.txt,\n" +
			"run the new binary with \"version\", and then replace the binary that you ran.",
		Example: "  eoy update\n  eoy update --check\n  eoy update --version v1.0.0",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u, err := newUpdater(cmd)
			if err != nil {
				return err
			}
			return u.Run(cmd.Context(), o)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.Check, "check", false, "print the current and latest versions and change nothing; exit code 3 means a newer release exists")
	f.StringVar(&o.Tag, "version", "", "install this release tag, for example v1.0.0 (default: the latest release)")
	f.BoolVar(&o.Force, "force", false, "install even if the version is the same or this is a development build")
	return cmd
}
