package access

import (
	"regexp"
	"strings"
)

// Reading aci.
//
// A 389 Directory Server aci is one string on an entry:
//
//	(targetattr="mail")(version 3.0; acl "a name"; allow (read,search) userdn = "ldap:///anyone";)
//
// The leading parenthesised clauses say what it is about; the version block
// says what it does and to whom. An aci on an entry bears on that entry and
// everything beneath it, which is why this reads the entry's ancestors too.
//
// As with olcAccess, what is parsed is the common shape, the raw value is
// always kept, and anything unrecognised is reported whole rather than guessed
// at: this is the mechanism that decides who can read a password hash.

var (
	aciName    = regexp.MustCompile(`(?i)\bacl\s+"([^"]*)"`)
	aciTarget  = regexp.MustCompile(`(?i)\(\s*(target|targetattr|targetfilter|targetscope|targattrfilters|targetcontrol|targetattrfilters)\s*(!?=)\s*"([^"]*)"\s*\)`)
	aciRights  = regexp.MustCompile(`(?i)\b(allow|deny)\s*\(([^)]*)\)`)
	aciSubject = regexp.MustCompile(`(?i)\b(userdn|groupdn|roledn|userattr|groupattr|dns|ip|authmethod|dayofweek|timeofday)\s*(!?=)\s*"([^"]*)"`)
)

// ParseACI reads one aci value from an entry.
//
// source is the entry the aci is written on, target the entry asked about; an
// aci found on an ancestor is marked inherited, because that is the fact an
// operator needs to see before going looking for it on the wrong entry.
func ParseACI(value, source, target string) Rule {
	rule := Rule{
		Style: StyleACI, Source: source, Raw: value,
		Inherited: !dnEqual(source, target),
		Applies:   AppliesYes,
	}
	if m := aciName.FindStringSubmatch(value); m != nil {
		rule.Name = m[1]
	}

	t := &Target{}
	scopeGiven := ""
	for _, m := range aciTarget.FindAllStringSubmatch(value, -1) {
		keyword, op, body := strings.ToLower(m[1]), m[2], m[3]
		switch keyword {
		case "target":
			t.DN = strings.TrimPrefix(body, "ldap:///")
		case "targetattr":
			for _, a := range strings.Split(body, "||") {
				a = strings.TrimSpace(a)
				if a == "" {
					continue
				}
				if op == "!=" {
					a = "!" + a
				}
				t.Attributes = append(t.Attributes, a)
			}
		case "targetfilter":
			t.Filter = body
		case "targetscope":
			scopeGiven = strings.ToLower(body)
		}
	}
	switch {
	case scopeGiven != "":
		t.Scope = scopeGiven
	case t.DN != "":
		t.Scope = "subtree"
	default:
		t.Scope = "subtree"
	}
	rule.Target = t

	var grants []Grant
	for _, m := range aciRights.FindAllStringSubmatch(value, -1) {
		kind := KindAllow
		if strings.EqualFold(m[1], "deny") {
			kind = KindDeny
		}
		rights := strings.Join(strings.Fields(strings.ReplaceAll(m[2], ",", " ")), ",")
		grants = append(grants, Grant{Kind: kind, Access: rights})
	}
	subjects := aciSubject.FindAllStringSubmatch(value, -1)
	for i := range grants {
		if i < len(subjects) {
			grants[i].Subject = subjectText(subjects[i][1], subjects[i][2], subjects[i][3])
		} else if len(subjects) == 1 {
			// One subject and several rights clauses: the subject is theirs.
			grants[i].Subject = subjectText(subjects[0][1], subjects[0][2], subjects[0][3])
		}
	}
	rule.Grants = grants
	rule.Parsed = len(grants) > 0 && strings.Contains(strings.ToLower(value), "version 3.0")
	if !rule.Parsed {
		rule.Why = "Alder did not read this rule's structure; it is shown as the server holds it"
	}

	rule.Applies, rule.Why = aciApplies(rule, t, target, rule.Why)
	return rule
}

// subjectText is who a clause names, in the rule's own words.
func subjectText(keyword, op, body string) string {
	who := strings.TrimPrefix(body, "ldap:///")
	if op == "!=" {
		return "not " + who
	}
	if strings.EqualFold(keyword, "userdn") || strings.EqualFold(keyword, "groupdn") ||
		strings.EqualFold(keyword, "roledn") {
		return who
	}
	return strings.ToLower(keyword) + " " + who
}

// aciApplies decides whether an aci bears on the entry asked about.
//
// An aci is in force on the entry it is written on and everything beneath it,
// so the interesting cases are the ones that narrow that: an explicit target,
// a scope of "base" on an ancestor, or a filter nobody here evaluates.
func aciApplies(rule Rule, t *Target, target, why string) (string, string) {
	if t.DN != "" && !dnUnder(target, t.DN) {
		return AppliesNo, "the rule targets another part of the tree"
	}
	if rule.Inherited {
		switch t.Scope {
		case "base", "entry":
			return AppliesNo, "the rule applies only to the entry it is written on"
		case "onelevel":
			if dnEqual(parentOf(target), rule.Source) {
				return AppliesYes, "this entry is one level under the entry the rule is written on"
			}
			return AppliesNo, "the rule applies one level under the entry it is written on"
		}
	}
	if t.Filter != "" {
		return AppliesMaybe, "the rule is narrowed by a filter, which Alder does not evaluate"
	}
	if rule.Inherited {
		return AppliesYes, "the rule is written on an entry above this one, and applies down the tree"
	}
	return AppliesYes, orDefault(why, "the rule is written on this entry")
}
