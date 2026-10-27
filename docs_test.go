package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A docs checkout of two pages, shaped like kasofsk/chuggy-docs.
func fakeDocs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "# chuggy docs\n\n- [Jobs](docs/index.md)\n  - [A job's container](docs/jobs/container.md)\n- [Not a page](docs/missing.md)\n")
	write("llms.txt", "# chuggy docs\n\n- [Overview](https://raw.githubusercontent.com/kasofsk/chuggy-docs/main/docs/index.md): the terms.\n- [A job's container](https://raw.githubusercontent.com/kasofsk/chuggy-docs/main/docs/jobs/container.md): start, user.\n")
	write("docs/index.md", "---\ntitle: Jobs\ndescription: What chuggy runs a project's stages in.\n---\n\n# Jobs\n\nSee [the container](jobs/container.md) and [its user](jobs/container.md#user).\n")
	write("docs/jobs/container.md", "---\ntitle: A job's container\ndescription: \"Every job, in a container.\"\n---\n\n# A job's container\n\n## User\n\nBack to the [index](../index.md), the [runner](https://github.com/kasofsk/chuggy-linux#configure), and [below](#user).\n\n| Key | Value |\n|---|---|\n| uid | 1000 |\n")
	return dir
}

func docsServer(t *testing.T) http.Handler {
	t.Helper()
	s, err := newServer(config{docsDir: fakeDocs(t), host: "chug.gy"})
	if err != nil {
		t.Fatal(err)
	}
	return s.handler()
}

func TestDocsPagesRender(t *testing.T) {
	h := docsServer(t)
	w := get(h, "chug.gy", "/docs")
	if w.Code != 200 {
		t.Fatalf("/docs answered %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"<title>Jobs · chuggy docs</title>",
		`href="/docs/jobs/container"`,
		`href="/docs/jobs/container#user"`,
		`aria-current="page">Jobs</a>`,
		`<li class="nested"><a href="/docs/jobs/container">A job&#39;s container</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/docs lacks %q", want)
		}
	}
	if strings.Contains(body, "Not a page") {
		t.Error("a README entry with no file is listed")
	}
	if strings.Count(body, "<h1>") != 1 {
		t.Errorf("the body's own heading was not dropped: %d h1", strings.Count(body, "<h1>"))
	}

	w = get(h, "chug.gy", "/docs/jobs/container/")
	if w.Code != 200 {
		t.Fatalf("/docs/jobs/container/ answered %d", w.Code)
	}
	body = w.Body.String()
	for _, want := range []string{
		`<h2 id="user">User</h2>`,
		`href="/docs"`,
		`href="https://github.com/kasofsk/chuggy-linux#configure"`,
		`href="#user"`,
		"<table>",
		`content="Every job, in a container."`,
		`href="https://github.com/kasofsk/chuggy-docs/blob/main/docs/jobs/container.md"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("container page lacks %q", want)
		}
	}
	if w := get(h, "chug.gy", "/docs/nope"); w.Code != 404 {
		t.Errorf("an unknown page answered %d", w.Code)
	}
}

func TestLLMsNamesThePagesHere(t *testing.T) {
	w := get(docsServer(t), "chug.gy", "/llms.txt")
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("/llms.txt answered %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "(https://chug.gy/docs/jobs/container)") || !strings.Contains(w.Body.String(), "(https://chug.gy/docs):") {
		t.Errorf("llms.txt still names GitHub:\n%s", w.Body.String())
	}
}

func TestWithoutDocsTheRoutesRedirect(t *testing.T) {
	_, h, _ := testServer(t, false)
	for _, p := range []string{"/docs", "/llms.txt"} {
		w := get(h, "chug.gy", p)
		if w.Code != 302 || !strings.Contains(w.Header().Get("Location"), "github") {
			t.Errorf("%s answered %d %s", p, w.Code, w.Header().Get("Location"))
		}
	}
}

func TestDocPaths(t *testing.T) {
	for source, want := range map[string]string{
		"index.md":          "/docs",
		"jobs/index.md":     "/docs/jobs",
		"jobs/container.md": "/docs/jobs/container",
		"repository.md":     "/docs/repository",
	} {
		if got := docPath(source); got != want {
			t.Errorf("docPath(%q) = %q, want %q", source, got, want)
		}
	}
}

func TestDocsRefuseAMissingIndex(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("- [x](docs/x.md)\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "llms.txt"), nil, 0o644)
	if _, err := newServer(config{docsDir: dir}); err == nil {
		t.Error("a docs directory with no index.md was accepted")
	}
	_ = httptest.NewRecorder
}
