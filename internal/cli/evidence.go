package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordantdavis/eoy/internal/store"
)

// evidenceKind selects which evidence a command acts on. The evidence
// resource acts on all of it. The pullrequests resource acts only on the
// evidence with the source "pr".
type evidenceKind struct {
	plural string // resource name in messages, for example "evidence"
	source string // "" for every source
}

var (
	allEvidence = evidenceKind{plural: "evidence"}
	prEvidence  = evidenceKind{plural: "pullrequests", source: store.PRSource}
)

func evidenceResource() Resource {
	k := allEvidence
	return Resource{
		Name: "evidence", Plural: "evidence", ShortNames: []string{"ev"},
		Commands: map[string]func() *cobra.Command{
			"get":      func() *cobra.Command { return getEvidenceCmd(k) },
			"describe": func() *cobra.Command { return describeEvidenceCmd(k) },
			"create":   createEvidenceCmd,
			"delete":   func() *cobra.Command { return deleteEvidenceCmd(k) },
			"tag":      func() *cobra.Command { return tagEvidenceCmd(k) },
		},
	}
}

// resolve returns the evidence for ids and fails if one is of another kind.
func (k evidenceKind) resolve(cmd *cobra.Command, s *store.Store, ids []string) ([]store.Evidence, error) {
	list, err := s.Resolve(cmd.Context(), ids)
	if err != nil {
		return nil, err
	}
	for _, e := range list {
		if k.source != "" && e.Source != k.source {
			return nil, fmt.Errorf("%s is not in %s: its source is %q. Use eoy %s evidence", e.ID, k.plural, e.Source, cmd.Parent().Name())
		}
	}
	return list, nil
}

func getEvidenceCmd(k evidenceKind) *cobra.Command {
	var from, to string
	var sources, tags []string
	var out output
	cmd := &cobra.Command{
		Use:   "[ID...]",
		Short: "List " + k.plural + ", newest first",
		Long: "List " + k.plural + ", newest first. Without IDs, list every item that matches the flags.\n" +
			"Several --tag flags must all match. -o json also prints the body and the notes of each item.",
		RunE: func(cmd *cobra.Command, ids []string) error {
			if err := out.check(); err != nil {
				return err
			}
			r, err := parseRange(from, to)
			if err != nil {
				return err
			}
			f := store.Filter{Range: r, Sources: sources, Tags: tags}
			if k.source != "" {
				f.Sources = []string{k.source}
			}
			return withStore(cmd, func(s *store.Store) error {
				var list []store.Evidence
				if len(ids) > 0 {
					if list, err = k.resolve(cmd, s, ids); err != nil {
						return err
					}
					list = filterLocal(list, f)
				} else if list, err = s.List(cmd.Context(), f); err != nil {
					return err
				}
				return writeEvidenceList(cmd, s, list, k, out)
			})
		},
	}
	addRangeFlags(cmd, &from, &to, false)
	if k.source == "" {
		cmd.Flags().StringSliceVar(&sources, "source", nil, "only these sources, for example slack,email")
	}
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "only items with all of these tags")
	addOutputFlags(cmd, &out, true, "json", "wide", "name")
	return cmd
}

// filterLocal applies the range, source and tag parts of f to items that
// were selected by ID.
func filterLocal(list []store.Evidence, f store.Filter) []store.Evidence {
	var out []store.Evidence
	for _, e := range list {
		if !f.Range.Contains(e.OccurredAt) {
			continue
		}
		if len(f.Sources) > 0 && !containsFold(f.Sources, e.Source) {
			continue
		}
		ok := true
		for _, t := range f.Tags {
			ok = ok && containsFold(e.Tags, t)
		}
		if ok {
			out = append(out, e)
		}
	}
	return out
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), s) {
			return true
		}
	}
	return false
}

func writeEvidenceList(cmd *cobra.Command, s *store.Store, list []store.Evidence, k evidenceKind, out output) error {
	w := cmd.OutOrStdout()
	switch out.format {
	case "json":
		notes, err := s.ListNotes(cmd.Context(), store.NoteFilter{})
		if err != nil {
			return err
		}
		byEvidence := map[string][]store.Note{}
		for _, n := range notes {
			byEvidence[n.EvidenceID] = append(byEvidence[n.EvidenceID], n)
		}
		for i := range list {
			list[i].Notes = byEvidence[list[i].ID]
		}
		return writeJSON(w, list)
	case "name":
		for _, e := range list {
			fmt.Fprintln(w, e.ID)
		}
		return nil
	}
	wide := out.format == "wide"
	titleWidth := 60
	if wide {
		titleWidth = 0
	}
	if k.source == store.PRSource {
		header := []string{"ID", "MERGED", "PR", "TITLE", "TAGS", "NOTES"}
		if wide {
			header = append(header, "AUTHOR", "URL")
		}
		t := newTable(w, out, header...)
		for _, e := range list {
			var pr store.PRInfo
			if e.PR != nil {
				pr = *e.PR
			}
			cells := []string{e.ID, day(e.OccurredAt), fmt.Sprintf("%s#%d", pr.Repo, pr.Number),
				oneLine(e.Title, titleWidth), orNone(e.Tags), fmt.Sprint(e.NoteCount)}
			if wide {
				cells = append(cells, pr.Author, e.URL)
			}
			t.row(cells...)
		}
		return t.flush()
	}
	header := []string{"ID", "DATE", "SOURCE", "TITLE", "TAGS", "NOTES"}
	if wide {
		header = append(header, "URL")
	}
	t := newTable(w, out, header...)
	for _, e := range list {
		cells := []string{e.ID, day(e.OccurredAt), e.Source, oneLine(e.Title, titleWidth), orNone(e.Tags), fmt.Sprint(e.NoteCount)}
		if wide {
			cells = append(cells, e.URL)
		}
		t.row(cells...)
	}
	return t.flush()
}

func describeEvidenceCmd(k evidenceKind) *cobra.Command {
	return &cobra.Command{
		Use:   "ID...",
		Short: "Show every field, tag and note of " + k.plural,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, ids []string) error {
			return withStore(cmd, func(s *store.Store) error {
				list, err := k.resolve(cmd, s, ids)
				if err != nil {
					return err
				}
				for i, e := range list {
					if e, err = s.Get(cmd.Context(), e.ID); err != nil {
						return err
					}
					if i > 0 {
						fmt.Fprintln(cmd.OutOrStdout())
					}
					describeEvidence(cmd.OutOrStdout(), e)
				}
				return nil
			})
		},
	}
}

func describeEvidence(w io.Writer, e store.Evidence) {
	field := func(name, value string) {
		if value != "" {
			fmt.Fprintf(w, "%-13s%s\n", name+":", value)
		}
	}
	field("ID", e.ID)
	field("Source", e.Source)
	field("External ID", e.ExternalID)
	field("Date", e.OccurredAt)
	field("Title", e.Title)
	field("URL", e.URL)
	if e.PR != nil {
		field("Repo", e.PR.Repo)
		field("Number", fmt.Sprint(e.PR.Number))
		field("Author", e.PR.Author)
	}
	field("Tags", orNone(e.Tags))
	field("Created", e.CreatedAt)
	field("Updated", e.UpdatedAt)
	if e.Body != "" {
		fmt.Fprintln(w, "Body:")
		fmt.Fprintln(w, indent(e.Body, "  "))
	}
	if len(e.Notes) == 0 {
		fmt.Fprintln(w, "Notes:       <none>")
		return
	}
	fmt.Fprintln(w, "Notes:")
	for _, n := range e.Notes {
		fmt.Fprintf(w, "  %d  %s  tags: %s\n", n.ID, day(n.CreatedAt), orNone(n.Tags))
		fmt.Fprintln(w, indent(n.Body, "    "))
	}
}

func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n"+prefix)
}

func createEvidenceCmd() *cobra.Command {
	var in store.NewEvidence
	var file, bodyFile string
	var out output
	cmd := &cobra.Command{
		Use:   "[TITLE...]",
		Short: "Add evidence from any source, or import a JSON file",
		Long: `Add one piece of evidence, or import many from a JSON file with -f.

The source says where the evidence came from, for example slack, email or a
company tool. You choose the names. The date defaults to now.

If you give --external-id, the ID of the item in its source, a second create
with the same source and external ID updates the item and adds no duplicate.
Notes and tags stay. Without --external-id, every create adds a new item.

With -f, the file holds JSON objects with these fields: source, title,
external_id, body, url, occurred_at and tags. Only source and title are
required. The file can be one object, an array, or one object per line.
Use "-f -" to read standard input. --source sets the source of objects that
have none, and --tag adds tags to every object.`,
		Example: `  eoy create evidence --source slack --date 2026-03-04 --url https://... Thanked by the infra team
  eoy create evidence -f recognitions.json --tag imported`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := out.check(); err != nil {
				return err
			}
			var items []store.NewEvidence
			if file != "" {
				if len(args) > 0 || in.ExternalID != "" || in.Body != "" || bodyFile != "" || in.URL != "" || in.OccurredAt != "" {
					return usageErrorf("-f takes no title and no --external-id, --body, --body-file, --url or --date")
				}
				var err error
				if items, err = readEvidenceFile(cmd, file); err != nil {
					return err
				}
				for i := range items {
					if items[i].Source == "" {
						items[i].Source = in.Source
					}
					items[i].Tags = append(items[i].Tags, in.Tags...)
				}
			} else {
				in.Title = strings.Join(args, " ")
				if in.Title == "" {
					return usageErrorf("give a title, or -f FILE")
				}
				if in.Source == "" {
					return usageErrorf("give --source, for example --source slack")
				}
				if bodyFile != "" {
					if in.Body != "" {
						return usageErrorf("give --body or --body-file, not both")
					}
					b, err := readFileOrStdin(cmd, bodyFile)
					if err != nil {
						return err
					}
					in.Body = string(b)
				}
				items = []store.NewEvidence{in}
			}
			return withStore(cmd, func(s *store.Store) error {
				counts, ids, err := s.UpsertEvidence(cmd.Context(), items, now())
				if err != nil {
					return err
				}
				w := cmd.OutOrStdout()
				switch {
				case out.format == "name":
					for _, id := range ids {
						fmt.Fprintln(w, id)
					}
				case out.format == "json":
					return writeJSON(w, struct {
						store.Counts
						IDs []string `json:"ids"`
					}{counts, ids})
				case file != "":
					fmt.Fprintf(w, "evidence: %s\n", formatCounts(counts))
				default:
					verb := "created"
					if counts.Updated > 0 {
						verb = "updated"
					} else if counts.Unchanged > 0 {
						verb = "unchanged"
					}
					fmt.Fprintf(w, "evidence/%s %s\n", ids[0], verb)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&file, "filename", "f", "", "JSON file to import, or - for standard input")
	cmd.Flags().StringVar(&in.Source, "source", "", "where the evidence came from, for example slack or email")
	cmd.Flags().StringVar(&in.ExternalID, "external-id", "", "ID of the item in its source, so a second create updates it")
	cmd.Flags().StringVar(&in.Body, "body", "", "longer text, for example the message")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "read the body from this file, or - for standard input")
	cmd.Flags().StringVar(&in.URL, "url", "", "link to the evidence")
	cmd.Flags().StringVar(&in.OccurredAt, "date", "", "when it happened: YYYY-MM-DD, YYYY-MM-DDTHH:MM:SS, or RFC 3339 (default: now)")
	cmd.Flags().StringSliceVar(&in.Tags, "tag", nil, "tags to add, can repeat")
	addOutputFlags(cmd, &out, false, "json", "name")
	return cmd
}

func readFileOrStdin(cmd *cobra.Command, path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(cmd.InOrStdin())
	}
	return os.ReadFile(path)
}

// readEvidenceFile reads one JSON object, an array of objects, or a stream
// of objects such as JSON Lines.
func readEvidenceFile(cmd *cobra.Command, path string) ([]store.NewEvidence, error) {
	b, err := readFileOrStdin(cmd, path)
	if err != nil {
		return nil, err
	}
	name := path
	if path == "-" {
		name = "standard input"
	}
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("%s: no evidence", name)
	}
	dec := json.NewDecoder(bufio.NewReader(bytes.NewReader(trimmed)))
	dec.DisallowUnknownFields()
	var items []store.NewEvidence
	if trimmed[0] == '[' {
		if err := dec.Decode(&items); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return items, nil
	}
	for {
		var it store.NewEvidence
		err := dec.Decode(&it)
		if errors.Is(err, io.EOF) {
			return items, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: object %d: %w", name, len(items)+1, err)
		}
		items = append(items, it)
	}
}

func deleteEvidenceCmd(k evidenceKind) *cobra.Command {
	var all, force bool
	cmd := &cobra.Command{
		Use:   "[ID...]",
		Short: "Delete " + k.plural + ". Without --force, only list them",
		Long: "Delete " + k.plural + " by ID, or every item with --all.\n\n" +
			"Without --force, the command lists the items that it would delete and changes\n" +
			"nothing. With --force, it deletes them with their notes and tags. A later\n" +
			"import or sync can add an item back, but without its notes and tags.",
		RunE: func(cmd *cobra.Command, ids []string) error {
			if len(ids) == 0 && !all {
				return usageErrorf("give one or more IDs, or --all")
			}
			if len(ids) > 0 && all {
				return usageErrorf("give IDs or --all, not both")
			}
			return withStore(cmd, func(s *store.Store) error {
				var targets []store.Evidence
				var err error
				if all {
					f := store.Filter{}
					if k.source != "" {
						f.Sources = []string{k.source}
					}
					targets, err = s.List(cmd.Context(), f)
				} else {
					targets, err = k.resolve(cmd, s, ids)
				}
				if err != nil {
					return err
				}
				w := cmd.OutOrStdout()
				if len(targets) == 0 {
					fmt.Fprintln(w, "nothing to delete")
					return nil
				}
				if !force {
					t := newTable(w, output{}, "ID", "DATE", "SOURCE", "TITLE", "NOTES")
					for _, e := range targets {
						t.row(e.ID, day(e.OccurredAt), e.Source, oneLine(e.Title, 60), fmt.Sprint(e.NoteCount))
					}
					if err := t.flush(); err != nil {
						return err
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "not deleted: run again with --force to delete %s with their notes and tags\n", plural(len(targets), "item"))
					return nil
				}
				del := make([]string, len(targets))
				for i, e := range targets {
					del[i] = e.ID
				}
				n, err := s.DeleteEvidence(cmd.Context(), del)
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "deleted %s\n", plural(n, "item"))
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "delete every item")
	cmd.Flags().BoolVar(&force, "force", false, "delete for real")
	return cmd
}

func tagEvidenceCmd(k evidenceKind) *cobra.Command {
	return &cobra.Command{
		Use:   "ID TAG...",
		Short: "Add or remove tags on " + k.plural,
		Long: "Add tags to one item. A tag that ends with '-' is removed, as in kubectl label.\n" +
			"Tags are lowercase.",
		Example: "  eoy tag " + k.plural + " ID migration infra\n  eoy tag " + k.plural + " ID infra-",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			add, remove := parseTagArgs(args[1:])
			return withStore(cmd, func(s *store.Store) error {
				if _, err := k.resolve(cmd, s, args[:1]); err != nil {
					return err
				}
				if err := s.TagEvidence(cmd.Context(), args[0], add, remove); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s/%s tagged\n", k.plural, args[0])
				return nil
			})
		},
	}
}
