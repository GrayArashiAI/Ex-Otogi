// Package settingstest checks a module's settings against their schema in
// tests: the defaults the settings form shows (the fields' default tags)
// must be the defaults the module applies to fields left unset.
package settingstest

import (
	"encoding/json"
	"reflect"
	"testing"
)

// WithDefaults returns a copy of cfg with every unset field that has a
// default tag set to that default, in nested structs, in slices of structs,
// and behind set pointers too, as an operator who typed in every default the
// form shows would. A nil pointer stays nil: that part was left out. cfg
// itself keeps its unset fields, for the test to compare the two.
func WithDefaults[C any](t *testing.T, cfg C) C {
	t.Helper()
	v := reflect.ValueOf(&cfg).Elem()
	fill(t, v)
	return cfg
}

func fill(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.Struct:
		for i := range v.NumField() {
			f, field := v.Field(i), v.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			if def, ok := field.Tag.Lookup("default"); ok && f.IsZero() {
				if err := json.Unmarshal(defaultJSON(def, f.Type()), f.Addr().Interface()); err != nil {
					t.Fatalf("%s: default %q: %v", field.Name, def, err)
				}
				continue
			}
			fill(t, f)
		}
	case reflect.Slice:
		if v.IsNil() {
			return
		}
		own := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		reflect.Copy(own, v)
		v.Set(own)
		for i := range v.Len() {
			fill(t, v.Index(i))
		}
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		own := reflect.New(v.Type().Elem())
		own.Elem().Set(v.Elem())
		v.Set(own)
		fill(t, own.Elem())
	}
}

// defaultJSON writes a default tag as JSON for its field: a string default
// (a duration such as 5m included) is quoted; numbers, booleans, and lists
// are JSON already.
func defaultJSON(def string, typ reflect.Type) []byte {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() == reflect.String || typ.Kind() == reflect.Int64 && typ.Name() == "Duration" {
		b, _ := json.Marshal(def)
		return b
	}
	return []byte(def)
}
