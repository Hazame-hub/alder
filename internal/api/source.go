package api

import (
	"runtime/debug"

	"github.com/gofiber/fiber/v2"
)

// The AGPL-3.0 section 13 offer: a way for the people using this running
// instance to get the Corresponding Source of the version they are actually
// talking to.
//
// It is served rather than written in a README because the obligation runs to
// the user of the running instance, and the user of a modified instance has no
// reason to know where its source went. An operator who deploys a fork points
// --source-url at their fork and the offer is satisfied.
//
// It answers without a session. That used to be the reason it was registered by
// hand, outside the generated surface -- but a session in this package is
// required per handler by require(), never by middleware, so being in the
// contract never implied being behind one. Describing it in openapi.yaml costs
// nothing and closes the gap that did matter: docs/COMPATIBILITY.md calls that
// document the contract, and until 1.0 it was silent about the one endpoint a
// licence obliges Alder to serve.

// DefaultSourceURL is where the unmodified project lives. An operator running a
// modified build is required to change it.
const DefaultSourceURL = "https://github.com/hazame-hub/alder"

const sourceNotice = "Alder is free software under the GNU Affero General Public License, " +
	"version 3. If you are using a modified version of Alder over a network, " +
	"section 13 entitles you to its Corresponding Source. If sourceUrl does not " +
	"lead to the source of the version you are talking to, the operator of this " +
	"instance is not complying with the licence."

// GetSourceOffer serves the offer.
//
// No require() call, deliberately: an offer only the already-connected can read
// would not discharge the obligation.
func (s *Server) GetSourceOffer(c *fiber.Ctx) error {
	return c.JSON(s.sourceOffer())
}

func (s *Server) sourceOffer() SourceOffer {
	url := s.cfg.SourceURL
	if url == "" {
		url = DefaultSourceURL
	}
	offer := SourceOffer{
		License:   "AGPL-3.0-only",
		SourceUrl: url,
		Version:   s.cfg.Version,
		Notice:    sourceNotice,
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				offer.Revision = ptr(setting.Value)
			case "vcs.modified":
				// Reported only when true. False and absent mean the same
				// thing here, and the schema says so.
				if setting.Value == "true" {
					offer.Modified = ptr(true)
				}
			}
		}
	}
	return offer
}
