package api

import (
	"encoding/base64"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// ptr is the pointer-taking helper the generated optional fields need.
func ptr[T any](v T) *T { return &v }

// --- values -----------------------------------------------------------------

// encodeValue renders one attribute value for JSON.
//
// The decision is made from the bytes and the syntax together, not from either
// alone. A value that is not valid UTF-8 can never be text; a value belonging
// to a binary syntax is base64 even when its bytes happen to be readable,
// because a JPEG that starts with printable bytes is still a JPEG and the
// editor must not offer a text box for it.
func encodeValue(v []byte, kind schema.ValueKind) AttributeValue {
	size := len(v)
	if isBinaryKind(kind) || !utf8.Valid(v) || hasControlBytes(v) {
		return AttributeValue{Base64: ptr(base64.StdEncoding.EncodeToString(v)), Size: ptr(size)}
	}
	return AttributeValue{Text: ptr(string(v)), Size: ptr(size)}
}

func isBinaryKind(k schema.ValueKind) bool {
	switch k {
	case schema.KindBinary, schema.KindCertificate, schema.KindImage:
		return true
	}
	return false
}

// hasControlBytes reports bytes that would not survive a round trip through a
// JSON string cleanly enough to edit. Tab and newline are allowed, since a
// postal address legitimately contains them.
func hasControlBytes(v []byte) bool {
	for _, c := range v {
		if c < 0x20 && c != '\t' && c != '\n' && c != '\r' {
			return true
		}
	}
	return false
}

// decodeValue reads one attribute value from JSON. Exactly one of the two forms
// must be present; a value carrying both is a client bug worth reporting rather
// than guessing about.
func decodeValue(v AttributeValue) ([]byte, error) {
	switch {
	case v.Base64 != nil && v.Text != nil:
		return nil, fmt.Errorf("a value carries both text and base64; it must carry exactly one")
	case v.Base64 != nil:
		b, err := base64.StdEncoding.DecodeString(*v.Base64)
		if err != nil {
			return nil, fmt.Errorf("a value is not valid base64: %w", err)
		}
		return b, nil
	case v.Text != nil:
		return []byte(*v.Text), nil
	default:
		// An explicitly empty value is legal in LDAP and is not the same as an
		// absent one.
		return []byte{}, nil
	}
}

func decodeValues(values []AttributeValue) ([][]byte, error) {
	out := make([][]byte, 0, len(values))
	for i, v := range values {
		b, err := decodeValue(v)
		if err != nil {
			return nil, fmt.Errorf("value %d: %w", i+1, err)
		}
		out = append(out, b)
	}
	return out, nil
}

// --- schema kinds -----------------------------------------------------------

func attributeKind(k schema.AttributeKind) AttributeKind {
	out := AttributeKind{
		Name:        k.Name,
		Kind:        AttributeKindKind(k.Kind),
		Known:       k.Known,
		SingleValue: ptr(k.SingleValue),
		ReadOnly:    ptr(k.ReadOnly),
		Operational: ptr(k.Operational),
		Sensitive:   ptr(k.Sensitive),
	}
	if k.OID != "" {
		out.Oid = ptr(k.OID)
	}
	if k.Desc != "" {
		out.Desc = ptr(k.Desc)
	}
	if k.Syntax != "" {
		out.Syntax = ptr(k.Syntax)
		out.SyntaxLabel = ptr(k.SyntaxLabel)
	}
	if k.MaxLength > 0 {
		out.MaxLength = ptr(k.MaxLength)
	}
	if k.Obsolete {
		out.Obsolete = ptr(true)
	}
	return out
}

// --- entries ----------------------------------------------------------------

// entryAttributes renders an entry's attributes, annotated from the schema and
// ordered for a human: objectClass first, then the required attributes, then
// the rest alphabetically, with the server-owned operational ones last.
//
// The order is deliberate. A directory returns attributes in whatever order it
// pleases, and an editor that renders that order makes the same entry look
// different on two servers.
func entryAttributes(e *directory.Entry, sch *schema.Schema, req schema.AttributeRequirements) []EntryAttribute {
	required := map[string]bool{}
	for _, name := range req.Must {
		required[foldName(name)] = true
	}

	out := make([]EntryAttribute, 0, len(e.Order))
	for _, name := range e.Order {
		kind := sch.KindOf(name)
		values := e.Attributes[name]

		attr := EntryAttribute{
			Name:       name,
			Kind:       attributeKind(kind),
			Required:   ptr(required[foldName(name)]),
			ValueCount: ptr(len(values)),
		}
		if kind.Sensitive {
			// The count is reported so the UI can say "set" without ever
			// carrying the hash to the browser.
			attr.Withheld = ptr(true)
			attr.Values = []AttributeValue{}
		} else {
			attr.Values = make([]AttributeValue, 0, len(values))
			for _, v := range values {
				attr.Values = append(attr.Values, encodeValue(v, kind.Kind))
			}
		}
		out = append(out, attr)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return attributeRank(out[i], required) < attributeRank(out[j], required)
	})
	return out
}

// attributeRank orders attributes into bands; within a band the stable sort
// keeps the alphabetical order established below.
func attributeRank(a EntryAttribute, required map[string]bool) int {
	switch {
	case foldName(a.Name) == "objectclass":
		return 0
	case a.Kind.Operational != nil && *a.Kind.Operational:
		return 3
	case required[foldName(a.Name)]:
		return 1
	default:
		return 2
	}
}

func foldName(s string) string {
	base := schema.BaseName(s)
	out := make([]byte, len(base))
	for i := 0; i < len(base); i++ {
		c := base[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

func requirementsView(req schema.AttributeRequirements) Requirements {
	out := Requirements{
		Must: ptr(req.Must),
		May:  ptr(req.May),
	}
	if req.Structural != nil {
		out.Structural = ptr(req.Structural.Name())
	}
	if len(req.Unknown) > 0 {
		out.Unknown = ptr(req.Unknown)
	}
	return out
}

// --- capabilities -----------------------------------------------------------

func capabilitiesView(c directory.Capabilities) Capabilities {
	return Capabilities{
		NamingContexts:          c.NamingContexts,
		SubschemaSubentry:       c.SubschemaSubentry,
		SupportedControls:       ptr(c.SupportedControls),
		SupportedExtensions:     ptr(c.SupportedExtensions),
		SupportedSaslMechanisms: ptr(c.SupportedSASLMechs),
		Paging:                  c.Paging,
		ServerSort:              ptr(c.ServerSort),
		WhoAmI:                  ptr(c.WhoAmI),
		// The editor reads this to decide whether to offer a password control
		// at all, rather than offering one that can only fail.
		PasswordModify: ptr(c.PasswordModify),
		ConfigContext:  ptrIfSet(c.ConfigContext),
		// Likewise: the schema browser offers editing only where there is
		// somewhere to write, and says why when there is not.
		SchemaWrite: ptr(schemaWriteView(c.SchemaWrite)),
		Config: ptr(ConfigAccess{
			Dn:           ptrIfSet(c.Config.DN),
			Readable:     c.Config.Readable,
			SeparateBind: c.Config.SeparateBind,
			BoundAs:      ptrIfSet(c.Config.BoundAs),
			Reason:       ptrIfSet(c.Config.Reason),
		}),
	}
}

func schemaWriteView(w directory.SchemaWrite) SchemaWrite {
	out := SchemaWrite{
		Style:             SchemaWriteStyle(w.Style),
		ObjectClassAttr:   ptrIfSet(w.ObjectClassAttr),
		AttributeTypeAttr: ptrIfSet(w.AttributeTypeAttr),
		Unavailable:       ptrIfSet(w.Unavailable),
	}
	if len(w.Targets) > 0 {
		targets := make([]SchemaTarget, 0, len(w.Targets))
		for _, t := range w.Targets {
			targets = append(targets, SchemaTarget{
				Dn:             t.DN,
				Name:           t.Name,
				ObjectClasses:  t.ObjectClasses,
				AttributeTypes: t.AttributeTypes,
			})
		}
		out.Targets = &targets
	}
	return out
}

// ptrIfSet omits an empty string rather than sending one, so a field that means
// "there is nothing to say here" is absent instead of present and blank.
func ptrIfSet(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// --- tree -------------------------------------------------------------------

func treeNode(e *directory.Entry, sch *schema.Schema, hasChildren, isRoot bool) TreeNode {
	node := TreeNode{
		Dn:          e.DN.String(),
		Rdn:         rdnLabel(e.DN),
		HasChildren: hasChildren,
	}
	classes := e.ObjectClasses()
	if len(classes) > 0 {
		node.ObjectClasses = ptr(classes)
		if sch != nil {
			if req := sch.Requirements(classes); req.Structural != nil {
				node.Structural = ptr(req.Structural.Name())
			}
		}
	}
	if isRoot {
		node.IsNamingContext = ptr(true)
	}
	return node
}

// rdnLabel renders the leftmost RDN, which is the tree's label for a node.
func rdnLabel(d dn.DN) string {
	if len(d) == 0 {
		return ""
	}
	return d.RDN().String()
}

// --- change records ---------------------------------------------------------

// changeRecord converts a ChangeRequest from the wire into the domain type.
//
// Everything user-supplied is parsed here: the DN through the dn package, the
// values out of base64, the operation names against a closed set. A request
// that does not survive this function never reaches the directory.
func changeRecord(req ChangeRequest) (directory.ChangeRecord, error) {
	target, err := dn.Parse(req.Dn)
	if err != nil {
		return directory.ChangeRecord{}, fmt.Errorf("dn: %w", err)
	}
	out := directory.ChangeRecord{DN: target}

	switch req.Type {
	case ChangeRequestTypeAdd:
		out.Type = directory.ChangeAdd
		if req.Attributes == nil {
			return out, fmt.Errorf("an add requires attributes")
		}
		for _, a := range *req.Attributes {
			values, valErr := decodeValues(a.Values)
			if valErr != nil {
				return out, fmt.Errorf("%s: %w", a.Name, valErr)
			}
			out.Attrs = append(out.Attrs, directory.Attribute{Name: a.Name, Values: values})
		}
	case ChangeRequestTypeModify:
		out.Type = directory.ChangeModify
		if req.Mods == nil {
			return out, fmt.Errorf("a modify requires modifications")
		}
		for _, m := range *req.Mods {
			var values [][]byte
			if m.Values != nil {
				values, err = decodeValues(*m.Values)
				if err != nil {
					return out, fmt.Errorf("%s: %w", m.Name, err)
				}
			}
			op, opErr := modOp(m.Op)
			if opErr != nil {
				return out, opErr
			}
			out.Mods = append(out.Mods, directory.Mod{Op: op, Name: m.Name, Values: values})
		}
	case ChangeRequestTypeDelete:
		out.Type = directory.ChangeDelete
	case ChangeRequestTypeSetpassword:
		out.Type = directory.ChangeSetPassword
		if req.NewPassword == nil || *req.NewPassword == "" {
			return out, fmt.Errorf("a password change requires newPassword")
		}
		out.NewPassword = *req.NewPassword
	case ChangeRequestTypeModrdn:
		out.Type = directory.ChangeModRDN
		if req.NewRdn == nil || *req.NewRdn == "" {
			return out, fmt.Errorf("a rename requires newRdn")
		}
		out.NewRDN = *req.NewRdn
		if req.DeleteOldRdn != nil {
			out.DeleteOldRDN = *req.DeleteOldRdn
		}
		if req.NewSuperior != nil && *req.NewSuperior != "" {
			sup, supErr := dn.Parse(*req.NewSuperior)
			if supErr != nil {
				return out, fmt.Errorf("newSuperior: %w", supErr)
			}
			out.NewSuperior = sup
		}
	default:
		return out, fmt.Errorf("unknown change type %q", req.Type)
	}
	return out, nil
}

func modOp(op ChangeModOp) (directory.ModOp, error) {
	switch op {
	case ChangeModOpAdd:
		return directory.ModAdd, nil
	case ChangeModOpDelete:
		return directory.ModDelete, nil
	case ChangeModOpReplace:
		return directory.ModReplace, nil
	default:
		return "", fmt.Errorf("unknown modification %q", op)
	}
}

// changeRequest is the inverse: a domain record rendered back onto the wire
// type, so an imported LDIF can be posted straight to /changes/apply.
func changeRequest(c directory.ChangeRecord) ChangeRequest {
	out := ChangeRequest{Dn: c.DN.String()}
	switch c.Type {
	case directory.ChangeAdd:
		out.Type = ChangeRequestTypeAdd
		attrs := make([]ChangeAttribute, 0, len(c.Attrs))
		for _, a := range c.Attrs {
			attrs = append(attrs, ChangeAttribute{Name: a.Name, Values: encodeRawValues(a.Values)})
		}
		out.Attributes = ptr(attrs)
	case directory.ChangeModify:
		out.Type = ChangeRequestTypeModify
		mods := make([]ChangeMod, 0, len(c.Mods))
		for _, m := range c.Mods {
			mod := ChangeMod{Name: m.Name, Op: ChangeModOp(m.Op)}
			if len(m.Values) > 0 {
				mod.Values = ptr(encodeRawValues(m.Values))
			}
			mods = append(mods, mod)
		}
		out.Mods = ptr(mods)
	case directory.ChangeDelete:
		out.Type = ChangeRequestTypeDelete
	case directory.ChangeSetPassword:
		// NewPassword is deliberately not copied. This direction produces the
		// request an imported document would post back, and a password that
		// came in from an LDIF is not something to hand out again.
		out.Type = ChangeRequestTypeSetpassword
	case directory.ChangeModRDN:
		out.Type = ChangeRequestTypeModrdn
		out.NewRdn = ptr(c.NewRDN)
		out.DeleteOldRdn = ptr(c.DeleteOldRDN)
		if len(c.NewSuperior) > 0 {
			out.NewSuperior = ptr(c.NewSuperior.String())
		}
	}
	return out
}

// encodeRawValues renders values with no schema to consult, so the decision is
// made from the bytes alone.
func encodeRawValues(values [][]byte) []AttributeValue {
	out := make([]AttributeValue, 0, len(values))
	for _, v := range values {
		if !utf8.Valid(v) || hasControlBytes(v) {
			out = append(out, AttributeValue{
				Base64: ptr(base64.StdEncoding.EncodeToString(v)),
				Size:   ptr(len(v)),
			})
			continue
		}
		out = append(out, AttributeValue{Text: ptr(string(v)), Size: ptr(len(v))})
	}
	return out
}

// --- scope ------------------------------------------------------------------

func searchScope(s SearchRequestScope) (directory.Scope, error) {
	return directory.ParseScope(string(s))
}
