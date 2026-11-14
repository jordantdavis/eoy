// Package daterange holds an inclusive range of calendar days, used by every
// eoy command that takes --from and --to.
package daterange

import (
	"fmt"
	"strings"
	"time"
)

// Layout is the day format for --from and --to.
const Layout = "2006-01-02"

// Range is an inclusive range of days in YYYY-MM-DD form. An empty From or To
// leaves that end open.
type Range struct {
	From string
	To   string
}

// Parse validates both ends and makes sure that from is not after to.
func Parse(from, to string) (Range, error) {
	for _, d := range []struct{ flag, value string }{{"--from", from}, {"--to", to}} {
		if d.value == "" {
			continue
		}
		if _, err := time.Parse(Layout, d.value); err != nil {
			return Range{}, fmt.Errorf("%s %q: want a date in YYYY-MM-DD form", d.flag, d.value)
		}
	}
	if from != "" && to != "" && from > to {
		return Range{}, fmt.Errorf("--from %s is after --to %s", from, to)
	}
	return Range{From: from, To: to}, nil
}

// Contains reports whether a timestamp that starts with YYYY-MM-DD falls in
// the range. It compares only the day part.
func (r Range) Contains(timestamp string) bool {
	if len(timestamp) < len(Layout) {
		return false
	}
	day := timestamp[:len(Layout)]
	if r.From != "" && day < r.From {
		return false
	}
	if r.To != "" && day > r.To {
		return false
	}
	return true
}

// NormalizeTime accepts a day (YYYY-MM-DD), a local time without a zone
// (YYYY-MM-DDTHH:MM:SS), or an RFC 3339 time. It returns a day or a local
// time unchanged, and an RFC 3339 time in UTC. Each form starts with the day,
// so Contains and text order work on all of them.
func NormalizeTime(s string) (string, error) {
	s = strings.TrimSpace(s)
	if _, err := time.Parse(Layout, s); err == nil {
		return s, nil
	}
	if _, err := time.Parse(localLayout, s); err == nil {
		return s, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	return "", fmt.Errorf("time %q: want YYYY-MM-DD, YYYY-MM-DDTHH:MM:SS, or RFC 3339", s)
}

const localLayout = "2006-01-02T15:04:05"
