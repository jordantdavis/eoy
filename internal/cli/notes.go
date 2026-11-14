package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordantdavis/eoy/internal/store"
)

func noteResource() Resource {
	return Resource{
		Name: "note", Plural: "notes",
		Commands: map[string]func() *cobra.Command{
			"get":      getNotesCmd,
			"describe": describeNotesCmd,
			"create":   createNoteCmd,
			"delete":   deleteNotesCmd,
			"tag":      tagNoteCmd,
		},
	}
}

func parseNoteIDs(args []string) ([]int64, error) {
	ids := make([]int64, len(args))
	for i, a := range args {
		id, err := strconv.ParseInt(a, 10, 64)
		if err != nil || id <= 0 {
			return nil, usageErrorf("note ID %q: want a number. Run eoy get notes to see IDs", a)
		}
		ids[i] = id
	}
	return ids, nil
}

func createNoteCmd() *cobra.Command {
	var evidenceID, file string
	var tags []string
	var out output
	cmd := &cobra.Command{
		Use:   "[TEXT...]",
		Short: "Add a note to a piece of evidence",
		Long: `Add a note to a piece of evidence. One piece of evidence can have many
notes. Give the text as arguments, or read it from a file with -f.`,
		Example: `  eoy create note --evidence pr:123456789 Use this for the collaboration section
  eoy create note -e slack:6f1c2a9b0d3e4f51 -f draft.md --tag draft`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := out.check(); err != nil {
				return err
			}
			if evidenceID == "" {
				return usageErrorf("give --evidence ID")
			}
			body := strings.Join(args, " ")
			if file != "" {
				if body != "" {
					return usageErrorf("give the text as arguments or with -f, not both")
				}
				b, err := readFileOrStdin(cmd, file)
				if err != nil {
					return err
				}
				body = string(b)
			}
			if strings.TrimSpace(body) == "" {
				return usageErrorf("give the note text, or -f FILE")
			}
			return withStore(cmd, func(s *store.Store) error {
				n, err := s.AddNote(cmd.Context(), evidenceID, body, tags, now())
				if err != nil {
					return err
				}
				switch out.format {
				case "json":
					return writeJSON(cmd.OutOrStdout(), n)
				case "name":
					fmt.Fprintln(cmd.OutOrStdout(), n.ID)
				default:
					fmt.Fprintf(cmd.OutOrStdout(), "note/%d created\n", n.ID)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&evidenceID, "evidence", "e", "", "ID of the evidence that the note is about")
	cmd.Flags().StringVarP(&file, "filename", "f", "", "read the text from this file, or - for standard input")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "tags to add, can repeat")
	addOutputFlags(cmd, &out, false, "json", "name")
	return cmd
}

func getNotesCmd() *cobra.Command {
	var evidenceID string
	var tags []string
	var out output
	cmd := &cobra.Command{
		Use:   "[ID...]",
		Short: "List notes, oldest first",
		Long:  "List notes, oldest first. Several --tag flags must all match.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := out.check(); err != nil {
				return err
			}
			ids, err := parseNoteIDs(args)
			if err != nil {
				return err
			}
			return withStore(cmd, func(s *store.Store) error {
				if len(ids) > 0 {
					if _, err := s.ResolveNotes(cmd.Context(), ids); err != nil {
						return err
					}
				}
				notes, err := s.ListNotes(cmd.Context(), store.NoteFilter{IDs: ids, EvidenceID: evidenceID, Tags: tags})
				if err != nil {
					return err
				}
				w := cmd.OutOrStdout()
				switch out.format {
				case "json":
					return writeJSON(w, notes)
				case "name":
					for _, n := range notes {
						fmt.Fprintln(w, n.ID)
					}
					return nil
				}
				width := 60
				if out.format == "wide" {
					width = 0
				}
				t := newTable(w, out, "ID", "EVIDENCE", "CREATED", "TAGS", "NOTE")
				for _, n := range notes {
					t.row(fmt.Sprint(n.ID), n.EvidenceID, day(n.CreatedAt), orNone(n.Tags), oneLine(n.Body, width))
				}
				return t.flush()
			})
		},
	}
	cmd.Flags().StringVarP(&evidenceID, "evidence", "e", "", "only notes on this evidence")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "only notes with all of these tags")
	addOutputFlags(cmd, &out, true, "json", "wide", "name")
	return cmd
}

func describeNotesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ID...",
		Short: "Show notes with the evidence that they are about",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseNoteIDs(args)
			if err != nil {
				return err
			}
			return withStore(cmd, func(s *store.Store) error {
				notes, err := s.ResolveNotes(cmd.Context(), ids)
				if err != nil {
					return err
				}
				w := cmd.OutOrStdout()
				for i, n := range notes {
					e, err := s.Get(cmd.Context(), n.EvidenceID)
					if err != nil {
						return err
					}
					if i > 0 {
						fmt.Fprintln(w)
					}
					fmt.Fprintf(w, "%-10s%d\n", "ID:", n.ID)
					fmt.Fprintf(w, "%-10s%s  %s\n", "Evidence:", e.ID, oneLine(e.Title, 60))
					fmt.Fprintf(w, "%-10s%s\n", "Created:", n.CreatedAt)
					fmt.Fprintf(w, "%-10s%s\n", "Tags:", orNone(n.Tags))
					fmt.Fprintln(w, "Note:")
					fmt.Fprintln(w, indent(n.Body, "  "))
				}
				return nil
			})
		},
	}
}

func deleteNotesCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "ID...",
		Short: "Delete notes. Without --force, only list them",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseNoteIDs(args)
			if err != nil {
				return err
			}
			return withStore(cmd, func(s *store.Store) error {
				notes, err := s.ResolveNotes(cmd.Context(), ids)
				if err != nil {
					return err
				}
				w := cmd.OutOrStdout()
				if !force {
					t := newTable(w, output{}, "ID", "EVIDENCE", "NOTE")
					for _, n := range notes {
						t.row(fmt.Sprint(n.ID), n.EvidenceID, oneLine(n.Body, 60))
					}
					if err := t.flush(); err != nil {
						return err
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "not deleted: run again with --force to delete %s\n", plural(len(notes), "note"))
					return nil
				}
				n, err := s.DeleteNotes(cmd.Context(), ids)
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "deleted %s\n", plural(n, "note"))
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "delete for real")
	return cmd
}

func tagNoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ID TAG...",
		Short:   "Add or remove tags on a note",
		Long:    "Add tags to one note. A tag that ends with '-' is removed, as in kubectl label.",
		Example: "  eoy tag note 3 draft\n  eoy tag note 3 draft- final",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseNoteIDs(args[:1])
			if err != nil {
				return err
			}
			add, remove := parseTagArgs(args[1:])
			return withStore(cmd, func(s *store.Store) error {
				if err := s.TagNote(cmd.Context(), ids[0], add, remove); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "note/%d tagged\n", ids[0])
				return nil
			})
		},
	}
}

func tagResource() Resource {
	return Resource{
		Name: "tag", Plural: "tags",
		Commands: map[string]func() *cobra.Command{"get": getTagsCmd},
	}
}

func getTagsCmd() *cobra.Command {
	var out output
	cmd := &cobra.Command{
		Use:   "",
		Short: "List every tag with the number of evidence items and notes that have it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := out.check(); err != nil {
				return err
			}
			return withStore(cmd, func(s *store.Store) error {
				tags, err := s.Tags(cmd.Context())
				if err != nil {
					return err
				}
				w := cmd.OutOrStdout()
				switch out.format {
				case "json":
					return writeJSON(w, tags)
				case "name":
					for _, c := range tags {
						fmt.Fprintln(w, c.Tag)
					}
					return nil
				}
				t := newTable(w, out, "TAG", "EVIDENCE", "NOTES")
				for _, c := range tags {
					t.row(c.Tag, fmt.Sprint(c.Evidence), fmt.Sprint(c.Notes))
				}
				return t.flush()
			})
		},
	}
	addOutputFlags(cmd, &out, true, "json", "name")
	return cmd
}
