package main

// What the dashboard's Traffic and Tests pages read from events.db, and the
// arithmetic they show: counts of visitor-days (one visitor ID on one UTC
// day), Wilson intervals, and the chance one arm beats another.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"time"
)

const (
	dayLayout = "2006-01-02"
	// An arm with fewer visitors than this is shown greyed.
	fewVisitors = 100
	// Seconds of engaged time that make a visitor-day engaged.
	engagedSeconds = 10
	engagedScroll  = 50
	drawCount      = 10000
	wilsonZ        = 1.959963984540054 // the 97.5th percentile of the normal
)

// ------------------------------------------------------------------ math --

// The 95% Wilson score interval for k successes in n trials.
func wilson(k, n int) (low, high float64) {
	if n <= 0 {
		return 0, 0
	}
	p, nf, z2 := float64(k)/float64(n), float64(n), wilsonZ*wilsonZ
	centre := p + z2/(2*nf)
	margin := wilsonZ * math.Sqrt(p*(1-p)/nf+z2/(4*nf*nf))
	denom := 1 + z2/nf
	return math.Max(0, (centre-margin)/denom), math.Min(1, (centre+margin)/denom)
}

// A Gamma(shape, 1) draw by Marsaglia and Tsang; shape must be at least 1.
func gammaDraw(r *rand.Rand, shape float64) float64 {
	d := shape - 1.0/3
	c := 1 / math.Sqrt(9*d)
	for {
		x := r.NormFloat64()
		v := 1 + c*x
		if v <= 0 {
			continue
		}
		v = v * v * v
		u := r.Float64()
		if u < 1-0.0331*x*x*x*x || math.Log(u) < 0.5*x*x+d*(1-v+math.Log(v)) {
			return d * v
		}
	}
}

func betaDraw(r *rand.Rand, a, b float64) float64 {
	x, y := gammaDraw(r, a), gammaDraw(r, b)
	return x / (x + y)
}

// The chance an arm beats the baseline: each rate is Beta(1+successes,
// 1+failures), compared over drawCount draws. The seed comes from the label
// and the counts, so the same data shows the same figure on every load.
func chanceToBeat(label string, baseK, baseN, armK, armN int) float64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%s|%d|%d|%d|%d", label, baseK, baseN, armK, armN)
	r := rand.New(rand.NewPCG(h.Sum64(), 0x9e3779b97f4a7c15))
	wins := 0
	for i := 0; i < drawCount; i++ {
		base := betaDraw(r, float64(1+baseK), float64(1+baseN-baseK))
		arm := betaDraw(r, float64(1+armK), float64(1+armN-armK))
		if arm > base {
			wins++
		}
	}
	return float64(wins) / drawCount
}

// ---------------------------------------------------------------- traffic --

type dayCount struct {
	Day             string
	Visitors, Views int
}

type countRow struct {
	Key             string
	Visitors, Views int
}

// The traffic dimensions, each a column list the rows are grouped by.
var trafficDimensions = []struct {
	Name    string
	Columns string
	Where   string
}{
	{"pages", "path", ""},
	{"referrers", "referrer", ""},
	{"campaigns", "utm_source, utm_medium, utm_campaign", "AND (utm_source != '' OR utm_medium != '' OR utm_campaign != '')"},
	{"countries", "country", ""},
	{"devices", "device", ""},
}

// The days from..to, both included, as "2006-01-02".
func dayRange(from, to time.Time) []string {
	var days []string
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		days = append(days, d.Format(dayLayout))
	}
	return days
}

// Visitors and views for each day of from..to; a day with no events is zero.
func (s *eventStore) daily(ctx context.Context, from, to time.Time) ([]dayCount, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT day, COUNT(DISTINCT visitor), COUNT(*) FROM events
		WHERE kind = 'view' AND day BETWEEN ? AND ? GROUP BY day`, from.Format(dayLayout), to.Format(dayLayout))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	have := map[string]dayCount{}
	for rows.Next() {
		var d dayCount
		if err := rows.Scan(&d.Day, &d.Visitors, &d.Views); err != nil {
			return nil, err
		}
		have[d.Day] = d
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []dayCount
	for _, day := range dayRange(from, to) {
		d := have[day]
		d.Day = day
		out = append(out, d)
	}
	return out, nil
}

// The rows of one dimension, most visitors first. Unless all, at most limit+1
// rows come back, so the caller can tell whether there are more.
func (s *eventStore) breakdown(ctx context.Context, dim int, from, to time.Time, all bool, limit int) ([]countRow, error) {
	d := trafficDimensions[dim]
	q := fmt.Sprintf(`SELECT %s, COUNT(DISTINCT day || visitor) AS v, COUNT(*) AS n FROM events
		WHERE kind = 'view' AND day BETWEEN ? AND ? %s GROUP BY %s ORDER BY v DESC, n DESC, %s`,
		d.Columns, d.Where, d.Columns, d.Columns)
	args := []any{from.Format(dayLayout), to.Format(dayLayout)}
	if !all {
		q += " LIMIT ?"
		args = append(args, limit+1)
	}
	rows, err := s.read.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []countRow
	for rows.Next() {
		var parts [3]string
		var r countRow
		dst := []any{&parts[0]}
		if dim == 2 {
			dst = []any{&parts[0], &parts[1], &parts[2]}
		}
		if err := rows.Scan(append(dst, &r.Visitors, &r.Views)...); err != nil {
			return nil, err
		}
		r.Key = parts[0]
		if dim == 2 {
			r.Key = dash(parts[0]) + " / " + dash(parts[1]) + " / " + dash(parts[2])
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// ------------------------------------------------------------------ tests --

// What was counted for one arm of one test, over all the days there are.
type armCounts struct {
	Visitors, Engaged, Reached75, FormStarts, Submissions int
	Clicks                                                map[string]int
}

type testCounts struct {
	First, Last string
	Arms        map[string]*armCounts
}

// Every test found in the events, with each arm's counts. Every count is of
// visitor-days that have a view carrying the test and arm; events with no
// test or arm are not counted.
func (s *eventStore) testCounts(ctx context.Context) (map[string]*testCounts, error) {
	pairs, err := s.read.QueryContext(ctx, `SELECT DISTINCT test, arm FROM events WHERE test != '' AND arm != ''`)
	if err != nil {
		return nil, err
	}
	var all [][2]string
	for pairs.Next() {
		var p [2]string
		if err := pairs.Scan(&p[0], &p[1]); err != nil {
			pairs.Close()
			return nil, err
		}
		all = append(all, p)
	}
	pairs.Close()
	if err := pairs.Err(); err != nil {
		return nil, err
	}

	// One arm at a time: the index holds an arm's events in visitor-day order,
	// so each pass reads it once with nothing to sort.
	out := map[string]*testCounts{}
	for _, p := range all {
		test, arm := p[0], p[1]
		c := &armCounts{Clicks: map[string]int{}}
		var first, last string
		err := s.read.QueryRowContext(ctx, `SELECT MIN(day), MAX(day), SUM(v),
				SUM(v AND (secs >= ? OR s50)), SUM(v AND s75), SUM(v AND fs), SUM(v AND sub)
			FROM (SELECT day,
					MAX(kind = 'view') AS v,
					SUM(CASE WHEN kind = 'engaged' THEN value ELSE 0 END) AS secs,
					MAX(kind = 'scroll' AND value >= ?) AS s50,
					MAX(kind = 'scroll' AND value >= 75) AS s75,
					MAX(kind = 'form_start') AS fs,
					MAX(kind = 'submit') AS sub
				FROM events WHERE test != '' AND test = ? AND arm = ? GROUP BY day, visitor)`,
			engagedSeconds, engagedScroll, test, arm).
			Scan(&first, &last, &c.Visitors, &c.Engaged, &c.Reached75, &c.FormStarts, &c.Submissions)
		if err != nil {
			return nil, err
		}
		t := out[test]
		if t == nil {
			t = &testCounts{First: first, Last: last, Arms: map[string]*armCounts{}}
			out[test] = t
		}
		t.First, t.Last = min(t.First, first), max(t.Last, last)
		t.Arms[arm] = c
	}

	crows, err := s.read.QueryContext(ctx, `SELECT c.test, c.arm, c.target, COUNT(DISTINCT c.day || c.visitor)
		FROM events c WHERE c.kind = 'click' AND c.test != '' AND c.arm != ''
			AND EXISTS (SELECT 1 FROM events v WHERE v.test != '' AND v.test = c.test AND v.arm = c.arm AND v.day = c.day
				AND v.visitor = c.visitor AND v.kind = 'view')
		GROUP BY c.test, c.arm, c.target`)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var test, arm, target string
		var n int
		if err := crows.Scan(&test, &arm, &target, &n); err != nil {
			return nil, err
		}
		if t := out[test]; t != nil && t.Arms[arm] != nil {
			t.Arms[arm].Clicks[target] = n
		}
	}
	return out, crows.Err()
}

// The path an ended test's views carry; each carries the same.
func (s *eventStore) testRoute(ctx context.Context, test string) (string, error) {
	var path string
	err := s.read.QueryRowContext(ctx, `SELECT path FROM events WHERE test != '' AND test = ? AND kind = 'view'
		ORDER BY arm, day LIMIT 1`, test).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return path, err
}
