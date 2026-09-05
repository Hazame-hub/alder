package ldapdriver

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-ldap/ldap/v3"

	"github.com/hazame-hub/alder/internal/directory"
)

// Finding out where, and whether, this session can write schema.
//
// There are two arrangements in the wild and the server says which it uses. A
// server that publishes a configContext generates its subschema subentry from
// configuration entries: the subentry is a read-only view, and a definition is
// added by modifying one of the configuration entries behind it. A server that
// publishes no configContext lets the subschema subentry be modified directly.
//
// Neither branch asks who the vendor is. The distinction is announced in the
// RootDSE, which is what makes it a capability rather than a guess -- and it is
// a real architectural difference, not a quirk: on one server the schema is
// configuration, on the other it is data.
//
// Announcing a location is not the same as being able to write it, so this also
// reads the target entries. A session bound to the data suffix of a server whose
// schema lives in its configuration tree usually cannot see that tree at all,
// and finding that out at connect time is what lets the UI say so plainly
// instead of offering a button that always fails.

// The attributes that carry definitions under each arrangement.
const (
	attrObjectClasses      = "objectClasses"
	attrAttributeTypes     = "attributeTypes"
	attrOLCObjectClasses   = "olcObjectClasses"
	attrOLCAttributeTypes  = "olcAttributeTypes"
	configSchemaRDNKeyword = "cn=schema,"
)

func (s *session) findSchemaTargets(ctx context.Context, caps directory.Capabilities) directory.SchemaWrite {
	if caps.SubschemaSubentry == "" {
		return directory.SchemaWrite{
			Style:       directory.SchemaStyleNone,
			Unavailable: "The server published no subschemaSubentry, so Alder cannot locate its schema at all.",
		}
	}
	if caps.ConfigContext != "" {
		return s.configSchemaTargets(ctx, caps)
	}
	return s.subschemaTarget(ctx, caps)
}

// subschemaTarget checks that the subschema subentry can be read, and offers it
// as the single place a definition is written.
//
// Being readable is not proof of being writable — only a write proves that, and
// a probe write to install and remove a definition is not something to do behind
// someone's back. What it does rule out is the case where the entry named in the
// RootDSE is not there.
func (s *session) subschemaTarget(ctx context.Context, caps directory.Capabilities) directory.SchemaWrite {
	req := ldap.NewSearchRequest(caps.SubschemaSubentry, ldap.ScopeBaseObject, ldap.NeverDerefAliases,
		0, int(s.timeout.Seconds()), false, "(objectClass=*)",
		[]string{attrObjectClasses, attrAttributeTypes}, nil)
	res, err := s.searchLocked(ctx, req)
	if err != nil || len(res.Entries) == 0 {
		return directory.SchemaWrite{
			Style: directory.SchemaStyleNone,
			Unavailable: fmt.Sprintf(
				"Alder could not read %s, the schema entry this server named, so it cannot offer to change it.",
				caps.SubschemaSubentry),
		}
	}
	e := res.Entries[0]
	return directory.SchemaWrite{
		Style:             directory.SchemaStyleSubschema,
		ObjectClassAttr:   attrObjectClasses,
		AttributeTypeAttr: attrAttributeTypes,
		Targets: []directory.SchemaTarget{{
			DN:             e.DN,
			Name:           "the server schema",
			ObjectClasses:  len(e.GetAttributeValues(attrObjectClasses)),
			AttributeTypes: len(e.GetAttributeValues(attrAttributeTypes)),
		}},
	}
}

// configSchemaTargets lists the configuration entries that hold schema.
//
// Each is a separate collection, and which one a new definition joins is a
// choice with consequences — they load in order, and a definition can only refer
// to something defined before it. Alder lists them and lets the person choose
// rather than picking one, because there is no defensible default.
func (s *session) configSchemaTargets(ctx context.Context, caps directory.Capabilities) directory.SchemaWrite {
	base := configSchemaRDNKeyword + caps.ConfigContext
	req := ldap.NewSearchRequest(base, ldap.ScopeSingleLevel, ldap.NeverDerefAliases,
		0, int(s.timeout.Seconds()), false, "(objectClass=*)",
		[]string{"cn", attrOLCObjectClasses, attrOLCAttributeTypes}, nil)
	res, err := s.searchLocked(ctx, req)
	if err != nil {
		// Overwhelmingly this is the ordinary case of a session bound to the
		// data suffix, which has no rights in the configuration tree and is
		// told the entry does not exist. Say what would change it.
		return directory.SchemaWrite{
			Style: directory.SchemaStyleNone,
			Unavailable: fmt.Sprintf(
				"This server keeps its schema in its configuration tree, under %s, and this session "+
					"cannot read it (%s). Connect with an account that has rights there to edit the schema.",
				base, err),
		}
	}
	if len(res.Entries) == 0 {
		return directory.SchemaWrite{
			Style: directory.SchemaStyleNone,
			Unavailable: fmt.Sprintf(
				"This server keeps its schema in its configuration tree, but %s holds no schema entries.", base),
		}
	}

	targets := make([]directory.SchemaTarget, 0, len(res.Entries))
	// The definitions are already in hand — they were read to be counted — so
	// noting which collection each one came from costs nothing beyond reading
	// the OID off the front of it.
	origin := map[string]string{}
	for _, e := range res.Entries {
		name := schemaEntryName(e)
		classes := e.GetAttributeValues(attrOLCObjectClasses)
		attrs := e.GetAttributeValues(attrOLCAttributeTypes)
		for _, def := range append(append([]string{}, classes...), attrs...) {
			if oid := definitionOID(def); oid != "" {
				origin[oid] = name
			}
		}
		targets = append(targets, directory.SchemaTarget{
			DN:             e.DN,
			Name:           name,
			ObjectClasses:  len(classes),
			AttributeTypes: len(attrs),
		})
	}
	return directory.SchemaWrite{
		Style:             directory.SchemaStyleConfig,
		ObjectClassAttr:   attrOLCObjectClasses,
		AttributeTypeAttr: attrOLCAttributeTypes,
		Targets:           targets,
		Origin:            origin,
	}
}

// schemaEntryName strips the ordering prefix a config entry's name carries.
//
// These entries are named "{4}alder": the number is the load order, maintained
// by the server, and showing it in a picker invites someone to think it is part
// of the name. The DN keeps it, because the DN is what the modification is
// addressed to.
func schemaEntryName(e *ldap.Entry) string {
	cn := e.GetAttributeValue("cn")
	if cn == "" {
		return e.DN
	}
	if strings.HasPrefix(cn, "{") {
		if end := strings.IndexByte(cn, '}'); end > 0 {
			return cn[end+1:]
		}
	}
	return cn
}

// definitionOID reads the numeric OID off the front of an RFC 4512 definition.
//
// Every one of them opens "( 1.3.6.1.4.1.… " and the OID is the first token, so
// this is a deliberate shortcut past the real parser: it is used only to say
// which collection a definition came from, it runs over every definition on
// every connect, and a definition it cannot read simply reports no origin.
//
// A stored definition may carry the position prefix its server maintains —
// "{4}( 1.2.3 …" — which is skipped here for the same reason it is skipped
// everywhere else: the prefix is the server's bookkeeping, not part of the
// definition.
func definitionOID(def string) string {
	def = strings.TrimSpace(def)
	if strings.HasPrefix(def, "{") {
		if end := strings.IndexByte(def, '}'); end > 0 {
			def = strings.TrimSpace(def[end+1:])
		}
	}
	def = strings.TrimSpace(strings.TrimPrefix(def, "("))
	end := strings.IndexAny(def, " \t")
	if end <= 0 {
		return ""
	}
	return def[:end]
}
