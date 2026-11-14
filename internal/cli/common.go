package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jordantdavis/eoy/internal/config"
	"github.com/jordantdavis/eoy/internal/daterange"
	"github.com/jordantdavis/eoy/internal/gh"
	"github.com/jordantdavis/eoy/internal/store"
)

// now is the clock. Tests replace it.
var now = time.Now

// newGitHub returns a GitHub client. Tests replace it to use a fake server.
var newGitHub = func(ctx context.Context, logf func(string, ...any)) (*gh.Client, error) {
	return gh.New(ctx, gh.WithLog(logf))
}

// addDataFlags adds the --db and --config flags that every command inherits.
func addDataFlags(root *cobra.Command) {
	root.PersistentFlags().String("db", config.DefaultDBPath(), "database file (env EOY_DB)")
	root.PersistentFlags().String("config", config.DefaultPath(), "configuration file (env EOY_CONFIG)")
}

func flagValue(cmd *cobra.Command, name string) string {
	if f := cmd.Flags().Lookup(name); f != nil {
		return f.Value.String()
	}
	return ""
}

// withStore opens the database, runs fn, and closes the database.
func withStore(cmd *cobra.Command, fn func(*store.Store) error) error {
	s, err := store.Open(flagValue(cmd, "db"))
	if err != nil {
		return err
	}
	defer s.Close()
	return fn(s)
}

func loadConfig(cmd *cobra.Command) (config.Config, string, error) {
	path := flagValue(cmd, "config")
	c, err := config.Load(path)
	return c, path, err
}

func logf(cmd *cobra.Command) func(string, ...any) {
	return func(format string, args ...any) { fmt.Fprintf(cmd.ErrOrStderr(), format, args...) }
}

// output holds the kubectl-style -o and --no-headers flags.
type output struct {
	format    string
	noHeaders bool
	formats   []string
}

// addOutputFlags adds -o with the given formats, and --no-headers when a
// table is the default output.
func addOutputFlags(cmd *cobra.Command, o *output, table bool, formats ...string) {
	o.formats = formats
	cmd.Flags().StringVarP(&o.format, "output", "o", "", "output format: "+strings.Join(formats, ", "))
	if table {
		cmd.Flags().BoolVar(&o.noHeaders, "no-headers", false, "do not print the header row")
	}
}

func (o output) check() error {
	if o.format != "" && !slices.Contains(o.formats, o.format) {
		return usageErrorf("unknown output format %q; valid formats: %s", o.format, strings.Join(o.formats, ", "))
	}
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// table writes tab-separated rows as aligned columns.
type table struct {
	tw     *tabwriter.Writer
	header bool
}

func newTable(w io.Writer, o output, header ...string) *table {
	t := &table{tw: tabwriter.NewWriter(w, 0, 4, 3, ' ', 0)}
	if !o.noHeaders {
		fmt.Fprintln(t.tw, strings.Join(header, "\t"))
	}
	return t
}

func (t *table) row(cells ...string) { fmt.Fprintln(t.tw, strings.Join(cells, "\t")) }

func (t *table) flush() error { return t.tw.Flush() }

// oneLine joins the lines of s and cuts it to max runes.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if max <= 0 || len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func day(ts string) string {
	if len(ts) >= len(daterange.Layout) {
		return ts[:len(daterange.Layout)]
	}
	return ts
}

func orNone(list []string) string {
	if len(list) == 0 {
		return "<none>"
	}
	return strings.Join(list, ",")
}

// addRangeFlags adds --from and --to.
func addRangeFlags(cmd *cobra.Command, from, to *string, defaultYear bool) {
	fromHelp, toHelp := "first day, YYYY-MM-DD", "last day, YYYY-MM-DD"
	if defaultYear {
		fromHelp += " (default: January 1 of this year)"
		toHelp += " (default: today)"
	}
	cmd.Flags().StringVar(from, "from", "", fromHelp)
	cmd.Flags().StringVar(to, "to", "", toHelp)
}

func parseRange(from, to string) (daterange.Range, error) {
	r, err := daterange.Parse(from, to)
	if err != nil {
		return r, usageError{err}
	}
	return r, nil
}

// yearRange fills open ends with January 1 of this year and today.
func yearRange(from, to string) (daterange.Range, error) {
	t := now()
	if from == "" {
		from = fmt.Sprintf("%d-01-01", t.Year())
	}
	if to == "" {
		to = t.Format(daterange.Layout)
	}
	return parseRange(from, to)
}

// parseTagArgs splits "eoy tag" arguments into tags to add and tags to
// remove. A trailing '-' removes a tag, as in "kubectl label".
func parseTagArgs(args []string) (add, remove []string) {
	for _, a := range args {
		if t, ok := strings.CutSuffix(a, "-"); ok {
			remove = append(remove, t)
		} else {
			add = append(add, a)
		}
	}
	return add, remove
}

func plural(n int, one string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %ss", n, one)
}

func formatCounts(c store.Counts) string {
	return fmt.Sprintf("%d added, %d updated, %d unchanged", c.Added, c.Updated, c.Unchanged)
}
