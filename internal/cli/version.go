package cli

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/envflags"
)

func versionCmd(env *Env) *cobra.Command {
	var conn connection
	var asJSONOutput bool
	cmd := command(env, &cobra.Command{
		Use:   "version",
		Short: "Print this client's version, and a server's with --api-url",
		Long: "Prints the version of this alder binary. With --api-url (or ALDER_API_URL) it\n" +
			"also asks that Alder server for its version, which needs no session and no\n" +
			"directory.",
		Args: argsBetween(0, 0, "no arguments"),
	}, func(ctx context.Context, _ []string) error {
		return runVersion(ctx, env, &conn, asJSONOutput)
	})
	conn.registerAPI(cmd)
	cmd.Flags().BoolVar(&asJSONOutput, "json", false, "write JSON to standard output")
	envflags.Exclude(cmd.Flags(), "json")
	return cmd
}

func runVersion(ctx context.Context, env *Env, conn *connection, jsonMode bool) (err error) {
	defer func() { err = asJSON(env, jsonMode, err) }()
	client, _ := json.Marshal(map[string]string{"version": env.Version})
	if conn.apiURL == "" {
		if jsonMode {
			return writeDocument(env.Stdout, []byte(`{"client":`+string(client)+`}`))
		}
		writef(env.Stdout, "alder %s\n", env.Version)
		return nil
	}
	r, err := conn.client(env)
	if err != nil {
		return err
	}
	ctx, cancel := conn.bound(ctx)
	defer cancel()
	res, err := r.api.GetSourceOfferWithResponse(ctx)
	if err != nil {
		return transportFailure(ctx, "asking "+r.base+" for its version", err)
	}
	if res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		return r.refusal(ctx, "the version request", res.HTTPResponse, res.Body)
	}
	if jsonMode {
		return writeDocument(env.Stdout, []byte(`{"client":`+string(client)+`,"server":`+string(compactJSON(res.Body))+`}`))
	}
	s := res.JSON200
	writef(env.Stdout, "alder %s\n", env.Version)
	server := "server: alder " + safe(s.Version)
	if s.Revision != nil && *s.Revision != "" {
		server += " (" + safe(*s.Revision)
		if s.Modified != nil && *s.Modified {
			server += ", modified"
		}
		server += ")"
	}
	writef(env.Stdout, "%s at %s\n", server, r.base)
	return nil
}
