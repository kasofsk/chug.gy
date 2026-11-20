// Command workflowrun starts the chuggy-proof workflow on GitHub and waits for
// it. It exits 0 when the run succeeded, 1 when it concluded otherwise or ran
// a different commit than CHUG_COMMIT, and 2 when it could not finish.
package main

import (
	"bytes"
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

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// failure is a run that finished but did not pass: exit 1.
type failure struct{ msg string }

func (f failure) Error() string { return f.msg }

type runner struct {
	api      string
	repo     string
	tokFile  string
	client   *http.Client
	out      io.Writer
	interval time.Duration
	deadline time.Time
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("workflowrun", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", "", "repository as owner/name")
	outcome := fs.String("outcome", "", "outcome to ask the workflow for: success or failure")
	api := fs.String("api", "https://api.github.com", "GitHub API base URL")
	timeout := fs.Duration("timeout", 30*time.Minute, "give up after this long")
	interval := fs.Duration("interval", 5*time.Second, "time between requests while waiting")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if parts := strings.Split(*repo, "/"); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		fmt.Fprintln(stderr, "workflowrun: -repo must be owner/name")
		return 2
	}
	if *outcome != "success" && *outcome != "failure" {
		fmt.Fprintln(stderr, "workflowrun: -outcome must be success or failure")
		return 2
	}
	r := &runner{
		api:      strings.TrimRight(*api, "/"),
		repo:     *repo,
		tokFile:  getenv("GH_TOKEN_FILE"),
		client:   &http.Client{Timeout: 30 * time.Second},
		out:      stdout,
		interval: *interval,
		deadline: time.Now().Add(*timeout),
	}
	err := r.do(*outcome, getenv("CHUG_REF"), getenv("CHUG_COMMIT"))
	var f failure
	switch {
	case err == nil:
		return 0
	case errors.As(err, &f):
		fmt.Fprintln(stderr, "workflowrun: failed:", f.msg)
		return 1
	default:
		fmt.Fprintln(stderr, "workflowrun: could not finish:", err)
		return 2
	}
}

func (r *runner) do(outcome, ref, commit string) error {
	branch := ""
	if ref != "" {
		var ok bool
		if branch, ok = strings.CutPrefix(ref, "refs/heads/"); !ok || branch == "" {
			return fmt.Errorf("CHUG_REF %q is not a branch (refs/heads/...)", ref)
		}
	} else {
		var repo struct {
			DefaultBranch string `json:"default_branch"`
		}
		if err := r.call("GET", "/repos/"+r.repo, nil, &repo); err != nil {
			return err
		}
		if repo.DefaultBranch == "" {
			return errors.New("the repository has no default branch")
		}
		branch = repo.DefaultBranch
	}

	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	request := "chuggy-" + hex.EncodeToString(buf)
	body := map[string]any{"ref": branch, "inputs": map[string]string{"outcome": outcome, "request": request}}
	wf := "/repos/" + r.repo + "/actions/workflows/" + workflowFile
	if err := r.call("POST", wf+"/dispatches", body, nil); err != nil {
		return err
	}

	type run struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Title      string `json:"display_title"`
		HeadSHA    string `json:"head_sha"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		URL        string `json:"html_url"`
	}
	var found run
	list := wf + "/runs?event=workflow_dispatch&branch=" + url.QueryEscape(branch)
	for found.ID == 0 {
		var page struct {
			Runs []run `json:"workflow_runs"`
		}
		if err := r.call("GET", list, nil, &page); err != nil {
			return err
		}
		for _, x := range page.Runs {
			if strings.Contains(x.Name, request) || strings.Contains(x.Title, request) {
				found = x
				break
			}
		}
		if found.ID == 0 {
			if err := r.wait("the run to appear"); err != nil {
				return err
			}
		}
	}
	fmt.Fprintln(r.out, "run:", found.URL)

	last := ""
	cur := found
	for {
		if cur.Status != last {
			last = cur.Status
			fmt.Fprintln(r.out, "status:", cur.Status)
		}
		if cur.Status == "completed" {
			break
		}
		if err := r.wait("the run to complete"); err != nil {
			return err
		}
		cur = run{}
		if err := r.call("GET", fmt.Sprintf("/repos/%s/actions/runs/%d", r.repo, found.ID), nil, &cur); err != nil {
			return err
		}
	}
	if commit != "" && cur.HeadSHA != commit {
		return failure{fmt.Sprintf("the run tested commit %s, not %s", cur.HeadSHA, commit)}
	}
	if cur.Conclusion != "success" {
		return failure{"the run concluded " + cur.Conclusion}
	}
	fmt.Fprintln(r.out, "conclusion: success")
	return nil
}

func (r *runner) wait(what string) error {
	if !time.Now().Add(r.interval).Before(r.deadline) {
		return fmt.Errorf("timed out waiting for %s", what)
	}
	time.Sleep(r.interval)
	return nil
}

// call makes one API request, reading the token file afresh.
func (r *runner) call(method, path string, in, out any) error {
	if r.tokFile == "" {
		return errors.New("GH_TOKEN_FILE is not set")
	}
	raw, err := os.ReadFile(r.tokFile)
	if err != nil {
		return fmt.Errorf("cannot read GH_TOKEN_FILE: %w", errors.Unwrap(err))
	}
	token := strings.TrimSpace(string(raw))
	var rd io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, r.api+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %s", method, path, strings.ReplaceAll(err.Error(), token, "***"))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		msg := strings.ReplaceAll(strings.TrimSpace(string(data)), token, "***")
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return fmt.Errorf("%s %s: HTTP %d %s", method, path, resp.StatusCode, msg)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: bad response: %w", method, path, err)
		}
	}
	return nil
}
