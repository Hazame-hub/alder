package api

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/access"
	"github.com/hazame-hub/alder/internal/policy"
)

// Reading access control (1.19).
//
// "Why can't I write this?" is the question a directory is worst at answering,
// and Alder answered it with nothing at all: the preflight report listed
// access control among the things it neither reads nor translates. This reads
// it.
//
// Nothing here writes. Access control is the one thing in a directory that can
// lock every administrator out of it -- including the one making the change --
// and editing it stays out of scope on the record. What is in scope is putting
// the rules in front of the person who has to reason about them, in the
// server's order, with the raw value always beside whatever Alder read.

// GetAccessRules reports the access control rules that bear on one entry.
func (s *Server) GetAccessRules(c *fiber.Ctx, params GetAccessRulesParams) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	target, ok := parseDNParam(c, params.Dn)
	if !ok {
		return nil
	}
	ctx, cancel := reqCtx(c)
	defer cancel()

	// Who to ask about: the identity named, or the one this session is bound
	// as. A report about "you" is the question an operator is actually asking.
	//
	// A named one is parsed before it is used. The only authzID the driver
	// builds is "dn: <DN>", so anything that is not a DN is a question the
	// server cannot be asked, and 389 DS answers such a control with a
	// numeric error code where the rights letters go -- which reaches the
	// reader as "the server declined to say", a sentence that describes
	// access rather than a typo. Refusing it here says what actually
	// happened.
	subject := deref(params.As)
	if strings.TrimSpace(subject) == "" {
		subject = sess.BindDN()
	} else if _, ok := parseDNParam(c, subject); !ok {
		return nil
	}
	report, err := access.For(ctx, sess.Conn, target, access.Options{Subject: subject})
	if errors.Is(err, access.ErrNoEntry) {
		// The rules of an entry nobody can read would be a list with no
		// subject; the directory's own refusal is the honest answer.
		return s.fail(c, errors.Unwrap(err))
	}
	if err != nil {
		return s.fail(c, err)
	}

	out := AccessReport{
		Dn: report.DN, Styles: report.Styles, Disclaimer: access.Disclaimer,
		Rules: make([]AccessRule, 0, len(report.Rules)),
	}
	out.RightsNote = ptrIfSet(report.RightsNote)
	if e := report.Effective; e != nil {
		rights := EffectiveRights{Subject: e.Subject, Entry: e.Entry}
		rights.EntryWords = ptrIfAny(e.EntryWords)
		if len(e.Attributes) > 0 {
			attrs := make([]EffectiveAttributeRight, 0, len(e.Attributes))
			for _, a := range e.Attributes {
				attrs = append(attrs, EffectiveAttributeRight{
					Name: a.Name, Rights: a.Rights, Words: ptrIfAny(a.Words),
				})
			}
			rights.Attributes = &attrs
		}
		out.Effective = &rights
	}
	if len(report.Unread) > 0 {
		unread := make([]AccessUnread, 0, len(report.Unread))
		for _, u := range report.Unread {
			unread = append(unread, AccessUnread{Where: u.Where, Reason: u.Reason})
		}
		out.Unread = &unread
	}
	for _, rule := range report.Rules {
		out.Rules = append(out.Rules, accessRule(rule))
	}
	return c.JSON(out)
}

func accessRule(rule access.Rule) AccessRule {
	out := AccessRule{
		Style: rule.Style, Source: rule.Source, Raw: rule.Raw, Parsed: rule.Parsed,
		Applies: AccessRuleApplies(rule.Applies),
	}
	out.Index = rule.Index
	out.Name = ptrIfSet(rule.Name)
	out.Why = ptrIfSet(rule.Why)
	out.Inherited = ptrIfTrue(rule.Inherited)
	if rule.Target != nil {
		target := AccessTarget{}
		target.Scope = ptrIfSet(rule.Target.Scope)
		target.Dn = ptrIfSet(rule.Target.DN)
		target.Filter = ptrIfSet(rule.Target.Filter)
		target.Attributes = ptrIfAny(rule.Target.Attributes)
		out.Target = target
	}
	if len(rule.Grants) > 0 {
		grants := make([]AccessGrant, 0, len(rule.Grants))
		for _, grant := range rule.Grants {
			grants = append(grants, AccessGrant{
				Subject: grant.Subject, Access: grant.Access, Kind: AccessGrantKind(grant.Kind),
			})
		}
		out.Grants = &grants
	}
	return out
}

// GetPasswordPolicy reports the password policy in force on an entry and what
// the server records about that account.
//
// The sibling of the access report, and the same discipline: it says what the
// server holds and where, and it does not decide whether a bind would succeed.
func (s *Server) GetPasswordPolicy(c *fiber.Ctx, params GetPasswordPolicyParams) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	target, ok := parseDNParam(c, params.Dn)
	if !ok {
		return nil
	}
	ctx, cancel := reqCtx(c)
	defer cancel()

	report, err := policy.For(ctx, sess.Conn, target)
	if errors.Is(err, policy.ErrNoEntry) {
		return s.fail(c, errors.Unwrap(err))
	}
	if err != nil {
		return s.fail(c, err)
	}

	out := PolicyReport{Dn: report.DN, Disclaimer: policy.Disclaimer}
	if p := report.Policy; p != nil {
		rendered := PasswordPolicy{Source: PasswordPolicySource(p.Source), Why: p.Why,
			Settings: policySettings(p.Settings)}
		rendered.Dn = ptrIfSet(p.DN)
		out.Policy = &rendered
	}
	if st := report.State; st != nil {
		out.State = AccountState{Locked: st.Locked, MustChange: st.MustChange,
			Attributes: policySettings(st.Attributes)}
		out.State.LockedDetail = ptrIfSet(st.LockedDetail)
		out.State.Expiry = ptrIfSet(st.Expiry)
		out.State.Failures = ptrIfSet(st.Failures)
		out.State.Changed = ptrIfSet(st.Changed)
	}
	if len(report.Unread) > 0 {
		unread := make([]AccessUnread, 0, len(report.Unread))
		for _, u := range report.Unread {
			unread = append(unread, AccessUnread{Where: u.Where, Reason: u.Reason})
		}
		out.Unread = &unread
	}
	return c.JSON(out)
}

func policySettings(in []policy.Setting) []PolicySetting {
	out := make([]PolicySetting, 0, len(in))
	for _, s := range in {
		setting := PolicySetting{Key: s.Key, Values: s.Values}
		setting.Label = ptrIfSet(s.Label)
		setting.Detail = ptrIfSet(s.Detail)
		out = append(out, setting)
	}
	return out
}
