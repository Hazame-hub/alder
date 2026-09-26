package access

import (
	"regexp"
	"strconv"
	"strings"
)

// Reading olcAccess.
//
// The syntax, from slapd.access(5), is an ordered list of
//
//	{n}to <what> [by <who> <access> [<control>]]...
//
// consulted in order, first match wins, and that ordering is the whole
// mechanism: a "to * by * none" at position zero makes everything below it
// dead text. So the index is reported, always, and the rules are kept in the
// server's order rather than sorted into something tidier.
//
// What is parsed here is the common shape -- the target and the by clauses --
// and what is not parsed is said so. A rule with a regular expression target,
// a set specification or an SSF control is reported whole, because a half-read
// access rule is worse than an unread one: it invites a decision made on the
// half that was understood.

var olcIndex = regexp.MustCompile(`^\{(-?\d+)\}`)

// ParseOpenLDAP reads one olcAccess value. The raw value is always kept; the
// structure is filled in only where it was read with confidence.
func ParseOpenLDAP(value, source, target string) Rule {
	rule := Rule{Style: StyleOpenLDAP, Source: source, Raw: value, Applies: AppliesMaybe}

	body := value
	if m := olcIndex.FindStringSubmatch(body); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			rule.Index = &n
		}
		body = body[len(m[0]):]
	}
	body = strings.TrimSpace(body)

	lower := strings.ToLower(body)
	if !strings.HasPrefix(lower, "to ") && lower != "to" {
		rule.Why = "Alder did not recognise this as a to/by rule"
		return rule
	}
	body = strings.TrimSpace(body[len("to"):])

	// The target runs up to the first " by " clause; everything after that is
	// a sequence of them.
	what, rest := splitFirstBy(body)
	t, ok := parseOpenLDAPTarget(strings.TrimSpace(what))
	rule.Target = t
	grants, grantsOk := parseByClauses(rest)
	rule.Grants = grants
	rule.Parsed = ok && grantsOk
	if !rule.Parsed && rule.Why == "" {
		rule.Why = "part of this rule is in a form Alder does not read; it is shown as the server holds it"
	}
	rule.Applies, rule.Why = openLDAPApplies(t, ok, target, rule.Why)
	return rule
}

// splitFirstBy splits "…what… by …" at the first by clause, respecting that
// "by" can appear inside a quoted DN.
func splitFirstBy(s string) (what, rest string) {
	quoted := false
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '"':
			quoted = !quoted
		case quoted:
		case strings.HasPrefix(strings.ToLower(s[i:]), "by ") && (i == 0 || isSpace(s[i-1])):
			return s[:i], s[i:]
		}
	}
	return s, ""
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' }

// parseOpenLDAPTarget reads the "to" half: "*", "dn[.style]=<dn>",
// "filter=<f>", "attrs=<a,b>", in any combination the syntax allows.
func parseOpenLDAPTarget(what string) (*Target, bool) {
	t := &Target{}
	if what == "" {
		return t, false
	}
	if what == "*" {
		t.Scope = "*"
		return t, true
	}
	ok := true
	for _, token := range splitTokens(what) {
		lower := strings.ToLower(token)
		switch {
		case lower == "*":
			t.Scope = "*"
		case strings.HasPrefix(lower, "dn"):
			eq := strings.Index(token, "=")
			if eq < 0 {
				ok = false
				continue
			}
			style := strings.TrimPrefix(strings.ToLower(token[:eq]), "dn")
			style = strings.TrimPrefix(style, ".")
			if style == "" {
				style = "base"
			}
			t.Scope = style
			t.DN = unquote(token[eq+1:])
		case strings.HasPrefix(lower, "filter="):
			t.Filter = unquote(token[len("filter="):])
		case strings.HasPrefix(lower, "attrs="):
			for _, a := range strings.Split(unquote(token[len("attrs="):]), ",") {
				if a = strings.TrimSpace(a); a != "" {
					t.Attributes = append(t.Attributes, a)
				}
			}
		default:
			// A set specification, a value clause, something newer: not read.
			ok = false
		}
	}
	return t, ok
}

// splitTokens splits on whitespace outside quotes.
func splitTokens(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			quoted = !quoted
			cur.WriteByte(c)
		case !quoted && (c == ' ' || c == '\t'):
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// parseByClauses reads "by <who> <access> [<control>]" repeatedly.
func parseByClauses(rest string) ([]Grant, bool) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return nil, true
	}
	ok := true
	var grants []Grant
	for rest != "" {
		lower := strings.ToLower(rest)
		if !strings.HasPrefix(lower, "by") {
			ok = false
			break
		}
		rest = strings.TrimSpace(rest[len("by"):])
		clause, next := splitFirstBy(rest)
		rest = strings.TrimSpace(next)

		tokens := splitTokens(strings.TrimSpace(clause))
		if len(tokens) == 0 {
			ok = false
			continue
		}
		grant := Grant{Subject: unquote(tokens[0]), Kind: KindLevel}
		if len(tokens) > 1 {
			grant.Access = strings.Join(tokens[1:], " ")
		} else {
			// "by self" with no level means +0 in slapd's grammar, which is
			// not something to translate into a word here.
			grant.Access = ""
			ok = false
		}
		grants = append(grants, grant)
	}
	return grants, ok
}

// openLDAPApplies decides whether a rule bears on the entry asked about.
//
// Only the forms whose meaning is certain answer yes or no. A regular
// expression, a filter or an unread target answers maybe, with the reason:
// this report is read by somebody deciding whether a rule is their problem,
// and a confident wrong answer there is worse than an honest "it might be".
func openLDAPApplies(t *Target, parsed bool, target, why string) (string, string) {
	if t == nil || !parsed {
		return AppliesMaybe, orDefault(why, "Alder did not read this rule's target")
	}
	switch t.Scope {
	case "*":
		return AppliesYes, "the rule is written about every entry"
	case "base", "exact":
		if dnEqual(t.DN, target) {
			return AppliesYes, "the rule names this entry"
		}
		return AppliesNo, "the rule names another entry"
	case "subtree", "sub":
		if dnUnder(target, t.DN) {
			return AppliesYes, "this entry is in the subtree the rule names"
		}
		return AppliesNo, "this entry is outside the subtree the rule names"
	case "one", "onelevel":
		if parentOf(target) != "" && dnEqual(parentOf(target), t.DN) {
			return AppliesYes, "this entry is one level under what the rule names"
		}
		return AppliesNo, "this entry is not one level under what the rule names"
	case "children":
		if !dnEqual(target, t.DN) && dnUnder(target, t.DN) {
			return AppliesYes, "this entry is under what the rule names"
		}
		return AppliesNo, "this entry is not under what the rule names"
	}
	if t.Filter != "" {
		return AppliesMaybe, "the rule is written about entries matching a filter, which Alder does not evaluate"
	}
	if t.Scope == "" && t.DN == "" {
		// "to attrs=userPassword" names no entries, which in this grammar
		// means all of them: the attribute is the whole target.
		if len(t.Attributes) > 0 {
			return AppliesYes, "the rule is written about these attributes on every entry"
		}
		return AppliesYes, "the rule is written about every entry"
	}
	return AppliesMaybe, orDefault(why, "the rule's target is a form Alder does not evaluate")
}

func orDefault(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// parentOf is the DN one level up, as text.
func parentOf(target string) string {
	quoted := false
	for i := 0; i < len(target); i++ {
		switch {
		case target[i] == '\\':
			i++
		case target[i] == '"':
			quoted = !quoted
		case target[i] == ',' && !quoted:
			return strings.TrimSpace(target[i+1:])
		}
	}
	return ""
}
