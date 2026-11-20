package main

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestChugConfiguration(t *testing.T) {
	raw, err := os.ReadFile(".chug/configurations/default.json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Configuration struct {
			Workloads map[string]struct {
				Runner    string
				Commands  []string
				Authority struct{ Credentials []string }
				Worker    struct{ Setup []string }
			}
			Plans map[string][]struct {
				Name       string
				Evaluators []string
			}
			Defaults map[string]string
		}
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	c := d.Configuration
	for plan, stages := range c.Plans {
		for _, s := range stages {
			for _, e := range s.Evaluators {
				if _, ok := c.Workloads[e]; !ok {
					t.Errorf("plan %s stage %s: unknown workload %s", plan, s.Name, e)
				}
			}
		}
	}
	if _, ok := c.Plans[c.Defaults["evaluation"]]; !ok || c.Defaults["work"] != "work" {
		t.Errorf("defaults changed: %v", c.Defaults)
	}
	for _, n := range []string{"workflow-run-work", "workflow-run"} {
		w := c.Workloads[n]
		if w.Runner != "Commands" || len(w.Commands) != 1 ||
			!strings.Contains(w.Commands[0], "-repo kasofsk/chug.gy") || !strings.Contains(w.Commands[0], "-outcome success") ||
			!slices.Equal(w.Authority.Credentials, []string{"chuggy.github-workflow-runs"}) ||
			!slices.Equal(w.Worker.Setup, c.Workloads["checks"].Worker.Setup) {
			t.Errorf("%s: %+v", n, w)
		}
	}
	p := c.Plans["workflow-proof"]
	if len(p) != 2 || p[0].Name != "checks" || !slices.Equal(p[0].Evaluators, []string{"checks"}) ||
		p[1].Name != "workflow-run" || !slices.Equal(p[1].Evaluators, []string{"workflow-run"}) {
		t.Errorf("workflow-proof: %+v", p)
	}
}
