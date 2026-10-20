package ops

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
)

// Cut is a record cut down by --fields: id first whether named or not,
// then the fields named, in the record's order. Fields nil is every
// field.
type Cut[T any] struct {
	Record T
	Fields []string
}

// Has reports whether the cut prints a field.
func (c Cut[T]) Has(field string) bool {
	return c.Fields == nil || field == "id" || slices.Contains(c.Fields, field)
}

// MarshalJSON prints the record's fields that the cut keeps, in its
// struct's order, which is its schema's.
func (c Cut[T]) MarshalJSON() ([]byte, error) {
	if c.Fields == nil {
		return marshal(c.Record)
	}
	v := reflect.ValueOf(c.Record)
	var b bytes.Buffer
	b.WriteByte('{')
	for i := range v.NumField() {
		name := jsonName(v.Type().Field(i))
		if !c.Has(name) {
			continue
		}
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(name)
		val, err := marshal(v.Field(i).Interface())
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(val)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// FieldNames is a record's field names, in its schema's order.
func FieldNames(record any) []string {
	t := reflect.TypeOf(record)
	var out []string
	for i := range t.NumField() {
		out = append(out, jsonName(t.Field(i)))
	}
	return out
}

func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	return name
}
