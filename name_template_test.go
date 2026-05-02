package docker

import (
	"reflect"
	"testing"
	"text/template"
)

func mustParseNameTemplate(t *testing.T, body string) *template.Template {
	t.Helper()
	tmpl, err := parseNameTemplate(body)
	if err != nil {
		t.Fatalf("parseNameTemplate(%q): %v", body, err)
	}
	return tmpl
}

func TestRenderNameTemplates(t *testing.T) {
	tests := []struct {
		name      string
		templates []string
		labels    map[string]string
		want      []string
	}{
		{
			name:      "single template hits all referenced labels",
			templates: []string{`{{label "com.dokku.app-name"}}.{{label "com.dokku.process-type"}}`},
			labels:    map[string]string{"com.dokku.app-name": "docs", "com.dokku.process-type": "web"},
			want:      []string{"docs.web"},
		},
		{
			name:      "missing label aborts the template",
			templates: []string{`{{label "com.dokku.app-name"}}.{{label "com.dokku.process-type"}}`},
			labels:    map[string]string{"com.dokku.app-name": "docs"},
			want:      nil,
		},
		{
			name:      "empty label value treated as missing",
			templates: []string{`{{label "com.dokku.app-name"}}`},
			labels:    map[string]string{"com.dokku.app-name": "   "},
			want:      nil,
		},
		{
			name: "multiple templates compose independently",
			templates: []string{
				`{{label "com.dokku.app-name"}}.{{label "com.dokku.process-type"}}`,
				`{{label "com.dokku.app-name"}}`,
				`{{label "com.docker.compose.project"}}.{{label "com.docker.compose.service"}}`,
			},
			labels: map[string]string{
				"com.dokku.app-name":     "docs",
				"com.dokku.process-type": "web",
			},
			want: []string{"docs.web", "docs"},
		},
		{
			name:      "labelOr falls back when label missing",
			templates: []string{`{{labelOr "com.dokku.app-name" "anonymous"}}`},
			labels:    map[string]string{},
			want:      []string{"anonymous"},
		},
		{
			name:      "hasLabel gates a conditional",
			templates: []string{`{{if hasLabel "com.dokku.process-type"}}{{label "com.dokku.app-name"}}.{{label "com.dokku.process-type"}}{{else}}{{label "com.dokku.app-name"}}{{end}}`},
			labels:    map[string]string{"com.dokku.app-name": "docs"},
			want:      []string{"docs"},
		},
		{
			name:      "trailing whitespace trimmed",
			templates: []string{`  {{label "com.dokku.app-name"}}  `},
			labels:    map[string]string{"com.dokku.app-name": "docs"},
			want:      []string{"docs"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpls := make([]*template.Template, len(tc.templates))
			for i, body := range tc.templates {
				tmpls[i] = mustParseNameTemplate(t, body)
			}
			got := renderNameTemplates(tmpls, "container-name", "container-id", tc.labels)
			if len(got) == 0 {
				got = nil
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseNameTemplateRejectsEmpty(t *testing.T) {
	if _, err := parseNameTemplate(""); err == nil {
		t.Fatal("expected error for empty template")
	}
	if _, err := parseNameTemplate("   "); err == nil {
		t.Fatal("expected error for whitespace-only template")
	}
}

// TestShippedDefaultsAreSilentOnMissingLabels exercises the exact template
// bodies shipped in packaging/Corefile (after Caddyfile quote-unescaping)
// to make sure they render empty for unrelated containers instead of
// aborting and producing a debug log line per template per sync.
func TestShippedDefaultsAreSilentOnMissingLabels(t *testing.T) {
	shippedBodies := []string{
		`{{if and (hasLabel "com.dokku.app-name") (hasLabel "com.dokku.process-type")}}{{label "com.dokku.app-name"}}.{{label "com.dokku.process-type"}}{{end}}`,
		`{{if hasLabel "com.dokku.app-name"}}{{label "com.dokku.app-name"}}{{end}}`,
		`{{if and (hasLabel "com.docker.compose.project") (hasLabel "com.docker.compose.service")}}{{label "com.docker.compose.project"}}.{{label "com.docker.compose.service"}}{{end}}`,
	}

	tmpls := make([]*template.Template, len(shippedBodies))
	for i, body := range shippedBodies {
		tmpls[i] = mustParseNameTemplate(t, body)
	}

	cases := []struct {
		name   string
		labels map[string]string
		want   []string
	}{
		{
			name:   "container with no relevant labels renders nothing",
			labels: map[string]string{"unrelated": "value"},
			want:   nil,
		},
		{
			name:   "container with empty labels map renders nothing",
			labels: map[string]string{},
			want:   nil,
		},
		{
			name: "dokku web dyno collapses onto app.process and app",
			labels: map[string]string{
				"com.dokku.app-name":     "docs",
				"com.dokku.process-type": "web",
			},
			want: []string{"docs.web", "docs"},
		},
		{
			name: "dokku container with only app-name produces just the app name",
			labels: map[string]string{
				"com.dokku.app-name": "docs",
			},
			want: []string{"docs"},
		},
		{
			name: "compose container collapses onto project.service",
			labels: map[string]string{
				"com.docker.compose.project": "myproj",
				"com.docker.compose.service": "web",
			},
			want: []string{"myproj.web"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderNameTemplates(tmpls, "container-name", "container-id", tc.labels)
			if len(got) == 0 {
				got = nil
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
