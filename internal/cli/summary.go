package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jordantdavis/eoy/internal/store"
)

// barWidth is the number of cells that the largest value in a table fills.
const barWidth = 30

// summarySections lists the valid --by values in output order.
var summarySections = []string{"month", "source", "repo", "tag"}

// section is one capped list in the JSON output. More counts the rows that
// --top left out.
type section struct {
	Rows []store.Count `json:"rows"`
	More int           `json:"more"`
}

type summaryOut struct {
	From    string           `json:"from"`
	To      string           `json:"to"`
	Months  []store.MonthRow `json:"months,omitempty"`
	Sources *section         `json:"sources,omitempty"`
	Repos   *section         `json:"repos,omitempty"`
	Tags    *section         `json:"tags,omitempty"`
}

func summaryResource() Resource {
	return Resource{
		Name: "summary", Plural: "summary", ShortNames: []string{"sum"},
		Commands: map[string]func() *cobra.Command{"get": getSummaryCmd},
	}
}

func getSummaryCmd() *cobra.Command {
	var from, to string
	var sources, tags, by []string
	var top int
	var out output
	cmd := &cobra.Command{
		Use:   "",
		Short: "Count evidence by month, source, repo and tag, with bar charts",
		Long: `Count the stored evidence by month, by source, by PR repo, and by tag, and
draw a bar for each count. The range defaults to January 1 of this year
through today. --source and --tag filter like "eoy get evidence".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := out.check(); err != nil {
				return err
			}
			r, err := yearRange(from, to)
			if err != nil {
				return err
			}
			if top < 0 {
				return usageErrorf("--top %d: want 0 or more", top)
			}
			show := map[string]bool{}
			for _, name := range by {
				name = strings.ToLower(strings.TrimSpace(name))
				if !slices.Contains(summarySections, name) {
					return usageErrorf("--by %q: want one or more of %s", name, strings.Join(summarySections, ", "))
				}
				show[name] = true
			}
			return withStore(cmd, func(s *store.Store) error {
				sum, err := s.Summary(cmd.Context(), store.Filter{Range: r, Sources: sources, Tags: tags})
				if err != nil {
					return err
				}
				res := summaryOut{From: sum.From, To: sum.To}
				if show["month"] {
					res.Months = sum.Months
				}
				if show["source"] {
					res.Sources = capRows(sum.Sources, top)
				}
				if show["repo"] {
					res.Repos = capRows(sum.Repos, top)
				}
				if show["tag"] {
					res.Tags = capRows(sum.Tags, top)
				}
				if out.format == "json" {
					return writeJSON(cmd.OutOrStdout(), res)
				}
				return writeSummary(cmd.OutOrStdout(), res, sum.Sources)
			})
		},
	}
	addRangeFlags(cmd, &from, &to, true)
	cmd.Flags().StringSliceVar(&sources, "source", nil, "only these sources")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "only items with all of these tags")
	cmd.Flags().StringSliceVar(&by, "by", summarySections, "sections to show: "+strings.Join(summarySections, ", "))
	cmd.Flags().IntVar(&top, "top", 10, "most rows in the source, repo and tag tables. 0 shows all")
	addOutputFlags(cmd, &out, false, "json")
	return cmd
}

func capRows(rows []store.Count, top int) *section {
	if top == 0 || len(rows) <= top {
		return &section{Rows: rows}
	}
	return &section{Rows: rows[:top], More: len(rows) - top}
}

func writeSummary(w io.Writer, out summaryOut, sources []store.Count) error {
	var blocks []func() error
	if out.Months != nil {
		blocks = append(blocks, func() error { return writeMonths(w, out.Months, sources, out.From+" to "+out.To) })
	}
	if out.Sources != nil {
		blocks = append(blocks, func() error { return writeCounts(w, "By source", "SOURCE", "ITEMS", *out.Sources) })
	}
	if out.Repos != nil {
		blocks = append(blocks, func() error { return writeCounts(w, "PRs by repo", "REPO", "PRS", *out.Repos) })
	}
	if out.Tags != nil {
		blocks = append(blocks, func() error { return writeCounts(w, "By tag", "TAG", "ITEMS", *out.Tags) })
	}
	for i, block := range blocks {
		if i > 0 {
			fmt.Fprintln(w)
		}
		if err := block(); err != nil {
			return err
		}
	}
	return nil
}

// writeMonths writes one row per month. With more than one source, it
// writes one row per source in each month, and only the first row shows the
// month. All bars use the same scale.
func writeMonths(w io.Writer, months []store.MonthRow, sources []store.Count, period string) error {
	split := len(sources) > 1
	most := 0
	for _, m := range months {
		if !split {
			most = max(most, m.Total)
			continue
		}
		for _, c := range m.Sources {
			most = max(most, c.Count)
		}
	}
	fmt.Fprintf(w, "By month  %s\n", period)
	tw := summaryTable(w)
	num := numColumn("ITEMS", most)
	if !split {
		fmt.Fprintf(tw, "MONTH\t%s\n", num.header)
		for _, m := range months {
			fmt.Fprintf(tw, "%s\t%s\n", m.Month, num.withBar(m.Total, most))
		}
		return tw.Flush()
	}
	fmt.Fprintf(tw, "MONTH\tSOURCE\t%s\n", num.header)
	for _, m := range months {
		for i, c := range m.Sources {
			label := m.Month
			if i > 0 {
				label = ""
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", label, c.Key, num.withBar(c.Count, most))
		}
	}
	return tw.Flush()
}

func writeCounts(w io.Writer, title, keyHeader, countHeader string, rows section) error {
	fmt.Fprintln(w, title)
	if len(rows.Rows) == 0 {
		fmt.Fprintln(w, "none")
		return nil
	}
	most := rows.Rows[0].Count // rows are sorted highest first
	tw := summaryTable(w)
	num := numColumn(countHeader, most)
	fmt.Fprintf(tw, "%s\t%s\n", keyHeader, num.header)
	for _, c := range rows.Rows {
		fmt.Fprintf(tw, "%s\t%s\n", oneLine(c.Key, 40), num.withBar(c.Count, most))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if rows.More > 0 {
		fmt.Fprintf(w, "… and %d more\n", rows.More)
	}
	return nil
}

// bar returns n cells, or a scaled count when most is larger than width. A
// value above zero always gets at least one cell.
func bar(n, most, width int) string {
	if n <= 0 || most <= 0 {
		return ""
	}
	cells := n
	if most > width {
		cells = (n*width + most/2) / most
	}
	return strings.Repeat("█", max(cells, 1))
}

func summaryTable(w io.Writer) *tabwriter.Writer { return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0) }

// numCol right-aligns counts under a header. It is the last table column.
type numCol struct {
	header string
	width  int
}

func numColumn(header string, most int) numCol {
	return numCol{header: header, width: max(len(header), len(fmt.Sprint(most)))}
}

// withBar returns the padded count and its bar. The bar is outside the
// table columns, so a row with no bar has no trailing spaces.
func (c numCol) withBar(n, most int) string {
	cell := fmt.Sprintf("%*d", c.width, n)
	if b := bar(n, most, barWidth); b != "" {
		return cell + "  " + b
	}
	return cell
}
