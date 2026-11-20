package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestCatalog(t *testing.T) {
	b, err := os.ReadFile("../../.chug/configurations/default.json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Configuration struct {
			Workloads map[string]map[string]any `json:"workloads"`
			Plans     map[string][]struct {
				Name       string   `json:"name"`
				Evaluators []string `json:"evaluators"`
			} `json:"plans"`
			Defaults map[string]string `json:"defaults"`
		} `json:"configuration"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	cfg := d.Configuration
	for name, p := range cfg.Plans {
		for _, s := range p {
			for _, e := range s.Evaluators {
				if _, ok := cfg.Workloads[e]; !ok {
					t.Errorf("plan %s stage %s: unknown workload %s", name, s.Name, e)
				}
			}
		}
	}
	if cfg.Defaults["work"] != "work" || cfg.Defaults["evaluation"] != "standard" || cfg.Defaults["finalization"] != "stacked" {
		t.Errorf("defaults changed: %v", cfg.Defaults)
	}
	for _, n := range []string{"workflow-run-work", "workflow-run"} {
		w := cfg.Workloads[n]
		if w["runner"] != "Commands" {
			t.Errorf("%s runner %v", n, w["runner"])
		}
		cmds := w["commands"].([]any)
		if len(cmds) != 1 || !strings.Contains(cmds[0].(string), "--repo kasofsk/chug.gy") || !strings.Contains(cmds[0].(string), "--outcome success") {
			t.Errorf("%s commands %v", n, cmds)
		}
		creds := w["authority"].(map[string]any)["credentials"]
		if !reflect.DeepEqual(creds, []any{"chuggy.github-workflow-runs"}) {
			t.Errorf("%s credentials %v", n, creds)
		}
		if !reflect.DeepEqual(w["worker"], cfg.Workloads["checks"]["worker"]) {
			t.Errorf("%s worker differs from checks", n)
		}
	}
	p := cfg.Plans["workflow-proof"]
	if len(p) != 2 || p[0].Name != "checks" || !reflect.DeepEqual(p[0].Evaluators, []string{"checks"}) ||
		p[1].Name != "workflow-run" || !reflect.DeepEqual(p[1].Evaluators, []string{"workflow-run"}) {
		t.Errorf("plan workflow-proof: %v", p)
	}
}

func TestWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/chuggy-proof.yml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"workflow_dispatch:", "type: choice", "- success", "- failure", "inputs.request", "permissions: {}", "runs-on: ubuntu-latest"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(s, "uses:") || strings.Contains(s, "run: |\n          echo \"${{") {
		t.Error("unexpected uses or interpolation")
	}
}
