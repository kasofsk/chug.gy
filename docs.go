// The docs: kasofsk/chuggy-docs, rendered into this site at /docs.
//
// The image carries a checkout of the docs repository at the commit the
// Dockerfile pins, and CHUG_SITE_DOCS names its directory. At start every page
// is rendered once into memory, so serving one is a map lookup. Unset, the
// site sends /docs and /llms.txt to GitHub instead, as it did before the docs
// were rendered here.
//
// THE README IS THE TABLE OF CONTENTS. The docs repository's README lists its
// pages in reading order, each as a link to a file under docs/, and that list
// is the sidebar. A page not in the list is still served, but not listed.
//
// LINKS BETWEEN PAGES ARE REWRITTEN, NOT THE SOURCE. A docs page links to
// another as `host.md` or `jobs/host.md`, which GitHub resolves against the
// file. The renderer resolves the same way and emits the page's path here,
// keeping any fragment, so the one source reads right in both places.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

const docsPrefix = "/docs"

type docPage struct {
	// The path this page is served at, such as /docs or /docs/jobs/host.
	Path        string
	Title       string
	Description string
	Body        template.HTML
	// The page's file under docs/, such as jobs/host.md.
	Source string
}

type docs struct {
	pages map[string]*docPage
	// The README's list, in its order.
	listed []*docPage
	llms   []byte
}

// The README's page list: a bullet whose link is a file under docs/.
var readmeEntry = regexp.MustCompile(`(?m)^- \[([^\]]+)\]\(docs/([^)#]+\.md)\)`)

// Reads a docs checkout and renders every page. host is the name the rendered
// llms.txt and absolute links carry.
func loadDocs(dir, host string) (*docs, error) {
	root := os.DirFS(dir)
	d := &docs{pages: map[string]*docPage{}}
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	err := fs.WalkDir(root, "docs", func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		raw, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		source := strings.TrimPrefix(p, "docs/")
		page, err := renderDoc(md, source, raw)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		d.pages[page.Path] = page
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, ok := d.pages[docsPrefix]; !ok {
		return nil, errors.New("the docs have no index.md")
	}
	readme, err := fs.ReadFile(root, "README.md")
	if err != nil {
		return nil, err
	}
	for _, m := range readmeEntry.FindAllSubmatch(readme, -1) {
		if page, ok := d.pages[docPath(string(m[2]))]; ok {
			d.listed = append(d.listed, page)
		}
	}
	if len(d.listed) == 0 {
		return nil, errors.New("the README lists no pages")
	}
	llms, err := fs.ReadFile(root, "llms.txt")
	if err != nil {
		return nil, err
	}
	d.llms = rewriteLLMs(llms, host)
	return d, nil
}

// The path a docs file is served at: docs/index.md is /docs, and any other
// drops its extension under it.
func docPath(source string) string {
	if source == "index.md" {
		return docsPrefix
	}
	return docsPrefix + "/" + strings.TrimSuffix(source, ".md")
}

var frontMatter = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n`)

func renderDoc(md goldmark.Markdown, source string, raw []byte) (*docPage, error) {
	page := &docPage{Path: docPath(source), Source: source}
	if m := frontMatter.FindSubmatch(raw); m != nil {
		for _, line := range strings.Split(string(m[1]), "\n") {
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			value = strings.Trim(strings.TrimSpace(value), `"`)
			switch strings.TrimSpace(key) {
			case "title":
				page.Title = value
			case "description":
				page.Description = value
			}
		}
		raw = raw[len(m[0]):]
	}
	if page.Title == "" {
		return nil, errors.New("no title in the front matter")
	}
	// The body's own first heading repeats the title; the template sets it.
	raw = bytes.TrimLeft(raw, "\n")
	if bytes.HasPrefix(raw, []byte("# ")) {
		if i := bytes.IndexByte(raw, '\n'); i >= 0 {
			raw = raw[i+1:]
		} else {
			raw = nil
		}
	}
	ctx := parser.NewContext()
	doc := md.Parser().Parse(text.NewReader(raw), parser.WithContext(ctx))
	rewriteLinks(doc, source)
	var out bytes.Buffer
	if err := md.Renderer().Render(&out, raw, doc); err != nil {
		return nil, err
	}
	page.Body = template.HTML(out.String())
	return page, nil
}

// Points every link to a sibling .md file at that page's path here.
func rewriteLinks(doc ast.Node, source string) {
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		link, ok := n.(*ast.Link)
		if !ok {
			return ast.WalkContinue, nil
		}
		dest := string(link.Destination)
		if strings.Contains(dest, "://") || strings.HasPrefix(dest, "/") || strings.HasPrefix(dest, "#") {
			return ast.WalkContinue, nil
		}
		target, fragment, _ := strings.Cut(dest, "#")
		if !strings.HasSuffix(target, ".md") {
			return ast.WalkContinue, nil
		}
		resolved := path.Join(path.Dir(source), target)
		rewritten := docPath(resolved)
		if fragment != "" {
			rewritten += "#" + fragment
		}
		link.Destination = []byte(rewritten)
		return ast.WalkContinue, nil
	})
}

// llms.txt names each page by its raw file on GitHub; here it names the page.
func rewriteLLMs(llms []byte, host string) []byte {
	raw := regexp.MustCompile(`https://raw\.githubusercontent\.com/kasofsk/chuggy-docs/[^/]+/docs/([^)\s]+)\.md`)
	return raw.ReplaceAllFunc(llms, func(m []byte) []byte {
		sub := raw.FindSubmatch(m)
		return []byte("https://" + host + docPath(string(sub[1])+".md"))
	})
}

var docsPage = template.Must(template.New("docs").Parse(string(mustRead("docs.html"))))

func mustRead(name string) []byte {
	b, err := site.ReadFile("site/" + name)
	if err != nil {
		panic(err)
	}
	return b
}

func (s *server) docsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimSuffix(r.URL.Path, "/")
		if p == "" {
			p = docsPrefix
		}
		page, ok := s.docs.pages[p]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		var out bytes.Buffer
		err := docsPage.Execute(&out, map[string]any{
			"StylesKey": stylesKey,
			"Page":      page,
			"Listed":    s.docs.listed,
			"Source":    "https://github.com/kasofsk/chuggy-docs/blob/main/docs/" + page.Source,
		})
		if err != nil {
			http.Error(w, "the page could not be drawn", http.StatusInternalServerError)
			return
		}
		w.Write(out.Bytes())
	})
}

func (s *server) llmsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write(s.docs.llms)
}
