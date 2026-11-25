package main

// The dashboard's Traffic and Tests pages: what stats.go counts, laid out.

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	trafficTmpl = adminTemplate("traffic")
	testsTmpl   = adminTemplate("tests")
)

func (s *server) mountStats(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/traffic", s.admin(true, s.trafficPage))
	mux.HandleFunc("GET /admin/tests", s.admin(true, s.testsPage))
}

// ---------------------------------------------------------------- traffic --

const topRows = 10

var rangeChoices = []int{7, 30, 90}

type rangeLink struct {
	Days   int
	Href   string
	Active bool
}

type breakdownTable struct {
	Name, Title, Head string
	Rows              []countRow
	More              bool   // there are rows beyond the top
	All               bool   // every row is shown
	Href              string // the link that switches between top and all
}

func daysFrom(r *http.Request) int {
	if n, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil {
		for _, c := range rangeChoices {
			if n == c {
				return n
			}
		}
	}
	return 30
}

func (s *server) trafficPage(w http.ResponseWriter, r *http.Request, _ *adminCreds, _ string) {
	days := daysFrom(r)
	data := struct {
		adminFrame
		Stored          bool
		Ranges          []rangeLink
		From, To        string
		Visitors, Views int
		Chart           template.HTML
		Tables          []breakdownTable
	}{adminFrame: s.frame("Traffic", "Traffic", true), Stored: s.events != nil}
	for _, c := range rangeChoices {
		data.Ranges = append(data.Ranges, rangeLink{c, fmt.Sprintf("/admin/traffic?days=%d", c), c == days})
	}
	if s.events != nil {
		ctx := r.Context()
		to := s.events.now().UTC().Truncate(24 * time.Hour)
		from := to.AddDate(0, 0, -(days - 1))
		data.From, data.To = from.Format(dayLayout), to.Format(dayLayout)
		daily, err := s.events.daily(ctx, from, to)
		if err != nil {
			log.Printf("admin: traffic: %v", err)
			s.errorPage(w, http.StatusInternalServerError, "Error", "The events could not be read.", true)
			return
		}
		for _, d := range daily {
			data.Visitors += d.Visitors
			data.Views += d.Views
		}
		data.Chart = chartSVG(daily)
		open := r.URL.Query().Get("all")
		for i, d := range trafficDimensions {
			rows, err := s.events.breakdown(ctx, i, from, to, open == d.Name, topRows)
			if err != nil {
				log.Printf("admin: traffic: %s: %v", d.Name, err)
				s.errorPage(w, http.StatusInternalServerError, "Error", "The events could not be read.", true)
				return
			}
			t := breakdownTable{Name: d.Name, Title: strings.ToUpper(d.Name[:1]) + d.Name[1:], Head: tableHeads[d.Name]}
			if t.All = open == d.Name; !t.All && len(rows) > topRows {
				rows, t.More = rows[:topRows], true
			}
			for j := range rows {
				rows[j].Key = tableLabel(d.Name, rows[j].Key)
			}
			t.Rows = rows
			t.Href = fmt.Sprintf("/admin/traffic?days=%d", days)
			if t.More {
				t.Href += "&all=" + d.Name
			}
			data.Tables = append(data.Tables, t)
		}
	}
	s.renderAdmin(w, http.StatusOK, trafficTmpl, data)
}

var tableHeads = map[string]string{
	"pages": "Path", "referrers": "Site", "campaigns": "Source / medium / campaign", "countries": "Country", "devices": "Device",
}

// What a key is called when it is empty.
func tableLabel(table, key string) string {
	switch {
	case key != "":
		return key
	case table == "referrers":
		return "Direct"
	}
	return "Unknown"
}

// A bar chart of views and visitors by day, drawn as SVG on the server. It has
// no viewBox: the height is the stylesheet's fixed one, y is in pixels and x is
// in percentages, so the bars fill the width while the text keeps its size.
// The colours are the stylesheet's, through classes; nothing here is script or
// an inline style.
func chartSVG(days []dayCount) template.HTML {
	const height, top, bottom = 200.0, 16.0, 22.0
	peak := 1
	for _, d := range days {
		peak = max(peak, d.Views, d.Visitors)
	}
	scale := niceCeil(peak)
	plotH := height - top - bottom
	y := func(v int) float64 { return top + plotH*(1-float64(v)/float64(scale)) }
	slot := 100 / float64(len(days))

	var b strings.Builder
	b.WriteString(`<svg class="chart" role="img" aria-label="Visitors and views by day">`)
	for _, v := range []int{0, scale / 2, scale} {
		if v == 0 || v == scale || scale%2 == 0 {
			fmt.Fprintf(&b, `<line class="grid" x1="0" x2="100%%" y1="%.1f" y2="%.1f"/><text class="tick" x="2" y="%.1f">%s</text>`,
				y(v), y(v), y(v)-4, group(v))
		}
	}
	for i, d := range days {
		x := slot * float64(i)
		bw := slot * 0.8
		fmt.Fprintf(&b, `<g><title>%s: %s visitors, %s views</title>`, d.Day, group(d.Visitors), group(d.Views))
		fmt.Fprintf(&b, `<rect class="hit" x="%.3f%%" y="%g" width="%.3f%%" height="%g"/>`, x, top, slot, plotH)
		if d.Views > 0 {
			fmt.Fprintf(&b, `<rect class="views" x="%.3f%%" y="%.2f" width="%.3f%%" height="%.2f"/>`, x+slot*0.1, y(d.Views), bw, y(0)-y(d.Views))
		}
		if d.Visitors > 0 {
			fmt.Fprintf(&b, `<rect class="visitors" x="%.3f%%" y="%.2f" width="%.3f%%" height="%.2f"/>`, x+slot*0.1+bw*0.2, y(d.Visitors), bw*0.6, y(0)-y(d.Visitors))
		}
		b.WriteString(`</g>`)
	}
	if len(days) > 0 {
		fmt.Fprintf(&b, `<text class="tick" x="2" y="%g">%s</text>`, height-6, days[0].Day)
		fmt.Fprintf(&b, `<text class="tick" x="100%%" dx="-2" y="%g" text-anchor="end">%s</text>`, height-6, days[len(days)-1].Day)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// A count grouped in thousands: 27,420.
func group(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// The least of 1, 2, 4, 5, 10, 20, 40, 50, ... that is at least n; the even
// steps keep the axis's middle label a whole number.
func niceCeil(n int) int {
	for unit := 1; ; unit *= 10 {
		for _, m := range []int{1, 2, 4, 5} {
			if m*unit >= n {
				return m * unit
			}
		}
	}
}

// ------------------------------------------------------------------ tests --

type rateCell struct {
	Count     int
	Rate      string
	Interval  string
	Chance    string // empty where no chance is shown
	Greyed    bool
	NoVisitor bool
}

type armRow struct {
	Name     string
	Baseline bool
	Visitors int
	Few      bool
	Rates    []rateCell // engaged, reached 75%, form starts, submissions
	Clicks   []rateCell // one per tracked name of the test
}

type testSection struct {
	ID, Route   string
	Ended       bool
	First, Last string
	Arms        []armRow
	Names       []string // the tracked click names
}

var rateHeads = []string{"Engaged", "Reached 75%", "Form starts", "Submissions"}

func percent(f float64) string { return strconv.FormatFloat(f*100, 'f', 1, 64) + "%" }

func rate(k, n int, few bool) rateCell {
	c := rateCell{Count: k, Greyed: few}
	if n == 0 {
		c.NoVisitor = true
		return c
	}
	low, high := wilson(k, n)
	c.Rate = percent(float64(k) / float64(n))
	c.Interval = strings.TrimSuffix(percent(low), "%") + "–" + percent(high)
	return c
}

func (a *armCounts) rateCounts() []int {
	return []int{a.Engaged, a.Reached75, a.FormStarts, a.Submissions}
}

func (s *server) testsPage(w http.ResponseWriter, r *http.Request, _ *adminCreds, _ string) {
	data := struct {
		adminFrame
		Stored   bool
		Sections []testSection
		Heads    []string
	}{adminFrame: s.frame("Tests", "Tests", true), Stored: s.events != nil, Heads: rateHeads}
	if s.events != nil {
		sections, err := s.testSections(r)
		if err != nil {
			log.Printf("admin: tests: %v", err)
			s.errorPage(w, http.StatusInternalServerError, "Error", "The events could not be read.", true)
			return
		}
		data.Sections = sections
	}
	s.renderAdmin(w, http.StatusOK, testsTmpl, data)
}

// Declared tests first, in declaration order, then the ended ones by ID.
func (s *server) testSections(r *http.Request) ([]testSection, error) {
	ctx := r.Context()
	found, err := s.events.testCounts(ctx)
	if err != nil {
		return nil, err
	}
	var out []testSection
	declared := map[string]bool{}
	for _, def := range s.reg.order {
		declared[def.ID] = true
		var names []string
		for _, a := range def.Arms {
			names = append(names, a.Page)
		}
		out = append(out, buildSection(def.ID, def.Route, false, names, found[def.ID]))
	}
	var ended []string
	for id := range found {
		if !declared[id] {
			ended = append(ended, id)
		}
	}
	sort.Strings(ended)
	for _, id := range ended {
		var names []string
		for name := range found[id].Arms {
			names = append(names, name)
		}
		sort.Strings(names)
		route, err := s.events.testRoute(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, buildSection(id, route, true, names, found[id]))
	}
	return out, nil
}

func buildSection(id, route string, ended bool, arms []string, c *testCounts) testSection {
	sec := testSection{ID: id, Route: route, Ended: ended}
	if c == nil {
		c = &testCounts{Arms: map[string]*armCounts{}}
	}
	sec.First, sec.Last = c.First, c.Last
	nameSet := map[string]bool{}
	for _, a := range arms {
		if ac := c.Arms[a]; ac != nil {
			for n := range ac.Clicks {
				nameSet[n] = true
			}
		}
	}
	for n := range nameSet {
		sec.Names = append(sec.Names, n)
	}
	sort.Strings(sec.Names)
	var base *armCounts
	for i, name := range arms {
		ac := c.Arms[name]
		if ac == nil {
			ac = &armCounts{}
		}
		if i == 0 && !ended {
			base = ac
		}
		row := armRow{Name: name, Baseline: i == 0 && !ended, Visitors: ac.Visitors, Few: ac.Visitors < fewVisitors}
		for j, k := range ac.rateCounts() {
			cell := rate(k, ac.Visitors, row.Few)
			if base != nil && !row.Baseline && ac.Visitors > 0 && base.Visitors > 0 {
				cell.Chance = percent(chanceToBeat(id+"|"+rateHeads[j], base.rateCounts()[j], base.Visitors, k, ac.Visitors))
				cell.Greyed = cell.Greyed || base.Visitors < fewVisitors
			}
			row.Rates = append(row.Rates, cell)
		}
		for _, n := range sec.Names {
			row.Clicks = append(row.Clicks, rate(ac.Clicks[n], ac.Visitors, row.Few))
		}
		sec.Arms = append(sec.Arms, row)
	}
	return sec
}
