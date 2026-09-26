package api

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/access"
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

	report, err := access.For(ctx, sess.Conn, target)
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
