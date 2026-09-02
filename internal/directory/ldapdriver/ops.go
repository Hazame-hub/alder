package ldapdriver

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-ldap/ldap/v3"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/schema"
)

// Schema reads and parses the subschema subentry, once per session.
//
// The DN comes from the RootDSE, never from a constant: OpenLDAP publishes
// cn=subschema and 389 DS publishes cn=schema, and hardcoding either is the
// vendor branching the charter forbids.
func (s *session) Schema(ctx context.Context) (*schema.Schema, error) {
	s.schemaOnce.Do(func() {
		s.schema, s.schemaErr = s.loadSchema(ctx)
	})
	return s.schema, s.schemaErr
}

func (s *session) loadSchema(ctx context.Context) (*schema.Schema, error) {
	subschemaDN := s.caps.SubschemaSubentry
	if subschemaDN == "" {
		return nil, errors.New("directory: the server published no subschemaSubentry, so the schema cannot be located")
	}
	req := ldap.NewSearchRequest(
		subschemaDN, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 0, int(s.timeout.Seconds()), false,
		// The subschema entry is a subentry. OpenLDAP returns it for a plain
		// base search, but 389 DS requires the filter to name the object class
		// explicitly, and this filter satisfies both without a vendor check.
		"(objectClass=subschema)",
		schema.SubschemaAttributes,
		nil,
	)
	res, err := s.searchLocked(ctx, req)
	if err != nil || len(res.Entries) == 0 {
		// Fall back to the filter that matches anything. Some servers answer
		// the subschema search only this way, and the difference is not worth
		// a capability probe.
		req.Filter = "(objectClass=*)"
		res, err = s.searchLocked(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("directory: reading the schema from %s: %w", subschemaDN, err)
		}
	}
	if len(res.Entries) == 0 {
		return nil, fmt.Errorf("directory: the schema entry %s returned nothing", subschemaDN)
	}

	e := res.Entries[0]
	attrs := make(map[string][]string, len(e.Attributes))
	for _, a := range e.Attributes {
		attrs[a.Name] = append(attrs[a.Name], a.Values...)
	}
	parsed := schema.Load(e.DN, attrs)
	if len(parsed.Errors) > 0 {
		s.logger.Warn("some schema definitions did not parse",
			"subschema", e.DN, "failed", len(parsed.Errors), "loaded", parsed.Counts().ObjectClasses)
	}
	return parsed, nil
}

// Read returns one entry by DN.
//
// It is a base-scoped search rather than a distinct operation because LDAP has
// no read: reading an entry is searching at its DN with base scope, and saying
// so keeps one code path.
func (s *session) Read(ctx context.Context, target dn.DN, attrs []string) (*directory.Entry, error) {
	if len(attrs) == 0 {
		attrs = []string{"*", "+"}
	}
	req := ldap.NewSearchRequest(
		target.String(), ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, int(s.timeout.Seconds()), false,
		"(objectClass=*)", attrs, nil,
	)
	res, err := s.searchLocked(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(res.Entries) == 0 {
		return nil, &Error{Code: ldap.LDAPResultNoSuchObject, Message: ldap.LDAPResultCodeMap[ldap.LDAPResultNoSuchObject]}
	}
	return convertEntry(res.Entries[0])
}

// Search runs a paged search, returning at most req.Limit entries.
//
// Pages are fetched in a loop rather than one page per call, because a page is
// a wire-level detail and a limit is what the caller actually means. The cookie
// of an unfinished search is returned so the caller can continue; it is only
// valid on this session's connection.
func (s *session) Search(ctx context.Context, req directory.SearchRequest) (*directory.SearchResult, error) {
	req.Normalise()

	rendered, err := req.Filter.Render()
	if err != nil {
		return nil, fmt.Errorf("directory: %w", err)
	}

	out := &directory.SearchResult{}
	cookie := req.Cookie

	for {
		remaining := req.Limit - len(out.Entries)
		if remaining <= 0 {
			out.Truncated = true
			break
		}
		// Normalise clamps PageSize into [1, MaxPageSize] and remaining is
		// positive here, so this is bounded well below the uint32 the control
		// takes. The bound is restated rather than assumed, because the caller
		// of Normalise is a long way from the conversion below and a page size
		// that wrapped would ask the server for a very different page than the
		// one intended.
		pageSize := min(req.PageSize, remaining)
		if pageSize < 1 {
			pageSize = 1
		}
		if pageSize > directory.MaxPageSize {
			pageSize = directory.MaxPageSize
		}

		search := ldap.NewSearchRequest(
			req.BaseDN.String(), ldapScope(req.Scope), ldap.NeverDerefAliases,
			0, int(s.timeout.Seconds()), req.TypesOnly,
			rendered, req.Attributes, nil,
		)
		if s.caps.Paging {
			paging := ldap.NewControlPaging(uint32(pageSize))
			if len(cookie) > 0 {
				paging.SetCookie(cookie)
			}
			search.Controls = append(search.Controls, paging)
		} else {
			// Without the paging control, the size limit is the only bound
			// available. The server answers sizeLimitExceeded when it has more,
			// which is reported as truncation rather than as a failure.
			search.SizeLimit = remaining
		}

		res, searchErr := s.searchLocked(ctx, search)
		if searchErr != nil {
			var le *Error
			if errors.As(searchErr, &le) && le.Code == ldap.LDAPResultSizeLimitExceeded {
				out.Truncated = true
				break
			}
			return nil, searchErr
		}

		for _, e := range res.Entries {
			entry, convErr := convertEntry(e)
			if convErr != nil {
				return nil, convErr
			}
			out.Entries = append(out.Entries, entry)
		}
		out.Referrals = append(out.Referrals, res.Referrals...)

		if !s.caps.Paging {
			break
		}
		cookie = pagingCookie(res.Controls)
		if len(cookie) == 0 {
			break // the server has no more results
		}
		if len(out.Entries) >= req.Limit {
			out.Truncated = true
			break
		}
	}

	out.Cookie = cookie
	if len(out.Cookie) > 0 {
		out.Truncated = true
	}
	return out, nil
}

func pagingCookie(controls []ldap.Control) []byte {
	c := ldap.FindControl(controls, ldap.ControlTypePaging)
	if c == nil {
		return nil
	}
	paging, ok := c.(*ldap.ControlPaging)
	if !ok {
		return nil
	}
	return paging.Cookie
}

func ldapScope(s directory.Scope) int {
	switch s {
	case directory.ScopeBase:
		return ldap.ScopeBaseObject
	case directory.ScopeOneLevel:
		return ldap.ScopeSingleLevel
	default:
		return ldap.ScopeWholeSubtree
	}
}

// HasChildren reports whether an entry has any immediate children.
//
// The tree browser calls this per node to decide whether to draw an expander.
// It asks for one entry and no attributes, which is the cheapest question the
// protocol can answer.
func (s *session) HasChildren(ctx context.Context, target dn.DN) (bool, error) {
	req := ldap.NewSearchRequest(
		target.String(), ldap.ScopeSingleLevel, ldap.NeverDerefAliases,
		1, int(s.timeout.Seconds()), false,
		"(objectClass=*)",
		// "1.1" is the RFC 4511 OID meaning "no attributes at all".
		[]string{"1.1"}, nil,
	)
	res, err := s.searchLocked(ctx, req)
	if err != nil {
		var le *Error
		if errors.As(err, &le) && le.Code == ldap.LDAPResultSizeLimitExceeded {
			return true, nil
		}
		return false, err
	}
	return len(res.Entries) > 0, nil
}

// Children returns the immediate children of an entry, for the tree browser.
func (s *session) Children(ctx context.Context, parent dn.DN, attrs []string, limit int, cookie []byte) (*directory.SearchResult, error) {
	return s.Search(ctx, directory.SearchRequest{
		BaseDN:     parent,
		Scope:      directory.ScopeOneLevel,
		Filter:     filter.Present("objectClass"),
		Attributes: attrs,
		Limit:      limit,
		Cookie:     cookie,
	})
}

// Apply performs a change. It is the only method in the package that writes.
//
// Everything above it in the application has already rendered this exact record
// as LDIF and had a person confirm it. Nothing here reinterprets the record: it
// maps one to one onto an LDAP operation.
func (s *session) Apply(ctx context.Context, ch directory.ChangeRecord) error {
	if err := ch.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("directory: the session is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	var err error
	switch ch.Type {
	case directory.ChangeAdd:
		req := ldap.NewAddRequest(ch.DN.String(), nil)
		for _, a := range ch.Attrs {
			req.Attribute(a.Name, byteValuesToStrings(a.Values))
		}
		err = s.conn.Add(req)
	case directory.ChangeModify:
		req := ldap.NewModifyRequest(ch.DN.String(), nil)
		for _, m := range ch.Mods {
			vals := byteValuesToStrings(m.Values)
			switch m.Op {
			case directory.ModAdd:
				req.Add(m.Name, vals)
			case directory.ModReplace:
				req.Replace(m.Name, vals)
			case directory.ModDelete:
				// A delete with no values removes the attribute entirely; the
				// go-ldap API expresses that as an empty value slice, and the
				// distinction is preserved from the LDIF the user confirmed.
				req.Delete(m.Name, vals)
			}
		}
		err = s.conn.Modify(req)
	case directory.ChangeDelete:
		err = s.conn.Del(ldap.NewDelRequest(ch.DN.String(), nil))
	case directory.ChangeModRDN:
		newSuperior := ""
		if len(ch.NewSuperior) > 0 {
			newSuperior = ch.NewSuperior.String()
		}
		err = s.conn.ModifyDN(ldap.NewModifyDNRequest(
			ch.DN.String(), ch.NewRDN, ch.DeleteOldRDN, newSuperior))
	default:
		return fmt.Errorf("directory: unknown change type %q", ch.Type)
	}

	if err != nil {
		cleaned := cleanLDAPError(err)
		// The summary names the DN and the attributes touched, never a value.
		s.logger.Warn("change rejected", "change", ch.Summary(), "error", cleaned)
		return cleaned
	}
	s.logger.Info("change applied", "change", ch.Summary())
	return nil
}

// byteValuesToStrings converts values for the go-ldap API, which takes strings.
//
// This is lossless: a Go string is a byte sequence and is not required to be
// valid UTF-8, so a JPEG survives the conversion unchanged. It is written out
// rather than inlined so that the reason is recorded where the conversion is.
func byteValuesToStrings(values [][]byte) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}
