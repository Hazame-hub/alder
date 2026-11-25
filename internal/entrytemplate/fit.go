package entrytemplate

import (
	"fmt"
	"strings"

	"github.com/hazame-hub/alder/internal/schema"
)

// Problems says why a template cannot create an entry on a server with this
// schema, or nothing when it can.
//
// The check is the one the server would make, made first so that "New user"
// offers only what will be accepted: every class defined, exactly one
// structural class, every required attribute set, and nothing set that the
// classes do not permit. A required attribute counts as set only when the
// template always gives it a value -- a constant, a required field or an
// allocated number -- because an optional field left blank leaves it out.
func (t *Template) Problems(sch *schema.Schema) []string {
	if sch == nil || sch.IsEmpty() {
		return []string{"this server's schema could not be read, so the template cannot be checked against it"}
	}
	var out []string
	req := sch.Requirements(t.ObjectClasses)
	for _, u := range req.Unknown {
		out = append(out, fmt.Sprintf("this server defines no object class %s", u))
	}
	if len(req.Unknown) > 0 {
		return out
	}
	if req.Structural == nil {
		out = append(out, "its object classes have no single structural class on this server")
	}

	permitted := map[string]bool{"objectclass": true}
	for _, n := range append(append([]string{}, req.Must...), req.May...) {
		permitted[strings.ToLower(n)] = true
	}
	for _, n := range t.AttributeNames() {
		if !permitted[strings.ToLower(sch.CanonicalAttrName(n))] {
			out = append(out, fmt.Sprintf("%s is not permitted by its object classes here", n))
		}
	}
	for _, n := range req.Must {
		if strings.EqualFold(n, "objectClass") {
			continue
		}
		if !t.alwaysSets(sch, n) {
			out = append(out, fmt.Sprintf("%s is required by its object classes and the template does not always set it", n))
		}
	}
	return out
}

// alwaysSets reports whether every rendering gives attr a value.
func (t *Template) alwaysSets(sch *schema.Schema, attr string) bool {
	canonical := strings.ToLower(sch.CanonicalAttrName(attr))
	for name := range t.Allocate {
		if strings.ToLower(sch.CanonicalAttrName(name)) == canonical {
			return true
		}
	}
	for name, values := range t.Attributes {
		if strings.ToLower(sch.CanonicalAttrName(name)) != canonical {
			continue
		}
		for _, v := range values {
			if t.valueAlwaysNonEmpty(v) {
				return true
			}
		}
	}
	return false
}

// valueAlwaysNonEmpty: a constant part, or a placeholder for a required field
// or an allocated number, guarantees text.
func (t *Template) valueAlwaysNonEmpty(v string) bool {
	if strings.TrimSpace(placeholderPattern.ReplaceAllString(v, "")) != "" {
		return true
	}
	for _, m := range placeholderPattern.FindAllStringSubmatch(v, -1) {
		if _, ok := lookupFold(t.Allocate, m[1]); ok {
			return true
		}
		for _, f := range t.Fields {
			if strings.EqualFold(f.Name, m[1]) && (f.Required || f.Default != "") {
				return true
			}
		}
	}
	return false
}
