package policy

import (
	"reflect"
	"sort"
	"strings"
)

// Fields returns the wire names of the fields a PATCH body actually sets.
//
// Patch bodies use pointer fields: nil means absent, non-nil means "write
// this", including writing an explicit null. Deriving the set by reflection
// rather than by a hand-written switch is what makes the ownership check
// mechanical — a handler never names a field, so adding one to the table and
// to the struct is the whole change (BACKEND.md Phase 1 exit criteria).
//
// Non-pointer fields are ignored. They carry path and query parameters, which
// are not writes.
func Fields(patch any) []string {
	v := reflect.ValueOf(patch)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}

	var out []string
	collectFields(v, &out)
	sort.Strings(out)
	return out
}

func collectFields(v reflect.Value, out *[]string) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		fv := v.Field(i)

		// Flatten embedded structs so a shared set of patchable fields can be
		// composed.
		if sf.Anonymous && fv.Kind() == reflect.Struct {
			collectFields(fv, out)
			continue
		}

		name := wireName(sf)
		if name == "" {
			continue
		}
		if fv.Kind() != reflect.Pointer {
			continue
		}
		if !fv.IsNil() {
			*out = append(*out, name)
		}
	}
}

// PatchFieldNames returns every field a patch struct *could* set, regardless of
// what this instance holds. Used by tests to prove the struct and the ownership
// table agree.
func PatchFieldNames(patch any) []string {
	t := reflect.TypeOf(patch)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	collectFieldNames(t, &out)
	sort.Strings(out)
	return out
}

func collectFieldNames(t reflect.Type, out *[]string) {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		if sf.Anonymous && sf.Type.Kind() == reflect.Struct {
			collectFieldNames(sf.Type, out)
			continue
		}
		if sf.Type.Kind() != reflect.Pointer {
			continue
		}
		if name := wireName(sf); name != "" {
			*out = append(*out, name)
		}
	}
}

func wireName(sf reflect.StructField) string {
	tag, ok := sf.Tag.Lookup("json")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" || name == "" {
		return ""
	}
	return name
}

// UnmappedFields returns the patch struct's fields that have no rule in the
// ownership table for this kind.
//
// A field a request can set but the table has never heard of is a field written
// with no owner checked. The test that calls this is the reason that cannot
// happen quietly.
func UnmappedFields(kind ObjectKind, patch any) []string {
	obj, ok := table[kind]
	if !ok {
		return PatchFieldNames(patch)
	}
	if obj.allFields != nil {
		return nil
	}
	var out []string
	for _, name := range PatchFieldNames(patch) {
		if _, mapped := obj.fields[name]; !mapped {
			out = append(out, name)
		}
	}
	return out
}
