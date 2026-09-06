package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

// A field the driver fills in and the wire drops is invisible, and invisible in
// the worst way: the back end is correct, the tests are green, and the feature
// simply does not appear. That has happened three times in this codebase, each
// time costing a debugging session that started by looking in the wrong half.
//
// capabilitiesView is a hand-written mapping, so nothing makes it keep up with
// the struct it maps. This is that nothing, made into something: every field of
// directory.Capabilities either changes the rendered view, or is named below
// with the reason it does not.

// notOnTheWire lists the fields that deliberately stay server-side, and why.
//
// Adding a name here is a decision to make something invisible to the UI, which
// is exactly the decision worth writing down.
var notOnTheWire = map[string]string{
	"VendorName": "carried on SessionInfo instead, which is where the UI reads it; " +
		"it is display-only and never branched on",
	"VendorVersion": "as VendorName",
	"StartTLS": "answered before a session exists — the connection screen chooses " +
		"the transport, and by the time this is known the choice has been made",
	"AllOperional": "decides what the server itself asks for when reading an entry; " +
		"the browser never needs to know how the attributes were requested",
	"SupportedLDAPVersion": "every server Alder targets answers 3, so it " +
		"distinguishes nothing and would be a row of trivia",
}

func TestEveryCapabilityEitherCrossesTheWireOrSaysWhyNot(t *testing.T) {
	typ := reflect.TypeOf(directory.Capabilities{})
	base := render(t, directory.Capabilities{})

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}

		probe := directory.Capabilities{}
		if !setProbeValue(reflect.ValueOf(&probe).Elem().Field(i)) {
			t.Fatalf("%s: this test does not know how to set a %s. Teach it, "+
				"rather than deleting the case — an unset field looks mapped.",
				field.Name, field.Type)
		}

		crosses := render(t, probe) != base
		reason, excused := notOnTheWire[field.Name]

		switch {
		case crosses && excused:
			t.Errorf("Capabilities.%s reaches the browser, but notOnTheWire still "+
				"claims it does not (%q). Remove the entry.", field.Name, reason)
		case !crosses && !excused:
			t.Errorf("Capabilities.%s never reaches the browser. capabilitiesView "+
				"in convert.go does not map it, and nothing else will notice. Map "+
				"it, or add it to notOnTheWire with the reason.", field.Name)
		}
	}
}

// The allowlist must not outlive the fields it excuses, or it silently starts
// excusing nothing while looking like it excuses something.
func TestNotOnTheWireNamesRealFields(t *testing.T) {
	typ := reflect.TypeOf(directory.Capabilities{})
	for name := range notOnTheWire {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("notOnTheWire names %q, which is not a Capabilities field any "+
				"more. Delete the entry.", name)
		}
	}
}

func render(t *testing.T, c directory.Capabilities) string {
	t.Helper()
	b, err := json.Marshal(capabilitiesView(c))
	if err != nil {
		t.Fatalf("marshalling the view: %v", err)
	}
	return string(b)
}

// setProbeValue puts a distinctive non-zero value into one field, so that a
// field which does cross the wire changes the rendered JSON and one which does
// not leaves it identical.
func setProbeValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		v.SetString("alder-probe")
		return true
	case reflect.Bool:
		v.SetBool(true)
		return true
	case reflect.Slice:
		if v.Type().Elem().Kind() != reflect.String {
			return false
		}
		v.Set(reflect.ValueOf([]string{"alder-probe"}))
		return true
	case reflect.Struct:
		// A nested struct counts as reaching the wire if any one of its own
		// fields does, so setting the first one this function understands is
		// enough to tell.
		for i := 0; i < v.NumField(); i++ {
			if !v.Type().Field(i).IsExported() {
				continue
			}
			if setProbeValue(v.Field(i)) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
