package config

import (
	"bytes"
	"fmt"
	"text/template"
)

// TemplateVars is SPEC.md §5.2's shared variable set: one table, used by every
// adapter kind and every command template a lane's argv is built from.
type TemplateVars struct {
	Path, Main, Repo, Name, Branch, Base, Parent, Agent, Prompt, Image, Port string
	Env                                                                     map[string]string
}

// RenderArgv runs text/template over each argv element independently.
//
// There is no shell interpretation anywhere in this path: each element is
// rendered on its own and used as one argv slot exactly as written, so a
// branch or lane name containing spaces or shell metacharacters is just a
// template value, never re-parsed by a shell.
func RenderArgv(argv []string, vars TemplateVars) ([]string, error) {
	if len(argv) == 0 {
		return nil, nil
	}
	out := make([]string, len(argv))
	for i, a := range argv {
		tmpl, err := template.New("argv").Option("missingkey=zero").Parse(a)
		if err != nil {
			return nil, fmt.Errorf("argv[%d] %q: %w", i, a, err)
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, vars); err != nil {
			return nil, fmt.Errorf("argv[%d] %q: %w", i, a, err)
		}
		out[i] = buf.String()
	}
	return out, nil
}
