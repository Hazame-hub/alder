package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/envflags"
	"github.com/hazame-hub/alder/internal/session"
)

// The passwords are read from the environment, from a file, or from standard
// input -- never from a flag. A flag's value is in the shell history, in the
// process list any user on the machine can read, and in the log of every CI job
// that echoes its commands.
const (
	// #nosec G101 -- the names of the variables a password is read from, not passwords.
	bindPasswordEnv = "ALDER_BIND_PASSWORD"
	// #nosec G101 -- as above.
	configBindPasswordEnv = "ALDER_CONFIG_BIND_PASSWORD"
)

// connection is how a command reaches a directory: the running Alder server to
// ask, and the fields the web interface's connection screen sends. Nothing here
// is a second way to describe an LDAP connection; each directory flag is a field
// of POST /session.
type connection struct {
	apiURL    string
	apiCAFile string
	timeout   time.Duration

	host               string
	port               int
	tlsMode            string
	caFile             string
	serverName         string
	insecureSkipVerify bool

	bindDN            string
	bindPasswordFile  string
	bindPasswordStdin bool

	configBindDN           string
	configBindPasswordFile string
}

func (c *connection) registerAPI(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&c.apiURL, "api-url", "",
		"the running Alder server to use, such as https://alder.example.com")
	f.StringVar(&c.apiCAFile, "api-ca-file", "",
		"PEM certificates to verify the Alder server's HTTPS certificate with, instead of the system roots")
	f.DurationVar(&c.timeout, "timeout", 10*time.Minute,
		"give up on the whole command after this long; 0 waits indefinitely")
}

func (c *connection) registerDirectory(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&c.host, "host", "", "the directory server's host, as the Alder server reaches it")
	f.IntVar(&c.port, "port", 0, "the directory server's port (default 636 for ldaps, 389 otherwise)")
	f.StringVar(&c.tlsMode, "tls", "ldaps", "ldaps, starttls or plaintext")
	f.StringVar(&c.caFile, "ca-file", "",
		"PEM certificates to verify the directory's certificate with, instead of the system roots")
	f.StringVar(&c.serverName, "server-name", "",
		"the name to check the directory's certificate against, when it is not --host")
	f.BoolVar(&c.insecureSkipVerify, "insecure-skip-verify", false,
		"do not verify the directory's certificate; the session is marked unverified")
	f.StringVar(&c.bindDN, "bind-dn", "", "the DN to bind as; omit for an anonymous bind")
	f.StringVar(&c.bindPasswordFile, "bind-password-file", "",
		"read the bind password from the first line of this file (otherwise "+bindPasswordEnv+" is used)")
	f.BoolVar(&c.bindPasswordStdin, "bind-password-stdin", false,
		"read the bind password from the first line of standard input")
	f.StringVar(&c.configBindDN, "config-bind-dn", "",
		"a second identity, used only for the server's configuration tree")
	f.StringVar(&c.configBindPasswordFile, "config-bind-password-file", "",
		"read that identity's password from this file (otherwise "+configBindPasswordEnv+" is used)")
	// Skipping certificate checks and taking a password from standard input are
	// decisions for the command line in front of you, not for an environment a
	// job inherited.
	envflags.Exclude(f, "insecure-skip-verify", "bind-password-stdin")
}

// checkAPI validates only what reaching the Alder server needs.
func (c *connection) checkAPI() error {
	_, _, err := apiBase(c.apiURL)
	return err
}

// check validates the connection flags before anything is read or sent.
func (c *connection) check(readsStdin bool) error {
	if _, _, err := apiBase(c.apiURL); err != nil {
		return err
	}
	if c.host == "" {
		return usagef("--host is required (or set ALDER_HOST): the directory server the Alder server should connect to")
	}
	switch c.tlsMode {
	case "ldaps", "starttls", "plaintext":
	default:
		return usagef("--tls must be ldaps, starttls or plaintext, not %q", c.tlsMode)
	}
	if c.port < 0 || c.port > 65535 {
		return usagef("--port must be between 1 and 65535")
	}
	if c.bindDN == "" && (c.bindPasswordFile != "" || c.bindPasswordStdin) {
		return usagef("a bind password was given without --bind-dn")
	}
	if c.bindPasswordFile != "" && c.bindPasswordStdin {
		return usagef("--bind-password-file and --bind-password-stdin are two answers to one question; give one")
	}
	if c.bindPasswordStdin && readsStdin {
		return usagef("standard input cannot carry both the bind password and the input document; " +
			"use --bind-password-file or " + bindPasswordEnv + " for the password")
	}
	if c.configBindDN == "" && c.configBindPasswordFile != "" {
		return usagef("--config-bind-password-file was given without --config-bind-dn")
	}
	return nil
}

func (c *connection) effectivePort() int {
	if c.port != 0 {
		return c.port
	}
	if c.tlsMode == "ldaps" {
		return 636
	}
	return 389
}

func (c *connection) label() string {
	return net.JoinHostPort(c.host, strconv.Itoa(c.effectivePort()))
}

// bound applies --timeout to the command's context.
func (c *connection) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.timeout)
}

// apiBase turns --api-url into the API's base address.
func apiBase(raw string) (string, *url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil, usagef("--api-url is required (or set ALDER_API_URL): the address of a running Alder server")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", nil, usagef("--api-url must be an http:// or https:// address, such as https://alder.example.com")
	}
	if u.User != nil {
		// A password in a URL is a password in a flag.
		return "", nil, usagef("--api-url must not contain a user name or password")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", nil, usagef("--api-url must not contain a query or a fragment")
	}
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, "/api/v1") {
		path += "/api/v1"
	}
	u.Path = path
	u.RawPath = ""
	return u.String(), u, nil
}

func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// remote is one conversation with an Alder server.
type remote struct {
	env       *Env
	base      string
	doer      *sessionDoer
	api       *api.ClientWithResponses
	connected bool
}

// sessionDoer carries Alder's session cookie from the response that set it to
// every request after, which is what a browser does. There is no cookie jar: a
// jar applies browser rules, and the one this cookie needs -- send it back to
// the server that set it, and nowhere else -- is simpler to state than to
// configure.
type sessionDoer struct {
	client *http.Client
	cookie *http.Cookie
}

func (d *sessionDoer) Do(req *http.Request) (*http.Response, error) {
	if d.cookie != nil {
		// #nosec G124 -- a cookie sent back to the server that set it; this program issues none.
		req.AddCookie(&http.Cookie{Name: d.cookie.Name, Value: d.cookie.Value})
	}
	// #nosec G704 -- the request goes to the Alder server named by --api-url, which is
	// what the client is for; redirects are not followed.
	res, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	for _, ck := range res.Cookies() {
		if ck.Name != session.CookieName && ck.Name != session.CookieNameInsecure {
			continue
		}
		if ck.Value == "" || ck.MaxAge < 0 {
			d.cookie = nil
			continue
		}
		// #nosec G124 -- held only to send back to the server that set it.
		d.cookie = &http.Cookie{Name: ck.Name, Value: ck.Value}
	}
	return res, nil
}

// client builds a remote without opening a session.
func (c *connection) client(env *Env) (*remote, error) {
	base, u, err := apiBase(c.apiURL)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if c.apiCAFile != "" {
		pem, readErr := readSmallFile(c.apiCAFile, 1<<20)
		if readErr != nil {
			return nil, failf("input", "cannot read --api-ca-file: %v", readErr)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, usagef("--api-ca-file %s holds no PEM certificate", c.apiCAFile)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	if u.Scheme == "http" && !loopback(u.Hostname()) {
		writef(env.Stderr, "alder: warning: %s is plain HTTP to another machine, "+
			"so the bind password crosses the network unencrypted\n", base)
	}
	doer := &sessionDoer{client: &http.Client{
		Transport: transport,
		// A redirected POST can carry its body -- the bind password -- to
		// wherever the redirect points. Alder's API never redirects, so a
		// redirect is a misconfiguration to report, not to follow.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	cl, err := api.NewClientWithResponses(base, api.WithHTTPClient(doer))
	if err != nil {
		return nil, usagef("--api-url: %v", err)
	}
	return &remote{env: env, base: base, doer: doer, api: cl}, nil
}

// open connects to the directory through Alder, as the connection screen does.
func (c *connection) open(ctx context.Context, env *Env) (*remote, error) {
	r, err := c.client(env)
	if err != nil {
		return nil, err
	}
	req := api.ConnectRequest{Host: c.host, Port: c.effectivePort(), Tls: api.ConnectRequestTls(c.tlsMode)}
	if c.caFile != "" {
		pem, readErr := readSmallFile(c.caFile, 1<<20)
		if readErr != nil {
			return nil, failf("input", "cannot read --ca-file: %v", readErr)
		}
		s := string(pem)
		req.CaCertificate = &s
	}
	if c.serverName != "" {
		req.ServerName = &c.serverName
	}
	if c.insecureSkipVerify {
		req.InsecureSkipVerify = &c.insecureSkipVerify
	}
	if c.bindDN != "" {
		secret, pwErr := c.password(env, c.bindPasswordFile, c.bindPasswordStdin, bindPasswordEnv, "--bind-dn")
		if pwErr != nil {
			return nil, pwErr
		}
		req.BindDn, req.BindPassword = &c.bindDN, &secret
	}
	if c.configBindDN != "" {
		secret, pwErr := c.password(env, c.configBindPasswordFile, false, configBindPasswordEnv, "--config-bind-dn")
		if pwErr != nil {
			return nil, pwErr
		}
		req.ConfigBindDn, req.ConfigBindPassword = &c.configBindDN, &secret
	}

	what := "opening a session on " + c.label() + " through " + r.base
	res, err := r.api.CreateSessionWithResponse(ctx, req)
	if err != nil {
		return nil, transportFailure(ctx, what, err)
	}
	if res.StatusCode() != http.StatusCreated {
		return nil, r.refusal(ctx, "opening a directory session on "+c.label(), res.HTTPResponse, res.Body)
	}
	if r.doer.cookie == nil {
		return nil, failf("no_session", "Alder at %s accepted the connection but issued no session cookie", r.base)
	}
	r.connected = true
	return r, nil
}

// password finds a password in the one place it was put.
func (c *connection) password(env *Env, file string, stdin bool, envName, forFlag string) (string, error) {
	switch {
	case file != "":
		return readSecretFile(file)
	case stdin:
		if err := env.claimStdin("the bind password"); err != nil {
			return "", err
		}
		return readSecret(env.Stdin, "standard input")
	}
	if v, ok := env.Getenv(envName); ok && v != "" {
		return v, nil
	}
	return "", usagef("%s needs a password: set %s, or pass %s-file or --bind-password-stdin",
		forFlag, envName, strings.TrimSuffix(strings.TrimPrefix(forFlag, "--"), "-dn")+"-password")
}

// close ends the session. It uses a context of its own, so an interrupted
// command still logs out rather than leaving its session to time out.
func (r *remote) close() {
	if r == nil || !r.connected {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = r.api.DeleteSessionWithResponse(ctx)
	r.connected = false
}

func readSmallFile(path string, limit int64) ([]byte, error) {
	// #nosec G304 -- a certificate file the user named on the command line.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d KB", path, limit>>10)
	}
	return data, nil
}

func ptr[T any](v T) *T { return &v }
