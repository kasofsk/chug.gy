// Command workflowrun starts the chuggy-proof workflow on GitHub, waits for
// the run it started and exits with the run's result.
//
// Exit status: 0 the run succeeded, 1 it concluded otherwise or ran a
// different commit than CHUG_COMMIT, 2 it could not finish.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const workflowFile = "chuggy-proof.yml"

type config struct {
	repo     string
	outcome  string
	api      string
	timeout  time.Duration
	interval time.Duration
	ref      string // CHUG_REF
	commit   string // CHUG_COMMIT
	tokenEnv string // GH_TOKEN_FILE
}

func main() {
	var c config
	flag.StringVar(&c.repo, "repo", "", "repository as owner/name")
	flag.StringVar(&c.outcome, "outcome", "", "outcome to ask the workflow for: success or failure")
	flag.StringVar(&c.api, "api", "https://api.github.com", "GitHub API base URL")
	flag.DurationVar(&c.timeout, "timeout", 30*time.Minute, "give up after this long")
	flag.DurationVar(&c.interval, "interval", 5*time.Second, "pause between asking again")
	flag.Parse()
	c.ref = os.Getenv("CHUG_REF")
	c.commit = os.Getenv("CHUG_COMMIT")
	c.tokenEnv = os.Getenv("GH_TOKEN_FILE")
	os.Exit(run(context.Background(), c, os.Stdout, os.Stderr))
}

type workflowRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Title      string `json:"display_title"`
	URL        string `json:"html_url"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HeadSHA    string `json:"head_sha"`
}

type client struct {
	c    config
	http *http.Client
}

func (cl *client) do(ctx context.Context, method, path string, body any, out any) error {
	if cl.c.tokenEnv == "" {
		return errors.New("GH_TOKEN_FILE is not set")
	}
	data, err := os.ReadFile(cl.c.tokenEnv)
	if err != nil {
		return fmt.Errorf("cannot read GH_TOKEN_FILE: %s", errors.Unwrap(err))
	}
	token := strings.TrimSpace(string(data))
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(cl.c.api, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := cl.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GitHub API %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(b)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("GitHub API %s %s: bad response: %v", method, path, err)
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func run(ctx context.Context, c config, stdout, stderr io.Writer) int {
	fail := func(code int, format string, a ...any) int {
		fmt.Fprintf(stderr, format+"\n", a...)
		return code
	}
	if owner, name, ok := strings.Cut(c.repo, "/"); !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return fail(2, "--repo must be owner/name")
	}
	if c.outcome != "success" && c.outcome != "failure" {
		return fail(2, "--outcome must be success or failure")
	}
	branch := ""
	if c.ref != "" {
		b, ok := strings.CutPrefix(c.ref, "refs/heads/")
		if !ok || b == "" {
			return fail(2, "CHUG_REF %q is not a branch (refs/heads/...)", c.ref)
		}
		branch = b
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	cl := &client{c: c, http: &http.Client{}}
	timedOut := func(err error) int {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fail(2, "timed out after %s", c.timeout)
		}
		return fail(2, "%v", err)
	}

	repoPath := "/repos/" + c.repo
	if branch == "" {
		var r struct {
			DefaultBranch string `json:"default_branch"`
		}
		if err := cl.do(ctx, "GET", repoPath, nil, &r); err != nil {
			return timedOut(err)
		}
		if r.DefaultBranch == "" {
			return fail(2, "repository has no default branch")
		}
		branch = r.DefaultBranch
	}

	var rb [8]byte
	if _, err := rand.Read(rb[:]); err != nil {
		return fail(2, "%v", err)
	}
	request := hex.EncodeToString(rb[:])
	wf := repoPath + "/actions/workflows/" + workflowFile
	err := cl.do(ctx, "POST", wf+"/dispatches", map[string]any{
		"ref":    branch,
		"inputs": map[string]string{"outcome": c.outcome, "request": request},
	}, nil)
	if err != nil {
		return timedOut(err)
	}

	var found *workflowRun
	q := "?event=workflow_dispatch&per_page=50&branch=" + url.QueryEscape(branch)
	for found == nil {
		var l struct {
			Runs []workflowRun `json:"workflow_runs"`
		}
		if err := cl.do(ctx, "GET", wf+"/runs"+q, nil, &l); err != nil {
			return timedOut(err)
		}
		for i := range l.Runs {
			if strings.Contains(l.Runs[i].Title, request) || strings.Contains(l.Runs[i].Name, request) {
				found = &l.Runs[i]
				break
			}
		}
		if found == nil {
			if err := sleep(ctx, c.interval); err != nil {
				return timedOut(err)
			}
		}
	}

	fmt.Fprintf(stdout, "run: %s\n", found.URL)
	cur := *found
	last := ""
	for {
		if cur.Status != last {
			fmt.Fprintf(stdout, "status: %s\n", cur.Status)
			last = cur.Status
		}
		if cur.Status == "completed" {
			break
		}
		if err := sleep(ctx, c.interval); err != nil {
			return timedOut(err)
		}
		if err := cl.do(ctx, "GET", fmt.Sprintf("%s/actions/runs/%d", repoPath, found.ID), nil, &cur); err != nil {
			return timedOut(err)
		}
	}

	if c.commit != "" && cur.HeadSHA != c.commit {
		return fail(1, "run tested commit %s, not CHUG_COMMIT %s", cur.HeadSHA, c.commit)
	}
	if cur.Conclusion != "success" {
		return fail(1, "run concluded %q", cur.Conclusion)
	}
	fmt.Fprintln(stdout, "conclusion: success")
	return 0
}
