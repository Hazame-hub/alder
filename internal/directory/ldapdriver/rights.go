package ldapdriver

import (
	"context"
	"strings"

	ber "github.com/go-asn1-ber/asn1-ber"
	"github.com/go-ldap/ldap/v3"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

// Asking the server what an identity may do.
//
// 389 Directory Server's Get Effective Rights control takes an authorization
// identity and answers, on the entry searched, two virtual attributes:
//
//	entryLevelRights: v
//	attributeLevelRights: objectClass:rsc, userPassword:none, alderTeam:none
//
// That answer is the directory's own, computed by the same code that will
// refuse the operation, which is exactly what reading access rules cannot be.
//
// The control value is the authorization identity as text -- "dn: <DN>" --
// and not a BER structure around it. Both encodings were tried against a real
// server: the plain form answers, and the wrapped form makes 389 DS reply with
// a numeric error code in place of the rights, which is the kind of thing only
// a harness tells you.

// effectiveRightsControl carries the authorization identity to ask about.
type effectiveRightsControl struct{ authzID string }

func (c effectiveRightsControl) GetControlType() string { return directory.OIDEffectiveRights }

func (c effectiveRightsControl) String() string {
	return "Get Effective Rights (" + directory.OIDEffectiveRights + ")"
}

func (c effectiveRightsControl) Encode() *ber.Packet {
	p := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "Control")
	p.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString,
		directory.OIDEffectiveRights, "Control Type"))
	// Never critical. A server that publishes the control but refuses this
	// request should answer the search without the rights, not fail it: the
	// entry is what the caller came for.
	p.AppendChild(ber.NewBoolean(ber.ClassUniversal, ber.TypePrimitive, ber.TagBoolean, false, "Criticality"))
	p.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString,
		c.authzID, "Control Value"))
	return p
}

// EffectiveRights asks the server what subject may do on target.
//
// subject is a DN, or empty for the identity this session is bound as -- the
// common case, because "why can't I write this?" is asked about oneself. An
// empty string is sent as "dn:" with no DN, which is how the control names an
// anonymous requester; a bound session passes its own DN instead.
func (s *session) EffectiveRights(ctx context.Context, target dn.DN, subject string) (*directory.EffectiveRights, error) {
	if !s.caps.EffectiveRights {
		return nil, directory.ErrRightsUnsupported
	}
	authz := "dn:"
	if subject = strings.TrimSpace(subject); subject != "" {
		authz = "dn: " + subject
	}

	req := ldap.NewSearchRequest(
		target.String(), ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, int(s.timeout.Seconds()), false,
		"(objectClass=*)",
		// The rights come back as virtual attributes, and they only come back
		// for the attributes the search asked for: "*" is what makes the
		// per-attribute answer cover the entry rather than one attribute.
		[]string{"*", "aclRights"},
		[]ldap.Control{effectiveRightsControl{authzID: authz}},
	)
	res, err := s.searchLocked(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(res.Entries) == 0 {
		return nil, &Error{Code: ldap.LDAPResultNoSuchObject, Message: ldap.LDAPResultCodeMap[ldap.LDAPResultNoSuchObject]}
	}

	out := &directory.EffectiveRights{Subject: subject}
	entry := res.Entries[0]
	for _, attr := range entry.Attributes {
		switch strings.ToLower(attr.Name) {
		case "entrylevelrights":
			if len(attr.Values) > 0 {
				out.Entry = strings.TrimSpace(attr.Values[0])
			}
		case "attributelevelrights":
			for _, value := range attr.Values {
				out.Attributes = append(out.Attributes, directory.ParseAttributeLevelRights(value)...)
			}
		}
	}
	if out.Entry == "" && len(out.Attributes) == 0 {
		// The server publishes the control and answered nothing: it declined
		// this request, most often because the bind may not ask about that
		// identity. Saying so beats reporting "no rights", which reads as a
		// verdict.
		return nil, directory.ErrRightsUnanswered
	}
	return out, nil
}
