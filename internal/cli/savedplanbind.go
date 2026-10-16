package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/hazame-hub/alder/internal/apiclient"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/ldif"
	"github.com/hazame-hub/alder/internal/project"
)

// What a saved plan's changes are bound to.
//
// A project's saved plan names the project by a digest of its files, and an
// apply refuses files that no longer render to it. That binds the files, not
// the changes: the changes are whatever the plan file says. So an apply also
// checks, against the project as this environment holds it, that every saved
// change is one the project's desired state would make. Editing the file
// alone then changes nothing that runs; it is refused.
//
// A plan alder plan saved has no project: its file is the whole input, and
// the only check is that each modification expects what it changes. What
// vouches for such a file is who wrote it.

// expectsWhatItChanges refuses a modification whose expectation does not name
// every attribute it changes. An expectation is what makes a saved change
// apply only to the state it was reviewed against, and one that leaves an
// attribute out leaves that attribute's state unchecked. Names are compared
// as the directory's schema resolves them, as the expectation was written.
func expectsWhatItChanges(path string, changes []apiclient.ChangeRequest, names attributeNames) error {
	for i, c := range changes {
		if c.Type != apiclient.ChangeRequestTypeModify || c.Mods == nil {
			continue
		}
		expected := map[string]bool{}
		if c.Expect != nil {
			for _, a := range c.Expect.Attributes {
				expected[attributeKey(names, a.Name)] = true
			}
		}
		for _, m := range *c.Mods {
			if !expected[attributeKey(names, m.Name)] {
				return &ExitError{Code: ExitNotApplicable, Local: "saved_plan_invalid",
					Message: fmt.Sprintf("not applied: change %d in %s modifies %s of %s and does not expect what it holds: "+
						"a saved plan does not make a change like that", i+1, safe(path), safe(m.Name), safe(c.Dn))}
			}
		}
	}
	return nil
}

// attributeKey is one attribute description, whatever name or case it is
// written in: the name the directory returns, lowercased, options kept.
func attributeKey(names attributeNames, name string) string {
	base, options, _ := strings.Cut(name, ";")
	key := strings.ToLower(names.canonical(base))
	if options != "" {
		key += ";" + strings.ToLower(options)
	}
	return key
}

// bindToProject refuses a saved change the project, as the environment holds
// it, would not make. A project plans its entries as desired state: an entry
// the directory lacks is added exactly as the files describe it, and one it
// holds is reconciled into replacements of the attributes that differ, each
// with every value the files give it. Nothing else.
func bindToProject(path string, changes []apiclient.ChangeRequest, p *project.Project, names attributeNames) error {
	entries := map[string][]*ldif.Record{}
	for _, st := range p.Managed {
		for _, rec := range st.Records {
			key := strings.ToLower(rec.DN.String())
			entries[key] = append(entries[key], rec)
		}
	}
	lookup := func(name string) *ldif.Record {
		d, err := dn.Parse(name)
		if err != nil {
			return nil
		}
		for _, rec := range entries[strings.ToLower(d.String())] {
			if rec.DN.Equal(d) {
				return rec
			}
		}
		// The same DN spelled otherwise: in another case, or with spaces.
		for _, st := range p.Managed {
			for _, rec := range st.Records {
				if rec.DN.Equal(d) {
					return rec
				}
			}
		}
		return nil
	}
	for i, c := range changes {
		refuse := func(format string, a ...any) error {
			return &ExitError{Code: ExitNotApplicable, Local: "saved_plan_mismatch",
				Message: fmt.Sprintf("not applied: change %d in %s, %s of %s, is not one the project makes: ",
					i+1, safe(path), c.Type, safe(c.Dn)) + fmt.Sprintf(format, a...) +
					". A saved plan applies only what the project's files ask for; plan again and review"}
		}
		rec := lookup(c.Dn)
		if rec == nil {
			return refuse("the project does not describe that entry")
		}
		want := projectAttributes(rec, names)
		switch c.Type {
		case apiclient.ChangeRequestTypeAdd:
			if c.Attributes == nil {
				return refuse("it adds no attributes")
			}
			got := map[string][][]byte{}
			for _, a := range *c.Attributes {
				values, err := attributeBytes(a.Values)
				if err != nil {
					return refuse("%s", err)
				}
				key := attributeKey(names, a.Name)
				got[key] = append(got[key], values...)
			}
			if len(got) != len(want) {
				return refuse("it adds other attributes than the files give the entry")
			}
			for key, values := range got {
				if !sameValueSet(values, want[key].all) {
					return refuse("it adds other values than the files give the entry")
				}
			}
		case apiclient.ChangeRequestTypeModify:
			if c.Mods == nil || len(*c.Mods) == 0 {
				return refuse("it changes nothing")
			}
			for _, m := range *c.Mods {
				if m.Op != apiclient.ChangeModOpReplace {
					return refuse("it does not replace %s, and a project only ever replaces an attribute", safe(m.Name))
				}
				held, ok := want[attributeKey(names, m.Name)]
				if !ok {
					return refuse("the files do not name %s on that entry", safe(m.Name))
				}
				var values [][]byte
				if m.Values != nil {
					var err error
					if values, err = attributeBytes(*m.Values); err != nil {
						return refuse("%s", err)
					}
				}
				if !held.matches(values) {
					return refuse("it does not set %s to the values the files give it", safe(m.Name))
				}
			}
		default:
			return refuse("a project never makes a %s", c.Type)
		}
	}
	return nil
}

// projectValues are the values an entry's files give one attribute: under
// each name it is written with, and all of them together.
type projectValues struct {
	written [][][]byte
	all     [][]byte
}

// matches is whether values are what the files give the attribute, under
// one of its names or all of them: a reconcile replaces each name the files
// write, with that name's values.
func (v projectValues) matches(values [][]byte) bool {
	if sameValueSet(values, v.all) {
		return true
	}
	for _, w := range v.written {
		if sameValueSet(values, w) {
			return true
		}
	}
	return false
}

func projectAttributes(rec *ldif.Record, names attributeNames) map[string]projectValues {
	out := map[string]projectValues{}
	for _, a := range rec.Attrs {
		key := attributeKey(names, a.Name)
		v := out[key]
		v.written = append(v.written, a.Values)
		v.all = append(v.all, a.Values...)
		out[key] = v
	}
	return out
}

// attributeBytes decodes a change's values. A value with neither text nor
// base64 is one the server withheld, which a saved plan never holds.
func attributeBytes(values []apiclient.AttributeValue) ([][]byte, error) {
	out := make([][]byte, 0, len(values))
	for _, v := range values {
		switch {
		case v.Text != nil:
			out = append(out, []byte(*v.Text))
		case v.Base64 != nil:
			b, err := base64.StdEncoding.DecodeString(*v.Base64)
			if err != nil {
				return nil, fmt.Errorf("a value is not base64")
			}
			out = append(out, b)
		default:
			return nil, fmt.Errorf("a value is missing")
		}
	}
	return out, nil
}

// sameValueSet compares values byte for byte, as the sets they are: what a
// reconcile compares to decide that an attribute differs.
func sameValueSet(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	sorted := func(in [][]byte) [][]byte {
		out := append([][]byte{}, in...)
		sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i], out[j]) < 0 })
		return out
	}
	x, y := sorted(a), sorted(b)
	for i := range x {
		if !bytes.Equal(x[i], y[i]) {
			return false
		}
	}
	return true
}

// exactKeys refuses a JSON document holding, in any object, a key twice, or,
// where the document is read into a struct, a key that is not one of the
// struct's names spelled exactly. encoding/json would take the last of two
// keys, and match a key in any case.
func exactKeys(data []byte, t reflect.Type) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := walkKeys(dec, t, "the document"); err != nil {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) || errors.Is(err, io.ErrUnexpectedEOF) {
			// Said by the decoder, which says it better.
			return nil
		}
		return err
	}
	return nil
}

func walkKeys(dec *json.Decoder, t reflect.Type, at string) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '[':
		var elem reflect.Type
		if t != nil && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) {
			elem = t.Elem()
		}
		for i := 0; dec.More(); i++ {
			if err := walkKeys(dec, elem, fmt.Sprintf("%s[%d]", at, i)); err != nil {
				return err
			}
		}
	case '{':
		var fields map[string]reflect.Type
		if t != nil && t.Kind() == reflect.Struct {
			fields = jsonFields(t)
		}
		seen := map[string]bool{}
		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				return err
			}
			key, _ := tok.(string)
			if seen[key] {
				return fmt.Errorf("%s holds the key %q twice", at, key)
			}
			seen[key] = true
			var next reflect.Type
			switch {
			case fields != nil:
				f, known := fields[key]
				if !known {
					return fmt.Errorf("%s holds the key %q, which is not one this release reads (keys are read exactly as written)", at, key)
				}
				next = f
			case t != nil && t.Kind() == reflect.Map:
				next = t.Elem()
			}
			if err := walkKeys(dec, next, at+"."+key); err != nil {
				return err
			}
		}
	}
	_, err = dec.Token()
	return err
}

// jsonFields maps the keys a struct is read from, as its tags spell them, to
// their types.
func jsonFields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			if f.Anonymous && f.Type.Kind() == reflect.Struct {
				for k, v := range jsonFields(f.Type) {
					out[k] = v
				}
				continue
			}
			name = f.Name
		}
		out[name] = f.Type
	}
	return out
}
