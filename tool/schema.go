package tool

import (
	"encoding"
	"reflect"
	"strings"
)

var textMarshaler = reflect.TypeFor[encoding.TextMarshaler]()

// schemaOf returns the JSON Schema for values of t as encoding/json would
// marshal them. seen guards against recursive types, which become {}.
func schemaOf(t reflect.Type, seen map[reflect.Type]bool) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Implements(textMarshaler) || reflect.PointerTo(t).Implements(textMarshaler) {
		return map[string]any{"type": "string"} // e.g. time.Time
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string"} // []byte is base64
		}
		return map[string]any{"type": "array", "items": schemaOf(t.Elem(), seen)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaOf(t.Elem(), seen)}
	case reflect.Struct:
		if seen[t] {
			return map[string]any{}
		}
		seen[t] = true
		defer delete(seen, t)
		props := map[string]any{}
		required := []string{}
		addFields(t, props, &required, seen)
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}
	return map[string]any{} // interfaces and anything else: any value
}

func addFields(t reflect.Type, props map[string]any, required *[]string, seen map[reflect.Type]bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" && opts == "" {
			continue
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if f.Anonymous && name == "" && ft.Kind() == reflect.Struct {
			addFields(ft, props, required, seen) // embedded fields are promoted, as in encoding/json
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		s := schemaOf(f.Type, seen)
		if d := f.Tag.Get("desc"); d != "" {
			s["description"] = d
		}
		if e := f.Tag.Get("enum"); e != "" {
			s["enum"] = strings.Split(e, ",")
		}
		props[name] = s
		if !strings.Contains(opts, "omitempty") && f.Type.Kind() != reflect.Pointer {
			*required = append(*required, name)
		}
	}
}
