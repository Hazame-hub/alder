package api

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/config"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Indexes, with a door of their own.
//
// Alder could create and remove an index from 1.23, and only inside a
// configuration comparison: a row appeared when this server differed from a
// snapshot of one that already had the index you wanted. So the feature was
// really "copy an index another server has", and the ordinary reason to add
// one -- a slow search you have just diagnosed -- had no path at all. A UI
// audit looked for it on the database entry, in the editor, and on the
// directory screen, found nothing, and wrote down that you were back in
// ldapmodify.
//
// The derivation is the one the comparison already uses. Nothing here is a
// second way to write an index; it is a second way to ask for the same
// change, which then goes through the same plan, the same LDIF and the same
// confirmation as every other write.

// GetConfigIndexes lists what each backend indexes.
func (s *Server) GetConfigIndexes(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	ctx, cancel := reqCtx(c)
	defer cancel()

	snap, err := config.Capture(ctx, sess.Conn, config.Options{})
	if err != nil {
		return configRefusal(c, s, err)
	}

	out := IndexReport{Provider: snap.Source.Provider, Backends: []IndexBackend{}}
	if snap.Completeness == snapshot.ConfigPartial {
		out.Note = ptr("Part of the configuration tree could not be read, so this may not be every index.")
	}

	// The backends, in the order the capture holds them, so two readings of
	// an unchanged server list them the same way.
	contexts := sess.Conn.Capabilities().NamingContexts
	for _, resource := range snap.Resources {
		if resource.Kind != config.KindDatabase && resource.Kind != config.KindBackend {
			continue
		}
		backend := IndexBackend{Name: resource.Name, Dn: resource.DN, Indexes: []IndexEntry{}}
		for _, index := range snap.Resources {
			if index.Kind != config.KindIndex || !belongsTo(index, resource) {
				continue
			}
			backend.Indexes = append(backend.Indexes, indexEntry(snap, index))
		}
		if !servesData(resource, contexts, len(backend.Indexes)) {
			continue
		}
		out.Backends = append(out.Backends, backend)
	}
	return c.JSON(out)
}

// servesData reports whether this is a backend an operator would index.
//
// The configuration model calls a great many things a backend. On 389
// Directory Server the list includes `bdb`, `ldbm`, the default-index
// templates and one entry per template attribute -- thirty-nine of them on
// the harness, of which exactly one holds data. A panel listing all of them
// is a panel nobody reads.
//
// The rule is the server's own: a backend that serves one of the naming
// contexts the RootDSE advertises is a backend holding directory data. Not a
// list of names, and not a guess from the shape of the DN -- the same
// capability the tree and the search screen are built on.
//
// A backend that already holds indexes is kept whatever its name, so nothing
// the server is really indexing can vanish from the screen because Alder
// disagreed about what it is.
func servesData(resource snapshot.ConfigResource, contexts []string, indexes int) bool {
	if indexes > 0 {
		return true
	}
	for _, ctx := range contexts {
		if strings.EqualFold(strings.TrimSpace(ctx), resource.Name) {
			return true
		}
	}
	return false
}

// belongsTo reports whether an index is this backend's. An index is named
// `<backend>/<attribute>`, which is the same identity a comparison uses.
func belongsTo(index, backend snapshot.ConfigResource) bool {
	name, _, ok := strings.Cut(index.Name, "/")
	return ok && strings.EqualFold(name, backend.Name)
}

func indexEntry(snap *snapshot.ConfigSnapshot, index snapshot.ConfigResource) IndexEntry {
	out := IndexEntry{
		Attribute: index.Label,
		Id:        index.ID(),
		Types:     ptr(config.IndexTypesOf(index, snap.Settings)),
	}
	if out.Attribute == "" {
		// The label is the attribute as the server spelled it; the name is
		// the identity. Falling back to the tail of the identity keeps a row
		// readable rather than blank.
		if _, attribute, ok := strings.Cut(index.Name, "/"); ok {
			out.Attribute = attribute
		}
	}
	record, refusal := config.RemoveRecord(snap, index)
	if refusal != "" {
		out.Blocked = ptr(refusal)
		out.BlockedDetail = ptr(refusalInWords(refusal))
		return out
	}
	out.Remove = ptr(changeRequest(record))
	return out
}

// refusalInWords is the reason a screen prints. The code is what a client
// branches on and is not a sentence; an audit found `index_value_shared`
// rendered raw to an operator, which is a code golf answer to "why can I not
// remove this".
func refusalInWords(refusal string) string {
	switch refusal {
	case config.RefusalIndexShared:
		return "This index shares its value with another attribute, so taking it away would " +
			"mean rewriting a value that indexes something else too. That is an edit rather " +
			"than a deletion, and Alder does not make it for you."
	case config.RefusalSystemIndex:
		return "The server maintains this index for itself. Removing it is not Alder's to offer."
	case config.RefusalNotPresent:
		return "This index is not on the server as Alder read it."
	case config.RefusalUnnamed:
		return "Alder could not tell which attribute this index is for."
	case config.RefusalNoParent:
		return "The backend this index would belong to is not in the configuration Alder read."
	case config.RefusalNotCreatable:
		return "This is not a kind of object Alder creates."
	}
	return refusal
}

// PlanConfigIndex derives the write this server wants for a new index.
func (s *Server) PlanConfigIndex(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	var req IndexRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "The request body could not be read.", err.Error())
	}
	attribute := strings.TrimSpace(req.Attribute)
	backendName := strings.TrimSpace(req.Backend)
	if attribute == "" || backendName == "" {
		return badRequest(c, "An index needs a backend and an attribute.", "")
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	snap, err := config.Capture(ctx, sess.Conn, config.Options{})
	if err != nil {
		return configRefusal(c, s, err)
	}

	backend, ok := backendNamed(snap, backendName)
	if !ok {
		return writeError(c, fiber.StatusNotFound, ErrorErrorNotFound,
			"No such backend on this server.",
			"The backend is named as the index report gives it, which is the suffix it serves.")
	}

	want := snapshot.ConfigResource{
		Section: config.SectionPerformance,
		Kind:    config.KindIndex,
		Name:    backend.Name + "/" + strings.ToLower(attribute),
		Label:   attribute,
	}
	// Already there is not a refusal. Saying so is the difference between a
	// screen that explains and one that appears to have done nothing.
	if _, exists := snap.ResourceByID(want.ID()); exists {
		return c.JSON(IndexCandidate{Exists: ptr(true)})
	}

	record, refusal := config.CreateRecord(snap, want, deref(req.Types))
	if refusal != "" {
		return c.JSON(IndexCandidate{
			Blocked:       ptr(refusal),
			BlockedDetail: ptr(refusalInWords(refusal)),
		})
	}
	out := changeRequest(record)
	return c.JSON(IndexCandidate{Change: &out})
}

func backendNamed(snap *snapshot.ConfigSnapshot, name string) (snapshot.ConfigResource, bool) {
	for _, resource := range snap.Resources {
		if resource.Kind != config.KindDatabase && resource.Kind != config.KindBackend {
			continue
		}
		if strings.EqualFold(resource.Name, name) {
			return resource, true
		}
	}
	return snapshot.ConfigResource{}, false
}
