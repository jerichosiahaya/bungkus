// Package prompt builds message lists from templates, like Eino's ChatTemplate.
package prompt

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/jerichosiahaya/bungkus"
)

// placeholderRole marks a Placeholder; it never leaves Format.
const placeholderRole = "\x00placeholder"

// Placeholder marks where Format inserts the []bungkus.Message passed under
// name, e.g. the conversation history. A missing or nil value inserts nothing.
func Placeholder(name string) bungkus.Message {
	return bungkus.Message{Role: placeholderRole, Content: name}
}

// Template is a list of messages whose Content is a text/template.
type Template struct {
	msgs  []bungkus.Message
	tmpls []*template.Template // nil for placeholders
}

// New parses each message's Content as a text/template:
//
//	t, err := prompt.New(
//		bungkus.Message{Role: "system", Content: "You are a {{.role}}. Answer in {{.lang}}."},
//		prompt.Placeholder("history"),
//		bungkus.Message{Role: "user", Content: "{{.question}}"},
//	)
func New(msgs ...bungkus.Message) (*Template, error) {
	t := &Template{msgs: msgs, tmpls: make([]*template.Template, len(msgs))}
	for i, m := range msgs {
		if m.Role == placeholderRole {
			continue
		}
		tmpl, err := template.New(fmt.Sprint(i)).Option("missingkey=error").Parse(m.Content)
		if err != nil {
			return nil, fmt.Errorf("prompt: message %d: %w", i, err)
		}
		t.tmpls[i] = tmpl
	}
	return t, nil
}

// Must is like New but panics on a parse error, for templates in package variables.
func Must(t *Template, err error) *Template {
	if err != nil {
		panic(err)
	}
	return t
}

// Format renders the templates with vars and fills the placeholders.
// A variable a template uses but vars lacks is an error.
func (t *Template) Format(vars map[string]any) ([]bungkus.Message, error) {
	var out []bungkus.Message
	for i, m := range t.msgs {
		if m.Role == placeholderRole {
			switch v := vars[m.Content].(type) {
			case nil:
			case []bungkus.Message:
				out = append(out, v...)
			default:
				return nil, fmt.Errorf("prompt: placeholder %q needs []bungkus.Message, got %T", m.Content, v)
			}
			continue
		}
		var sb strings.Builder
		if err := t.tmpls[i].Execute(&sb, vars); err != nil {
			return nil, fmt.Errorf("prompt: message %d: %w", i, err)
		}
		m.Content = sb.String()
		out = append(out, m)
	}
	return out, nil
}
