package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/helmet"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/web"
)

type serveOptions struct {
	addr        string
	tlsCert     string
	tlsKey      string
	allowHTTP   bool
	allowPlain  bool
	readOnly    bool
	logLevel    string
	logFormat   string
	sourceURL   string
	idleTimeout time.Duration
	maxLifetime time.Duration
}

func serveCmd() *cobra.Command {
	var o serveOptions

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Alder web UI and API",
		Long: "Starts the HTTP server that serves the single-page application and\n" +
			"the API it talks to.\n\n" +
			"Alder holds directory credentials in memory for the life of a browser\n" +
			"session, so it serves HTTPS by default. Pass --tls-cert and --tls-key,\n" +
			"or terminate TLS in front of it and pass --allow-http.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd.Context(), o)
		},
	}

	f := cmd.Flags()
	f.StringVar(&o.addr, "addr", ":8443", "address to listen on")
	f.StringVar(&o.tlsCert, "tls-cert", "", "PEM certificate chain to serve with")
	f.StringVar(&o.tlsKey, "tls-key", "", "PEM private key to serve with")
	f.BoolVar(&o.allowHTTP, "allow-http", false,
		"serve plain HTTP. Only safe behind a reverse proxy that terminates TLS")
	f.BoolVar(&o.allowPlain, "i-know-this-is-insecure", false,
		"permit connecting to a directory over plaintext LDAP")
	f.BoolVar(&o.readOnly, "read-only", false,
		"refuse every write, whatever the directory would have allowed")
	f.StringVar(&o.logLevel, "log-level", "info", "debug, info, warn or error")
	f.StringVar(&o.logFormat, "log-format", "text", "text or json")
	// Alder is AGPL-3.0. If you run a modified build and let others use it over
	// a network, section 13 requires you to offer them its source; this is where
	// you say where that is.
	f.StringVar(&o.sourceURL, "source-url", "",
		"where this build's source can be obtained (required for a modified build)")
	f.DurationVar(&o.idleTimeout, "session-idle-timeout", 30*time.Minute,
		"close a session that has not been used for this long")
	f.DurationVar(&o.maxLifetime, "session-max-lifetime", 12*time.Hour,
		"close a session this long after it was opened, however active")

	return cmd
}

func runServe(ctx context.Context, o serveOptions) error {
	logger, err := newLogger(o.logLevel, o.logFormat)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)

	serveTLS := o.tlsCert != "" && o.tlsKey != ""
	switch {
	case serveTLS:
	case o.allowHTTP:
		// Said once, loudly, at startup. A password crossing a plain HTTP
		// connection is the failure this whole design is meant to avoid, and
		// the operator should have to have decided to accept it.
		logger.Warn("serving plain HTTP: bind passwords will cross the network unencrypted " +
			"unless something in front of Alder is terminating TLS")
	default:
		return errors.New("alder serve: refusing to start without TLS.\n" +
			"Pass --tls-cert and --tls-key, or --allow-http if a reverse proxy in front of\n" +
			"Alder terminates TLS. Alder holds directory bind credentials for the life of a\n" +
			"session and will not carry them over a plaintext connection by default")
	}
	if o.allowPlain {
		logger.Warn("plaintext LDAP connections are permitted (--i-know-this-is-insecure)")
	}
	if o.readOnly {
		logger.Info("running read-only; every write will be refused")
	}
	if !web.Built() {
		logger.Warn("the single-page application is not built into this binary; " +
			"the API is live but the UI is a placeholder. Run \"task web\"")
	}

	server := api.NewServer(logger, api.Config{
		// The cookie's Secure attribute must match how the browser reaches
		// Alder, which behind a proxy is HTTPS even though this process speaks
		// HTTP. --allow-http is the operator saying "a proxy is in front", so
		// the cookie stays Secure unless they are running HTTP end to end.
		Secure:             serveTLS || !isLoopback(o.addr),
		AllowPlaintextLDAP: o.allowPlain,
		ReadOnly:           o.readOnly,
		IdleTimeout:        o.idleTimeout,
		MaxLifetime:        o.maxLifetime,
		SourceURL:          o.sourceURL,
		Version:            buildVersion(),
	})
	defer server.Close()

	app := fiber.New(fiber.Config{
		AppName:               "Alder " + buildVersion(),
		DisableStartupMessage: true,
		// Fiber's default error handler renders errors as plain text; the API
		// answers JSON everywhere, including for a panic.
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			var fe *fiber.Error
			if errors.As(err, &fe) {
				code = fe.Code
			}
			logger.Error("unhandled request error", "path", c.Path(), "status", code, "error", err)
			return c.Status(code).JSON(fiber.Map{
				"error":   "internal",
				"message": http.StatusText(code),
			})
		},
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
		// An LDIF import is the largest thing a client sends.
		BodyLimit: 16 << 20,
	})

	app.Use(recover.New())
	app.Use(requestLogger(logger))
	app.Use(helmet.New(helmet.Config{
		// The SPA is served from this origin and loads nothing from anywhere
		// else. Saying so removes script injection as a way to exfiltrate a
		// directory to a third party.
		ContentSecurityPolicy: "default-src 'self'; " +
			"script-src 'self'; " +
			"style-src 'self' 'unsafe-inline'; " +
			"img-src 'self' data: blob:; " +
			"font-src 'self'; " +
			"connect-src 'self'; " +
			"object-src 'none'; " +
			"base-uri 'none'; " +
			"form-action 'self'; " +
			"frame-ancestors 'none'",
		ReferrerPolicy:          "no-referrer",
		XFrameOptions:           "DENY",
		CrossOriginOpenerPolicy: "same-origin",
	}))
	app.Use(compress.New())

	app.Get("/healthz", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "version": buildVersion()})
	})

	// Order matters: the API first, then the SPA, which answers every
	// unrecognised path with index.html so client-side routing survives a hard
	// refresh.
	server.Register(app)
	web.Register(app)

	errc := make(chan error, 1)
	go func() {
		scheme := "http"
		if serveTLS {
			scheme = "https"
		}
		logger.Info("alder is listening",
			"url", fmt.Sprintf("%s://%s", scheme, displayAddr(o.addr)),
			"version", buildVersion(),
			"read_only", o.readOnly)
		if serveTLS {
			errc <- app.ListenTLS(o.addr, o.tlsCert, o.tlsKey)
			return
		}
		errc <- app.Listen(o.addr)
	}()

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("alder serve: %w", err)
		}
		return nil
	case sig := <-sigc:
		logger.Info("shutting down", "signal", sig.String())
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	// Shutting down closes every directory connection, which is what discards
	// the credentials held for them.
	if err := app.ShutdownWithTimeout(10 * time.Second); err != nil {
		return fmt.Errorf("alder serve: shutdown: %w", err)
	}
	return nil
}

// requestLogger logs one line per request.
//
// It logs the method, the path and the status. It does not log the query
// string, because a DN is in there and a DN names a person; and it never sees
// a body, because a body carries passwords.
func requestLogger(logger *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		started := time.Now()
		err := c.Next()
		logger.Debug("request",
			"method", c.Method(),
			"path", c.Path(),
			"status", c.Response().StatusCode(),
			"duration", time.Since(started).Round(time.Millisecond).String())
		return err
	}
}

func newLogger(level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("alder: unknown log level %q", level)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch strings.ToLower(format) {
	case "json":
		return slog.New(slog.NewJSONHandler(os.Stderr, opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(os.Stderr, opts)), nil
	default:
		return nil, fmt.Errorf("alder: unknown log format %q", format)
	}
}

// isLoopback reports whether an address binds only to the loopback interface.
// It decides whether the session cookie may drop its Secure attribute, which is
// otherwise a browser-enforced refusal to store it over plain HTTP.
func isLoopback(addr string) bool {
	host, _, found := strings.Cut(addr, ":")
	if !found {
		return false
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "[::1]"
}

// displayAddr renders a listen address as something clickable in a terminal.
func displayAddr(addr string) string {
	host, port, found := strings.Cut(addr, ":")
	if !found {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "[::]" {
		host = "localhost"
	}
	if _, err := strconv.Atoi(port); err != nil {
		return addr
	}
	return host + ":" + port
}
