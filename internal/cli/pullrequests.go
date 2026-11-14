package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordantdavis/eoy/internal/config"
	"github.com/jordantdavis/eoy/internal/gh"
	"github.com/jordantdavis/eoy/internal/store"
)

func pullRequestResource() Resource {
	k := prEvidence
	return Resource{
		Name: "pullrequest", Plural: "pullrequests", ShortNames: []string{"pr", "prs"},
		Commands: map[string]func() *cobra.Command{
			"get":      func() *cobra.Command { return getEvidenceCmd(k) },
			"describe": func() *cobra.Command { return describeEvidenceCmd(k) },
			"delete":   func() *cobra.Command { return deleteEvidenceCmd(k) },
			"tag":      func() *cobra.Command { return tagEvidenceCmd(k) },
			"sync":     syncPRsCmd,
		},
	}
}

func syncPRsCmd() *cobra.Command {
	var from, to, user, mannequin string
	var authors []string
	cmd := &cobra.Command{
		Use:   "",
		Short: "Fetch your merged PRs from GitHub and store them as evidence",
		Long: `Fetch merged PRs and store them as evidence with the source "pr". A second
sync updates changed PRs and never adds duplicates. Notes and tags stay.

The search uses your GitHub handle: --user, else github_user in the
configuration file, else the login of the active gh account. It also uses
your mannequin login (--mannequin, else mannequin in the configuration file)
and the extra_authors of the configuration file. Run "eoy get mannequins" to
find your mannequin.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			r, err := yearRange(from, to)
			if err != nil {
				return err
			}
			cfg, _, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			client, err := newGitHub(ctx, logf(cmd))
			if err != nil {
				return err
			}
			handle, err := githubHandle(ctx, client, user, cfg)
			if err != nil {
				return err
			}
			if mannequin == "" {
				mannequin = cfg.Mannequin
			}
			all := uniqueNonEmpty(append(append([]string{handle, mannequin}, cfg.ExtraAuthors...), authors...))
			logf(cmd)("searching merged PRs from %s to %s for %s\n", r.From, r.To, strings.Join(all, ", "))

			prs, err := client.MergedPRs(ctx, all, r)
			if err != nil {
				return err
			}
			return withStore(cmd, func(s *store.Store) error {
				counts, err := s.UpsertPRs(ctx, prs, now())
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "pullrequests: %s\n", formatCounts(counts))
				return nil
			})
		},
	}
	addRangeFlags(cmd, &from, &to, true)
	cmd.Flags().StringVar(&user, "user", "", "your GitHub handle (default: github_user, else the gh login)")
	cmd.Flags().StringVar(&mannequin, "mannequin", "", "your mannequin login (default: mannequin in the configuration file)")
	cmd.Flags().StringSliceVar(&authors, "author", nil, "more logins to include, can repeat")
	return cmd
}

func githubHandle(ctx context.Context, client *gh.Client, flag string, cfg config.Config) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if cfg.GitHubUser != "" {
		return cfg.GitHubUser, nil
	}
	return client.Login(ctx)
}

func uniqueNonEmpty(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		key := strings.ToLower(s)
		if s == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

func mannequinResource() Resource {
	return Resource{
		Name: "mannequin", Plural: "mannequins",
		Commands: map[string]func() *cobra.Command{
			"get":    getMannequinsCmd,
			"create": createMannequinCmd,
		},
	}
}

func getMannequinsCmd() *cobra.Command {
	var user, org string
	var maxCommits int
	var out output
	cmd := &cobra.Command{
		Use:   "",
		Short: "Find the placeholder login that an org migration made for you",
		Long: `An org migration replaces the author of old PRs with a "mannequin", a
placeholder account. Commits keep the real login, so this command finds
commits by --user in --org, finds the PRs that contain them, and counts the
PR authors that are mannequins. The top result is most likely yours.

Set --user to the login that made the commits before the migration. That can
be an older account, not your current handle. To include a mannequin in
every "eoy sync prs", run "eoy create mannequin LOGIN".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := out.check(); err != nil {
				return err
			}
			ctx := cmd.Context()
			cfg, _, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			if org == "" {
				org = cfg.Org
			}
			if org == "" {
				return usageErrorf("give --org, for example --org my-company")
			}
			client, err := newGitHub(ctx, logf(cmd))
			if err != nil {
				return err
			}
			login, err := githubHandle(ctx, client, user, cfg)
			if err != nil {
				return err
			}
			cands, err := client.FindMannequins(ctx, login, org, maxCommits)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			switch out.format {
			case "json":
				if cands == nil {
					cands = []gh.Candidate{}
				}
				return writeJSON(w, cands)
			case "name":
				for _, c := range cands {
					fmt.Fprintln(w, c.Login)
				}
				return nil
			}
			if len(cands) == 0 {
				return fmt.Errorf("no mannequin found: the PRs with commits by %s have real authors. Try an older login with --user", login)
			}
			t := newTable(w, out, "LOGIN", "PRS", "NAME")
			for _, c := range cands {
				t.row(c.Login, fmt.Sprint(c.PRs), c.Name)
			}
			if err := t.flush(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "To include the top result in every sync, run: eoy create mannequin %s --org %s\n", cands[0].Login, org)
			return nil
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "login that made the commits (default: your GitHub handle)")
	cmd.Flags().StringVar(&org, "org", "", "GitHub org to search (default: org in the configuration file)")
	cmd.Flags().IntVar(&maxCommits, "max-commits", 30, "how many commits to test")
	addOutputFlags(cmd, &out, true, "json", "name")
	return cmd
}

func createMannequinCmd() *cobra.Command {
	var org string
	cmd := &cobra.Command{
		Use:   "LOGIN",
		Short: "Save your mannequin login, so that every PR sync includes it",
		Long: `Write the mannequin login to the configuration file. Every later
"eoy sync prs" then includes the PRs of that login. With --org, the org also
becomes the default for "eoy get mannequins".`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			cfg.Mannequin = args[0]
			if org != "" {
				cfg.Org = org
			}
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "mannequin/%s saved to %s\n", args[0], path)
			return nil
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "also save this org as the default")
	return cmd
}
