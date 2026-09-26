package directory

import (
	"errors"
	"sort"
	"strings"
)

// What a server says an identity may do on an entry.
//
// This is not Alder's opinion. Reading access rules -- which internal/access
// does -- tells an operator what is written; it cannot tell them what the
// server will do, because evaluation depends on group membership, filters,
// connection security and the order of rules the reader may not be able to
// see. A server that publishes the Get Effective Rights control will answer
// the real question directly, and where one does, that answer is worth more
// than any amount of rule reading.
//
// Only 389 Directory Server publishes it today. OpenLDAP has no equivalent,
// and there is no pretending otherwise: a report from a server that cannot
// answer says so.

// ErrRightsUnsupported is returned by a session whose server does not publish
// the Get Effective Rights control.
var ErrRightsUnsupported = errors.New("directory: this server does not answer what an identity may do")

// ErrRightsUnanswered is returned when a server that publishes the control
// answered the search without any rights: it declined the question, usually
// because this bind may not ask about that identity. It is not "no rights".
var ErrRightsUnanswered = errors.New("directory: the server did not answer what that identity may do")

// AttributeRights is what an identity may do with one attribute, in the
// server's own letters.
type AttributeRights struct {
	Name string `json:"name"`
	// Rights as the server wrote them: "rsc", "rscwo", "none".
	Rights string `json:"rights"`
}

// EffectiveRights is the server's answer for one identity on one entry.
type EffectiveRights struct {
	// Subject is the identity asked about, as it was asked.
	Subject string `json:"subject"`
	// Entry is the entry-level rights, in the server's own letters.
	Entry string `json:"entry"`
	// Attributes is the per-attribute answer, sorted by name.
	Attributes []AttributeRights `json:"attributes"`
}

// Letters the servers use. Glossed for a reader, never translated away: the
// letters are what the server said, and an unknown one is shown as it came.
var (
	entryRightWords = map[byte]string{
		'v': "view this entry",
		'a': "add an entry under it",
		'd': "delete it",
		'n': "rename it",
	}
	attributeRightWords = map[byte]string{
		'r': "read",
		's': "search",
		'c': "compare",
		'w': "add a value",
		'o': "delete a value",
		'S': "self-write",
		'W': "write",
		'O': "obliterate",
		'p': "proxy",
	}
)

// EntryWords glosses the entry-level letters, in the order the server gave
// them, and passes through anything it does not recognise.
func (r *EffectiveRights) EntryWords() []string { return words(r.Entry, entryRightWords) }

// AttributeWords glosses one attribute's letters the same way.
func AttributeWords(rights string) []string { return words(rights, attributeRightWords) }

func words(letters string, gloss map[byte]string) []string {
	letters = strings.TrimSpace(letters)
	if letters == "" || strings.EqualFold(letters, "none") {
		return nil
	}
	var out []string
	for i := 0; i < len(letters); i++ {
		c := letters[i]
		if c == ',' || c == ' ' {
			continue
		}
		if word, ok := gloss[c]; ok {
			out = append(out, word)
			continue
		}
		out = append(out, string(c))
	}
	return out
}

// ParseAttributeLevelRights reads the server's attributeLevelRights value:
//
//	"objectClass:rsc, userPassword:none, alderTeam:none"
//
// A value it cannot split is skipped rather than guessed at; the caller still
// has the entry-level answer, and a wrong per-attribute claim here would be
// read as the server's own.
func ParseAttributeLevelRights(value string) []AttributeRights {
	var out []AttributeRights
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, rights, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		name, rights = strings.TrimSpace(name), strings.TrimSpace(rights)
		if name == "" {
			continue
		}
		out = append(out, AttributeRights{Name: name, Rights: rights})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}
