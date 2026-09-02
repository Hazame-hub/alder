package api

import (
	"runtime/debug"

	"github.com/gofiber/fiber/v2"
)

// SourceOffer is what AGPL-3.0 section 13 requires a networked service to give
// the people using it: a way to get the Corresponding Source of the version
// they are actually talking to.
//
// It is served rather than written in a README because the obligation is to the
// user of the running instance, and the user of a modified instance has no
// reason to know where its source went. An operator who deploys a fork points
// --source-url at their fork and the offer is satisfied.
type SourceOffer struct {
	License   string `json:"license"`
	SourceURL string `json:"sourceUrl"`
	Version   string `json:"version"`
	Revision  string `json:"revision,omitempty"`
	Modified  bool   `json:"modified,omitempty"`
	Notice    string `json:"notice"`
}

// DefaultSourceURL is where the unmodified project lives. An operator running a
// modified build is required to change it.
const DefaultSourceURL = "https://github.com/hazame-hub/alder"

const sourceNotice = "Alder is free software under the GNU Affero General Public License, " +
	"version 3. If you are using a modified version of Alder over a network, " +
	"section 13 entitles you to its Corresponding Source. If sourceUrl does not " +
	"lead to the source of the version you are talking to, the operator of this " +
	"instance is not complying with the licence."

// registerSourceOffer mounts the AGPL section 13 offer. It is deliberately
// outside the generated API surface and needs no session: the whole point is
// that anyone who can reach the service can reach the offer.
func (s *Server) registerSourceOffer(router fiber.Router) {
	router.Get("/source", func(c *fiber.Ctx) error {
		return c.JSON(s.sourceOffer())
	})
}

func (s *Server) sourceOffer() SourceOffer {
	url := s.cfg.SourceURL
	if url == "" {
		url = DefaultSourceURL
	}
	offer := SourceOffer{
		License:   "AGPL-3.0-only",
		SourceURL: url,
		Version:   s.cfg.Version,
		Notice:    sourceNotice,
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				offer.Revision = setting.Value
			case "vcs.modified":
				offer.Modified = setting.Value == "true"
			}
		}
	}
	return offer
}
