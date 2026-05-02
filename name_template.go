package docker

import (
	"fmt"
	"strings"
	"text/template"
)

// nameTemplateData is the data context passed to each name_from_labels
// template at execution time. Most operators reach for the `label`,
// `labelOr`, and `hasLabel` helpers because Docker label keys typically
// contain dots and cannot be reached via Go template field access.
type nameTemplateData struct {
	Name   string
	ID     string
	Labels map[string]string
}

// nameTemplateFuncs returns the funcmap stub used at parse time. The
// real implementations are bound per-execution via Clone()+Funcs() so
// each container can carry its own labels through the closure.
func nameTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"label":    func(string) (string, error) { return "", nil },
		"labelOr":  func(string, string) string { return "" },
		"hasLabel": func(string) bool { return false },
	}
}

// parseNameTemplate parses a Go text/template body for use with the
// name_from_labels Corefile directive.
func parseNameTemplate(body string) (*template.Template, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("template is empty")
	}
	return template.New("name_from_labels").Funcs(nameTemplateFuncs()).Parse(body)
}

// renderNameTemplates evaluates each parsed template against the given
// container metadata and returns the non-empty rendered names. A
// template that errors during execution (e.g., a `label` call hits a
// missing key) contributes no name; other templates and other name
// sources are unaffected.
func renderNameTemplates(templates []*template.Template, name, id string, labels map[string]string) []string {
	if len(templates) == 0 {
		return nil
	}
	out := make([]string, 0, len(templates))
	data := nameTemplateData{Name: name, ID: id, Labels: labels}
	for _, base := range templates {
		cloned, err := base.Clone()
		if err != nil {
			log.Debugf("name_from_labels template clone failed: %v", err)
			continue
		}
		cloned.Funcs(template.FuncMap{
			"label": func(k string) (string, error) {
				v := strings.TrimSpace(labels[k])
				if v == "" {
					return "", fmt.Errorf("label %q missing or empty", k)
				}
				return v, nil
			},
			"labelOr": func(k, def string) string {
				if v := strings.TrimSpace(labels[k]); v != "" {
					return v
				}
				return def
			},
			"hasLabel": func(k string) bool {
				return strings.TrimSpace(labels[k]) != ""
			},
		})
		var buf strings.Builder
		if err := cloned.Execute(&buf, data); err != nil {
			log.Debugf("name_from_labels template skipped: %v", err)
			continue
		}
		if rendered := strings.TrimSpace(buf.String()); rendered != "" {
			out = append(out, rendered)
		}
	}
	return out
}
