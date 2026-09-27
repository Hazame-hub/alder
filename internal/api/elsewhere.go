package api

// What the general entry editor does not offer, and why.
//
// Two different questions decide whether a field appears in the editor, and
// conflating them cost this product both a missing feature and a scope
// violation in the same screen.
//
// The first is the server's: NO-USER-MODIFICATION says the directory owns the
// value and refuses to let a client write it. That is read from the schema
// and is not a matter of opinion.
//
// The second is Alder's own, and it is this. A handful of attributes are
// writable, offered by the schema, and still have no business in a text box
// beside "telephoneNumber" -- either because writing them is out of scope for
// v1, or because they have a purpose-built editor that writes them one
// definition at a time. Until 1.30 there was no way to say that, so the
// editor said nothing: `aci` was offered on any entry that lacked one, and
// `olcAccess` -- an ordinary attribute, so never filtered at all -- was four
// editable text boxes on a database entry, `{0}` ordering prefixes and all.
//
// The reason travels with the attribute so the viewer can print it. An
// attribute that is shown and not editable, with no sentence saying why, is
// the same dead end the audit found around nsAccountLock -- only the other way
// round.

// editedElsewhere returns why the entry editor does not offer this attribute,
// or "" when it does.
//
// By name, which is normally the wrong way to decide anything here: section 3
// of the charter says branch on capabilities, not on the vendor. That rule is
// about detecting what a server can do. This is not that -- it is a statement
// about what Alder chooses to write, and the two attributes that carry an
// access rule are named in the charter itself. A directory that spells its
// access rules some third way is not covered, and that is honest: Alder does
// not claim to have recognised a rule it has never heard of.
func editedElsewhere(name string) string {
	switch foldName(name) {
	case "aci", "olcaccess":
		// Reading them is 1.19; writing them is out of scope and says so in
		// the decisions log. The reason is not squeamishness: an access rule
		// is the one change that can lock every administrator out of the
		// directory, both servers evaluate rules first-match-wins, and
		// olcAccess carries an explicit ordering prefix that a text box
		// invites somebody to retype. A correct-looking edit to one rule
		// changes the meaning of every rule after it.
		return "Alder reads access rules and does not write them. The Access panel shows what this holds."
	case "objectclasses", "attributetypes", "ldapsyntaxes", "matchingrules",
		"matchingruleuse", "ditcontentrules", "ditstructurerules", "nameforms",
		"ldapschemas":
		// The subschema entry carries a thousand of these on 389 DS. A schema
		// change is an add or a delete of one definition; a replace of the
		// whole attribute is not a schema edit, it is a schema replacement,
		// and no text box should be able to express it by accident.
		return "the schema editor writes these, one definition at a time"
	}
	return ""
}
