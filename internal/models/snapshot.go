package models

import (
	"encoding/json"
	"reflect"
	"strings"
)

// Snapshot is a stored row as an undo snapshot (BL-68): its JSON with every field, including the
// ones its JSON tags leave out when empty (a date not set yet, a cleared link), so that restoring
// it puts back exactly that row, empty fields included. A field tagged "-" (a memory's vector) stays
// out. A nil row is the empty string.
func Snapshot(row interface{}) string {
	m := SnapshotMap(row)
	if m == nil {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

// OmitemptyFields are the JSON names of a model's fields that a plain json.Marshal leaves out when
// empty: what an undo snapshot written before Snapshot may lack (BL-68).
func OmitemptyFields(row interface{}) []string {
	t := reflect.TypeOf(row)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !f.IsExported() || name == "-" || !strings.Contains(opts, "omitempty") {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out = append(out, name)
	}
	return out
}

// SnapshotMap is Snapshot as a map, for a snapshot carried inside another (UndoAlso.Before).
func SnapshotMap(row interface{}) map[string]interface{} {
	v := reflect.ValueOf(row)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	b, err := json.Marshal(row)
	if err != nil {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil || m == nil {
		return nil
	}
	if v.Kind() != reflect.Struct {
		return m
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" || !strings.Contains(opts, "omitempty") {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if _, ok := m[name]; !ok {
			fv := v.Field(i)
			if fv.Kind() == reflect.Pointer && fv.IsNil() {
				m[name] = nil
			} else {
				m[name] = fv.Interface()
			}
		}
	}
	return m
}
