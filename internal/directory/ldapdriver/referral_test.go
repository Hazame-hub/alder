package ldapdriver

import (
	"errors"
	"slices"
	"testing"

	ber "github.com/go-asn1-ber/asn1-ber"
	"github.com/go-ldap/ldap/v3"
)

// A referral result carries the URLs it refers to in the LDAPResult's
// referral field, which go-ldap leaves in the packet. Losing them would make a
// referral indistinguishable from any other refusal, and a caller deciding
// what it could not check has to know where the referral was.
func TestAReferralResultKeepsItsURLsAndTheEntryItStoppedAt(t *testing.T) {
	packet := resultPacket(ldap.LDAPResultReferral)
	// Round-tripped through the wire encoding, as go-ldap would read it.
	decoded, err := ber.DecodePacketErr(packet.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	got := cleanLDAPError(ldap.GetLDAPError(decoded))
	var le *Error
	if !errors.As(got, &le) || le.Code != ldap.LDAPResultReferral {
		t.Fatalf("got %v, want a referral", got)
	}
	if le.ReferralDN != "ou=remote,dc=alder,dc=test" {
		t.Errorf("referral DN = %q", le.ReferralDN)
	}
	want := []string{"ldap://a.test/uid=x,ou=remote,dc=alder,dc=test", "ldap://b.test/uid=x,ou=remote,dc=alder,dc=test"}
	if !slices.Equal(le.Referrals, want) {
		t.Errorf("referrals = %q, want %q", le.Referrals, want)
	}

	// Any other refusal carries neither: a matched DN on noSuchObject can name
	// an entry the bind may not know exists.
	decoded, _ = ber.DecodePacketErr(resultPacket(ldap.LDAPResultNoSuchObject).Bytes())
	if errors.As(cleanLDAPError(ldap.GetLDAPError(decoded)), &le); le.Code != ldap.LDAPResultNoSuchObject ||
		le.ReferralDN != "" || le.Referrals != nil {
		t.Errorf("noSuchObject kept %+v", le)
	}
}

// resultPacket is a SearchResultDone with this code, a matched DN and two
// referral URLs.
func resultPacket(code int) *ber.Packet {
	packet := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAP message")
	packet.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, 1, "id"))
	done := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ldap.ApplicationSearchResultDone, nil, "done")
	done.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, code, "code"))
	done.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "ou=remote,dc=alder,dc=test", "matched"))
	done.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "message"))
	refs := ber.Encode(ber.ClassContext, ber.TypeConstructed, 3, nil, "referral")
	for _, host := range []string{"a", "b"} {
		refs.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString,
			"ldap://"+host+".test/uid=x,ou=remote,dc=alder,dc=test", "uri"))
	}
	done.AppendChild(refs)
	packet.AppendChild(done)
	return packet
}
