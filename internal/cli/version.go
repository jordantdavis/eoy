package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jordantdavis/eoy/internal/version"
)

func newVersionCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version and git commit of eoy",
		Long: "Print the release tag and the short git commit of eoy, for example \"v1.2.0 (8a2d7ee)\".\n" +
			"\"eoy update\" runs this command to test a new binary, so the tag is always first.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := version.Get()
			switch output {
			case "":
				_, err := fmt.Fprintln(cmd.OutOrStdout(), info)
				return err
			case "json":
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(info)
			default:
				return usageErrorf("unknown output format %q; valid formats: json", output)
			}
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "output format: json")
	return cmd
}
