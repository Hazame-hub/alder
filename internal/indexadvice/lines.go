package indexadvice

import (
	"strconv"
	"strings"
	"time"
)

// The lines both servers write, read by what they contain rather than by a
// fixed prefix: 389 DS starts each line with a bracketed time, OpenLDAP with
// whatever syslog, journald or its own logger put in front.
//
//	389 DS:   [30/Oct/2026:14:02:11.123456789 +0000] conn=12 op=3 SRCH base="dc=alder,dc=test" scope=2 filter="(mail=x)" attrs=ALL
//	          [30/Oct/2026:14:02:11.623456789 +0000] conn=12 op=3 RESULT err=0 tag=101 nentries=1 wtime=0.0001 optime=0.5 etime=0.5001 notes=U details="Partially Unindexed Filter"
//	OpenLDAP: Oct 30 14:02:11 ldap slapd[42]: conn=1000 op=2 SRCH base="dc=alder,dc=test" scope=2 deref=0 filter="(mail=x)"
//	          Oct 30 14:02:11 ldap slapd[42]: conn=1000 op=2 SEARCH RESULT tag=101 err=0 qtime=0.000012 etime=0.500123 nentries=1 text=
//	          Oct 30 14:02:11 ldap slapd[42]: <= mdb_equality_candidates: (mail) not indexed

// connOp finds "conn=N op=M " and returns the pair as one key, and what
// follows it.
func connOp(line string) (string, string, bool) {
	i := strings.Index(line, "conn=")
	if i < 0 {
		return "", "", false
	}
	rest := line[i+len("conn="):]
	conn, rest, ok := strings.Cut(rest, " ")
	if !ok || !digits(conn) {
		return "", "", false
	}
	rest, ok = strings.CutPrefix(rest, "op=")
	if !ok {
		return "", "", false
	}
	op, rest, ok := strings.Cut(rest, " ")
	if !ok || !digits(op) {
		return "", "", false
	}
	return conn + "/" + op, rest, true
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// quoted reads name="value", where the value may hold an escaped quote. Both
// fields it is used for follow a space ("SRCH base=", " filter=").
func quoted(rest, name string) (string, bool) {
	i := strings.Index(rest, " "+name+`"`)
	if i < 0 {
		return "", false
	}
	var b strings.Builder
	for k := i + 1 + len(name) + 1; k < len(rest); k++ {
		c := rest[k]
		if c == '\\' && k+1 < len(rest) && rest[k+1] == '"' {
			b.WriteByte('"')
			k++
			continue
		}
		if c == '"' {
			return b.String(), true
		}
		b.WriteByte(c)
	}
	return "", false
}

// field reads name=value, the value running to the next space.
func field(rest, name string) (string, bool) {
	i := strings.Index(" "+rest, " "+name)
	if i < 0 {
		return "", false
	}
	value := rest[i+len(name):]
	if j := strings.IndexByte(value, ' '); j >= 0 {
		value = value[:j]
	}
	return value, value != ""
}

// parseSeconds reads a duration written as seconds, with or without a
// fraction. Older 389 DS releases write whole seconds.
func parseSeconds(s string) time.Duration {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f > 1e7 {
		return 0
	}
	return time.Duration(f * float64(time.Second))
}

// unindexedNote reports 389 DS's own word that a search was partly (U) or
// fully (A) unindexed. P (paged) and the rest say nothing about indexes.
func unindexedNote(rest string) bool {
	notes, ok := field(rest, "notes=")
	if !ok {
		return false
	}
	for _, n := range strings.Split(notes, ",") {
		if n == "U" || n == "A" {
			return true
		}
	}
	return false
}

// notIndexedLine reads OpenLDAP's "<= mdb_equality_candidates: (mail) not
// indexed", which names the attribute and the kind of match and no operation.
func notIndexedLine(line string) (string, string, bool) {
	if !strings.HasSuffix(strings.TrimSpace(line), "not indexed") {
		return "", "", false
	}
	for fn, kind := range map[string]string{
		"_equality_candidates":   TypeEquality,
		"_inequality_candidates": TypeEquality,
		"_substring_candidates":  TypeSubstring,
		"_presence_candidates":   TypePresence,
		"_approx_candidates":     TypeApprox,
	} {
		i := strings.Index(line, fn+": (")
		if i < 0 {
			continue
		}
		rest := line[i+len(fn)+3:]
		attr, _, ok := strings.Cut(rest, ")")
		if !ok || attr == "" || strings.ContainsAny(attr, " =") {
			return "", "", false
		}
		return attr, kind, true
	}
	return "", "", false
}

// lineTime reads 389 DS's bracketed time. OpenLDAP's syslog prefix carries no
// year, so its lines go without one rather than with a guessed one.
func lineTime(line string) time.Time {
	if !strings.HasPrefix(line, "[") {
		return time.Time{}
	}
	end := strings.IndexByte(line, ']')
	if end < 0 {
		return time.Time{}
	}
	stamp := line[1:end]
	for _, layout := range []string{"02/Jan/2006:15:04:05.999999999 -0700", "02/Jan/2006:15:04:05 -0700"} {
		if at, err := time.Parse(layout, stamp); err == nil {
			return at.UTC()
		}
	}
	return time.Time{}
}
