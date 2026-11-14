package daterange

import "testing"

func TestParse(t *testing.T) {
	if _, err := Parse("2026-01-01", "2026-12-31"); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse("", ""); err != nil {
		t.Fatalf("open range must be valid: %v", err)
	}
	for _, bad := range [][2]string{{"2026-1-1", ""}, {"", "tomorrow"}, {"2026-02-01", "2026-01-01"}} {
		if _, err := Parse(bad[0], bad[1]); err == nil {
			t.Errorf("Parse(%q, %q) must fail", bad[0], bad[1])
		}
	}
}

func TestContains(t *testing.T) {
	r, _ := Parse("2026-01-01", "2026-06-30")
	cases := map[string]bool{
		"2025-12-31T23:59:59":  false,
		"2026-01-01T00:00:00":  true,
		"2026-06-30T23:59:59":  true,
		"2026-06-30T10:00:00Z": true,
		"2026-07-01T00:00:00":  false,
	}
	for ts, want := range cases {
		if got := r.Contains(ts); got != want {
			t.Errorf("Contains(%q) = %v, want %v", ts, got, want)
		}
	}
	open, _ := Parse("", "")
	if !open.Contains("1999-01-01T00:00:00") {
		t.Error("open range must contain everything")
	}
}

func TestNormalizeTime(t *testing.T) {
	cases := map[string]string{
		"2026-03-04":                "2026-03-04",
		" 2026-03-04T09:30:00 ":     "2026-03-04T09:30:00",
		"2026-03-04T09:30:00-05:00": "2026-03-04T14:30:00Z",
		"2026-03-04T09:30:00Z":      "2026-03-04T09:30:00Z",
	}
	for in, want := range cases {
		if got, err := NormalizeTime(in); err != nil || got != want {
			t.Errorf("NormalizeTime(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "yesterday", "2026-3-4", "2026-03-04 09:30"} {
		if _, err := NormalizeTime(bad); err == nil {
			t.Errorf("NormalizeTime(%q) must fail", bad)
		}
	}
}
