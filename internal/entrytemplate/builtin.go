package entrytemplate

// The ID ranges the built-ins allocate from: 10000 up to 60000, the
// UID_MAX and GID_MAX most Linux distributions put in login.defs, so a
// directory account never collides with a local system one. An operator whose
// directory numbers differently writes a template file with the same name.
const (
	builtinIDFrom = 10000
	builtinIDTo   = 60000
)

// Builtins are the templates every Alder offers, before any --templates.
//
// Only standards-track classes (RFC 2798 inetOrgPerson, RFC 2307 posixAccount
// and posixGroup, RFC 4519 groupOfNames), and each is offered only where the
// connected server's schema accepts it. posixGroup is the case that needs
// two: OpenLDAP defines it STRUCTURAL and 389 Directory Server AUXILIARY, so
// one server takes posixGroup alone and the other needs a structural class
// beside it. Both variants ship, hidden; each server sees the one it accepts.
func Builtins() []*Template {
	return []*Template{
		{
			Name:          "posix-user",
			Title:         "User",
			Description:   "A person with a login: inetOrgPerson and posixAccount, with the next free uidNumber.",
			View:          ViewUsers,
			RDN:           "uid",
			ObjectClasses: []string{"inetOrgPerson", "posixAccount"},
			Fields: []Field{
				{Name: "uid", Label: "User name", Required: true, Help: "The login name. It also names the entry: uid=<this>."},
				{Name: "givenName", Label: "First name"},
				{Name: "sn", Label: "Last name", Required: true},
				{Name: "mail", Label: "Email"},
				{Name: "gidNumber", Label: "Primary group ID", Required: true, Kind: FieldNumber,
					Help: "The gidNumber of the user's primary group."},
			},
			Attributes: map[string][]string{
				"uid":           {"{uid}"},
				"givenName":     {"{givenName}"},
				"sn":            {"{sn}"},
				"cn":            {"{givenName} {sn}"},
				"mail":          {"{mail}"},
				"gidNumber":     {"{gidNumber}"},
				"homeDirectory": {"/home/{uid}"},
				"loginShell":    {"/bin/bash"},
			},
			Allocate: map[string]Range{"uidNumber": {From: builtinIDFrom, To: builtinIDTo}},
			Source:   "built-in",
			Hidden:   true,
		},
		{
			Name:          "posix-group",
			Title:         "POSIX group",
			Description:   "A posixGroup with the next free gidNumber. Members are added by user name (memberUid) after it exists.",
			View:          ViewGroups,
			RDN:           "cn",
			ObjectClasses: []string{"posixGroup"},
			Fields: []Field{
				{Name: "cn", Label: "Group name", Required: true},
				{Name: "description", Label: "Description"},
			},
			Attributes: map[string][]string{
				"cn":          {"{cn}"},
				"description": {"{description}"},
			},
			Allocate: map[string]Range{"gidNumber": {From: builtinIDFrom, To: builtinIDTo}},
			Source:   "built-in",
			Hidden:   true,
		},
		{
			Name:          "posix-group-of-names",
			Title:         "POSIX group",
			Description:   "A groupOfNames that is also a posixGroup, with the next free gidNumber.",
			View:          ViewGroups,
			RDN:           "cn",
			ObjectClasses: []string{"groupOfNames", "posixGroup"},
			Fields: []Field{
				{Name: "cn", Label: "Group name", Required: true},
				{Name: "description", Label: "Description"},
				{Name: "member", Label: "First member", Kind: FieldDN,
					Help: "Some servers require a groupOfNames to have a member."},
			},
			Attributes: map[string][]string{
				"cn":          {"{cn}"},
				"description": {"{description}"},
				"member":      {"{member}"},
			},
			Allocate: map[string]Range{"gidNumber": {From: builtinIDFrom, To: builtinIDTo}},
			Source:   "built-in",
			Hidden:   true,
		},
		{
			Name:          "group-of-names",
			Title:         "Group",
			Description:   "A groupOfNames, whose members are entries named by DN.",
			View:          ViewGroups,
			RDN:           "cn",
			ObjectClasses: []string{"groupOfNames"},
			Fields: []Field{
				{Name: "cn", Label: "Group name", Required: true},
				{Name: "description", Label: "Description"},
				{Name: "member", Label: "First member", Required: true, Kind: FieldDN,
					Help: "A groupOfNames needs at least one member on most servers."},
			},
			Attributes: map[string][]string{
				"cn":          {"{cn}"},
				"description": {"{description}"},
				"member":      {"{member}"},
			},
			Source: "built-in",
			Hidden: true,
		},
	}
}
