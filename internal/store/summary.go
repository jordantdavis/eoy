package store

import (
	"context"
	"sort"
	"time"
)

// Summary holds evidence counts for one filter, grouped four ways.
type Summary struct {
	From    string     `json:"from,omitempty"`
	To      string     `json:"to,omitempty"`
	Months  []MonthRow `json:"months"`
	Sources []Count    `json:"sources"`
	Repos   []Count    `json:"repos"`
	Tags    []Count    `json:"tags"`
}

// MonthRow counts the evidence of one calendar month, in YYYY-MM form, per
// source. Sources has one entry for each source in the summary, also when
// the count is zero, in the order of Summary.Sources.
type MonthRow struct {
	Month   string  `json:"month"`
	Total   int     `json:"total"`
	Sources []Count `json:"sources"`
}

// Count is the number of items for one key, for example a source or a tag.
type Count struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

const monthLayout = "2006-01"

// Summary counts the evidence that matches f. Months run from the month of
// f.Range.From to the month of f.Range.To, with zero rows for empty months.
// An open end uses the first or last month that has an item. Lists are
// sorted by count, highest first, then by key.
func (s *Store) Summary(ctx context.Context, f Filter) (Summary, error) {
	sum := Summary{From: f.Range.From, To: f.Range.To}
	where, args, err := filterClause(f)
	if err != nil {
		return sum, err
	}

	sum.Sources, err = s.counts(ctx, `
		SELECT e.source, count(*) FROM evidence e WHERE 1 = 1`+where+`
		 GROUP BY e.source ORDER BY count(*) DESC, e.source`, args)
	if err != nil {
		return sum, err
	}
	sum.Repos, err = s.counts(ctx, `
		SELECT p.repo, count(*) FROM evidence e JOIN pull_requests p ON p.evidence_id = e.id
		 WHERE 1 = 1`+where+`
		 GROUP BY p.repo ORDER BY count(*) DESC, p.repo`, args)
	if err != nil {
		return sum, err
	}
	sum.Tags, err = s.counts(ctx, `
		SELECT t.tag, count(*) FROM evidence e JOIN evidence_tags t ON t.evidence_id = e.id
		 WHERE 1 = 1`+where+`
		 GROUP BY t.tag ORDER BY count(*) DESC, t.tag`, args)
	if err != nil {
		return sum, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT substr(e.occurred_at, 1, 7), e.source, count(*) FROM evidence e
		 WHERE 1 = 1`+where+`
		 GROUP BY 1, 2`, args...)
	if err != nil {
		return sum, err
	}
	defer rows.Close()
	byMonth := map[string]map[string]int{}
	for rows.Next() {
		var month, source string
		var n int
		if err := rows.Scan(&month, &source, &n); err != nil {
			return sum, err
		}
		if byMonth[month] == nil {
			byMonth[month] = map[string]int{}
		}
		byMonth[month][source] = n
	}
	if err := rows.Err(); err != nil {
		return sum, err
	}
	sum.Months = fillMonths(byMonth, sum.Sources, f.Range.From, f.Range.To)
	return sum, nil
}

// fillMonths returns one row per month from the month of from to the month
// of to. An empty end uses the first or last month in counts.
func fillMonths(counts map[string]map[string]int, sources []Count, from, to string) []MonthRow {
	var months []string
	for m := range counts {
		months = append(months, m)
	}
	sort.Strings(months)
	var first, last string
	if len(months) > 0 {
		first, last = months[0], months[len(months)-1]
	}
	if from != "" {
		first = from[:len(monthLayout)]
	}
	if to != "" {
		last = to[:len(monthLayout)]
	}
	out := []MonthRow{}
	start, err1 := time.Parse(monthLayout, first)
	end, err2 := time.Parse(monthLayout, last)
	if err1 != nil || err2 != nil {
		return out
	}
	for m := start; !m.After(end); m = m.AddDate(0, 1, 0) {
		key := m.Format(monthLayout)
		row := MonthRow{Month: key, Sources: []Count{}}
		for _, src := range sources {
			n := counts[key][src.Key]
			row.Total += n
			row.Sources = append(row.Sources, Count{Key: src.Key, Count: n})
		}
		out = append(out, row)
	}
	return out
}

func (s *Store) counts(ctx context.Context, q string, args []any) ([]Count, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Count{}
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Key, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
