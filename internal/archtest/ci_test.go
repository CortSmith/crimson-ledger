package archtest

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ciConfig loads the repo's CircleCI config; the Docker build context excludes it, so the test skips there.
func ciConfig(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("../../../.circleci/config.yml")
	if err != nil {
		t.Skip("no .circleci/config.yml in this build context")
	}
	var c map[string]any
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// appJobs returns the job entries of the "app" workflow as name -> options.
func appJobs(t *testing.T, c map[string]any) map[string]map[string]any {
	t.Helper()
	wf, _ := c["workflows"].(map[string]any)["app"].(map[string]any)
	out := map[string]map[string]any{}
	for _, j := range wf["jobs"].([]any) {
		for name, opts := range j.(map[string]any) {
			o, _ := opts.(map[string]any)
			out[name] = o
		}
	}
	return out
}

func TestCI_MacOSOnlyOnTags(t *testing.T) {
	j := appJobs(t, ciConfig(t))["build-macos"]
	if j == nil {
		t.Fatal("build-macos is not in the app workflow")
	}
	f, _ := j["filters"].(map[string]any)
	if f["tags"] == nil {
		t.Fatal("build-macos needs a tags filter (macOS minutes cost credits)")
	}
	if b, _ := f["branches"].(map[string]any); b == nil || b["ignore"] == nil {
		t.Fatal("build-macos must ignore all branches")
	}
}

func TestCI_ReleaseIsProtected(t *testing.T) {
	j := appJobs(t, ciConfig(t))["app-release"]
	if j["context"] != "app-release" {
		t.Fatal("app-release must use the app-release context (secrets live there)")
	}
	f, _ := j["filters"].(map[string]any)
	if b, _ := f["branches"].(map[string]any); b == nil || b["ignore"] == nil || f["tags"] == nil {
		t.Fatal("app-release must run for tags only")
	}
	for _, name := range []string{"build-linux", "build-windows", "build-macos"} {
		if !strings.Contains(strings.Join(toStrings(j["requires"]), ","), name) {
			t.Fatalf("app-release must wait for %s", name)
		}
	}
}

func toStrings(v any) []string {
	var s []string
	for _, x := range v.([]any) {
		s = append(s, x.(string))
	}
	return s
}
