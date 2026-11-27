// The policies: the terms of service and the privacy policy, each published as
// versions that are files under legal/published/<slug>/<version>.md.
//
// A version is served at an address of its own that never serves another text
// (/<slug>/<version>, and the file's bytes at /<slug>/<version>.md), so an
// agreement can record that address and the sha256 digest of the bytes. The
// declarations below are checked when the server starts, as the pages are.
// The drafts in legal/ are not embedded and nothing serves them.
package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

//go:embed legal/published
var legalEmbedded embed.FS

// A policy as declared: its slug (its address), its title, the short name a
// footer links it by, and its published versions.
type policyDef struct {
	Slug     string
	Title    string
	Short    string
	Versions []versionDef
}

// A version is the date it takes effect, 2006-01-02, and the digest of its
// file's bytes, "sha256:" and lowercase hex.
type versionDef struct {
	Version string
	Digest  string
}

var policyDefs = []policyDef{
	{Slug: "terms", Title: "Terms of Service", Short: "Terms", Versions: []versionDef{
		{Version: "2026-11-27", Digest: "sha256:2e4db0a8427adf3ad494197bad9701085462a93f2e8538a946245f5252f27bc2"},
	}},
	{Slug: "privacy", Title: "Privacy Policy", Short: "Privacy", Versions: []versionDef{
		{Version: "2026-11-27", Digest: "sha256:5a513c5678d3269680c578985d9486c949cc57e73942f0a43fb0661d218e9a65"},
	}},
}

const (
	versionLayout   = "2006-01-02"
	effectiveLayout = "January 2, 2006"
	effectivePrefix = "Effective "
)

// A footer's link to a policy that has a version.
type policyLink struct {
	Slug  string
	Short string
}

type policyVersion struct {
	version string
	raw     []byte
	page    []byte
}

type policy struct {
	def      policyDef
	versions []*policyVersion // newest first
	byDate   map[string]*policyVersion
}

type policies struct {
	list  []*policy
	links []policyLink
}

func (p *policy) current() *policyVersion { return p.versions[0] }

// The routes of every policy in the table, which no page or slot may take.
func policyRoutes(defs []policyDef) []string {
	var routes []string
	for _, d := range defs {
		routes = append(routes, "/"+d.Slug, "/"+d.Slug+"/")
	}
	return routes
}

var legalPage = template.Must(template.New("legal").Parse(string(mustRead("legal.html"))))

// Checks the declarations against the files and renders every page, or says
// what is wrong. reserved are the routes the server already has; a slug may
// not be one of them, nor a route under one that ends in "/".
func loadPolicies(files fs.FS, defs []policyDef, reserved []string, stylesKey string) (*policies, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	out := &policies{}
	slugs := map[string]bool{}
	declared := map[string]bool{"README.md": true}
	for _, d := range defs {
		if !idPattern.MatchString(d.Slug) {
			return nil, fmt.Errorf("policy %q: a slug is lowercase letters, digits and hyphens", d.Slug)
		}
		if slugs[d.Slug] {
			return nil, fmt.Errorf("policy %q is declared twice", d.Slug)
		}
		slugs[d.Slug] = true
		if collides("/"+d.Slug, reserved) || collides("/"+d.Slug+"/", reserved) {
			return nil, fmt.Errorf("policy %q: its route collides with another route of the server", d.Slug)
		}
		if d.Title == "" || d.Short == "" {
			return nil, fmt.Errorf("policy %q needs a title and a short name", d.Slug)
		}
		p := &policy{def: d, byDate: map[string]*policyVersion{}}
		for _, v := range d.Versions {
			who := fmt.Sprintf("policy %q version %q", d.Slug, v.Version)
			date, err := time.Parse(versionLayout, v.Version)
			if err != nil || date.Format(versionLayout) != v.Version {
				return nil, fmt.Errorf("%s: a version is a date written 2006-01-02", who)
			}
			if p.byDate[v.Version] != nil {
				return nil, fmt.Errorf("%s is declared twice", who)
			}
			name := d.Slug + "/" + v.Version + ".md"
			declared[name] = true
			raw, err := fs.ReadFile(files, name)
			if err != nil {
				return nil, fmt.Errorf("%s: its file is absent: %w", who, err)
			}
			sum := sha256.Sum256(raw)
			if got := "sha256:" + hex.EncodeToString(sum[:]); got != v.Digest {
				return nil, fmt.Errorf("%s: the file hashes to %s, not the declared %s", who, got, v.Digest)
			}
			if err := checkEffective(raw, date); err != nil {
				return nil, fmt.Errorf("%s: %w", who, err)
			}
			var body bytes.Buffer
			if err := md.Convert(raw, &body); err != nil {
				return nil, fmt.Errorf("%s: %w", who, err)
			}
			pv := &policyVersion{version: v.Version, raw: raw, page: body.Bytes()}
			p.byDate[v.Version] = pv
			p.versions = append(p.versions, pv)
		}
		sort.Slice(p.versions, func(i, j int) bool { return p.versions[i].version > p.versions[j].version })
		out.list = append(out.list, p)
		if len(p.versions) > 0 {
			out.links = append(out.links, policyLink{Slug: d.Slug, Short: d.Short})
		}
	}
	err := fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && !declared[name] {
			return fmt.Errorf("%s is neither the README nor a declared version", name)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("legal/published: %w", err)
	}
	// Bodies are rendered into pages now that every footer link is known.
	for _, p := range out.list {
		for _, pv := range p.versions {
			page, err := out.render(p, pv, stylesKey)
			if err != nil {
				return nil, fmt.Errorf("policy %q version %q: %w", p.def.Slug, pv.version, err)
			}
			pv.page = page
		}
	}
	return out, nil
}

// The text carries exactly one line that begins "Effective ", and it states
// the date the version is filed under.
func checkEffective(raw []byte, date time.Time) error {
	var lines []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, effectivePrefix) {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		return fmt.Errorf("the text has %d lines beginning %q, not one", len(lines), effectivePrefix)
	}
	if want := effectivePrefix + date.Format(effectiveLayout); lines[0] != want {
		return fmt.Errorf("the text says %q, not %q", lines[0], want)
	}
	return nil
}

type legalData struct {
	Title     string
	Slug      string
	Version   string
	Current   string
	IsCurrent bool
	Body      template.HTML
	Others    []string // the other versions, newest first
	StylesKey string
	Policies  []policyLink
}

func (ps *policies) render(p *policy, pv *policyVersion, stylesKey string) ([]byte, error) {
	data := legalData{
		Title:     p.def.Title,
		Slug:      p.def.Slug,
		Version:   pv.version,
		Current:   p.current().version,
		IsCurrent: pv == p.current(),
		Body:      template.HTML(pv.page),
		StylesKey: stylesKey,
		Policies:  ps.links,
	}
	for _, other := range p.versions {
		if other != pv {
			data.Others = append(data.Others, other.version)
		}
	}
	var buf bytes.Buffer
	if err := legalPage.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Mounts the routes of each policy that has a version; one without has none,
// so everything under it is 404.
func (ps *policies) mount(mux *http.ServeMux) {
	for _, p := range ps.list {
		if len(p.versions) == 0 {
			continue
		}
		cached := func(w http.ResponseWriter, contentType string, body []byte) {
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.Header().Set("Content-Type", contentType)
			w.Write(body)
		}
		mux.HandleFunc("GET /"+p.def.Slug, func(w http.ResponseWriter, r *http.Request) {
			cached(w, "text/html; charset=utf-8", p.current().page)
		})
		mux.HandleFunc("GET /"+p.def.Slug+"/{rest}", func(w http.ResponseWriter, r *http.Request) {
			rest := r.PathValue("rest")
			if v, ok := strings.CutSuffix(rest, ".md"); ok {
				if pv := p.byDate[v]; pv != nil {
					cached(w, "text/markdown; charset=utf-8", pv.raw)
					return
				}
			} else if pv := p.byDate[rest]; pv != nil {
				cached(w, "text/html; charset=utf-8", pv.page)
				return
			}
			http.NotFound(w, r)
		})
	}
}
