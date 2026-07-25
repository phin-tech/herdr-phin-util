package config

import (
	"reflect"
	"strings"
	"testing"
)

// The common case needs no [projects] section: the repo templates already say
// where checkouts live.
func TestProjectRootsDerivedFromRepoTemplates(t *testing.T) {
	got, err := LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"~/src/*/*"}
	if !reflect.DeepEqual(got.Projects.Roots, want) {
		t.Errorf("Roots = %v, want %v", got.Projects.Roots, want)
	}
	if !got.Projects.GitOnly {
		t.Error("GitOnly should default on")
	}
	if got.Projects.Depth != defaultProjectDepth {
		t.Errorf("Depth = %d, want %d", got.Projects.Depth, defaultProjectDepth)
	}
}

func TestDeriveProjectRoots(t *testing.T) {
	cases := []struct {
		name      string
		templates []string
		want      []string
	}{
		{
			name:      "host owner repo",
			templates: []string{"~/src/{host}/{owner}/{repo}"},
			want:      []string{"~/src/*/*"},
		},
		{
			name:      "flat checkouts",
			templates: []string{"~/code/{repo}"},
			want:      []string{"~/code"},
		},
		{
			name:      "absolute path",
			templates: []string{"/opt/checkouts/{owner}/{repo}"},
			want:      []string{"/opt/checkouts/*"},
		},
		{
			name:      "several templates",
			templates: []string{"~/src/{host}/{owner}/{repo}", "~/work/{owner}/{repo}"},
			want:      []string{"~/src/*/*", "~/work/*"},
		},
		{
			// Two templates that differ only below {repo} describe one folder.
			name:      "deduplicated",
			templates: []string{"~/src/{owner}/{repo}", "~/src/{host}/{repo}"},
			want:      []string{"~/src/*"},
		},
		{
			// Nothing encloses the checkout, so there is no folder to scan.
			name:      "repo at the root",
			templates: []string{"{repo}"},
			want:      nil,
		},
		{
			// A template with no {repo} does not describe a checkout folder.
			name:      "no repo placeholder",
			templates: []string{"~/src/{owner}"},
			want:      nil,
		},
		{
			name:      "suffixed repo segment",
			templates: []string{"~/src/{owner}/{repo}.git"},
			want:      []string{"~/src/*"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveProjectRoots(tc.templates); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProjectsSectionOverridesDerivedRoots(t *testing.T) {
	dir := writeConfig(t, `
[repos]
templates = ["~/src/{host}/{owner}/{repo}"]

[projects]
roots = ["~/work", "~/play/*"]
git_only = false
depth = 3
`)

	got, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"~/work", "~/play/*"}
	if !reflect.DeepEqual(got.Projects.Roots, want) {
		t.Errorf("Roots = %v, want %v", got.Projects.Roots, want)
	}
	if got.Projects.GitOnly {
		t.Error("git_only = false should be honoured, not read as unset")
	}
	if got.Projects.Depth != 3 {
		t.Errorf("Depth = %d, want 3", got.Projects.Depth)
	}
}

// An empty roots list is the same as an absent one: fall back to the templates
// rather than leaving the picker with nothing to offer.
func TestEmptyProjectRootsFallsBackToTemplates(t *testing.T) {
	dir := writeConfig(t, `
[repos]
templates = ["~/elsewhere/{owner}/{repo}"]

[projects]
roots = []
`)

	got, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"~/elsewhere/*"}; !reflect.DeepEqual(got.Projects.Roots, want) {
		t.Errorf("Roots = %v, want %v", got.Projects.Roots, want)
	}
}

func TestProjectDepthOutOfRangeFallsBackAndComplains(t *testing.T) {
	cases := []struct {
		name  string
		depth string
		want  int
	}{
		{"below one", "0", defaultProjectDepth},
		{"negative", "-2", defaultProjectDepth},
		{"above the maximum", "99", maxProjectDepth},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := LoadFrom(writeConfig(t, "[projects]\ndepth = "+tc.depth+"\n"))
			if err != nil {
				t.Fatal(err)
			}
			if got.Projects.Depth != tc.want {
				t.Errorf("Depth = %d, want %d", got.Projects.Depth, tc.want)
			}
			if len(got.Problems) == 0 {
				t.Error("a bad depth should be reported, not silently ignored")
			}
			if !strings.Contains(strings.Join(got.Problems, " "), "projects.depth") {
				t.Errorf("Problems = %v, want one naming projects.depth", got.Problems)
			}
		})
	}
}

// Broken TOML must not leave the picker with no roots at all.
func TestProjectRootsSurviveABrokenFile(t *testing.T) {
	got, err := LoadFrom(writeConfig(t, "this is not = [valid toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Problems) == 0 {
		t.Error("expected the parse failure to be reported")
	}
	if len(got.Projects.Roots) == 0 {
		t.Error("expected roots derived from the default templates")
	}
}

func TestProjectPromptIsEmptyByDefault(t *testing.T) {
	got, err := LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.Prompts.Project != "" {
		t.Errorf("Project prompt = %q, want empty", got.Prompts.Project)
	}

	withPrompt, err := LoadFrom(writeConfig(t, "[agent.prompts]\nproject = \"in {{.Repo}}\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if withPrompt.Prompts.Project != "in {{.Repo}}" {
		t.Errorf("Project prompt = %q, want the configured one", withPrompt.Prompts.Project)
	}
}
