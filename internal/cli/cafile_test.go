package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testCertificatePEM is a self-signed CA certificate, made fresh so no key
// material is committed.
func testCertificatePEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Alder test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestCertificatesOnly(t *testing.T) {
	cert := testCertificatePEM(t)
	key := "-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEIBAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4fICE=\n-----END PRIVATE KEY-----\n"

	good := map[string]string{
		"one certificate":   cert,
		"a bundle":          cert + cert,
		"bundle comments":   "# Alder test CA\n\n" + cert + "\n# another\n" + cert,
		"windows endings":   strings.ReplaceAll(cert, "\n", "\r\n"),
		"surrounding blank": "\n\n" + cert + "\n\n",
	}
	for name, in := range good {
		t.Run(name, func(t *testing.T) {
			out, err := certificatesOnly([]byte(in))
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if strings.Contains(out, "#") {
				t.Errorf("comments were sent:\n%s", out)
			}
			if !strings.HasPrefix(out, "-----BEGIN CERTIFICATE-----\n") {
				t.Errorf("output is not the certificates:\n%s", out)
			}
		})
	}

	bad := map[string]string{
		"empty":                "",
		"not PEM":              "[credsStore]\nauth = c2VjcmV0\n",
		"a private key":        key,
		"a key beside a cert":  cert + key,
		"text before a cert":   "token=s3cret\n" + cert,
		"text after a cert":    cert + "token=s3cret\n",
		"a broken certificate": "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n",
	}
	for name, in := range bad {
		t.Run(name, func(t *testing.T) {
			if out, err := certificatesOnly([]byte(in)); err == nil {
				t.Errorf("accepted, and would have sent:\n%s", out)
			}
		})
	}
}

// A project names both the Alder server and its CA file, and a pull request
// can change both. A ca-file that is anything but certificates is refused
// before the session request, so its content never reaches a server.
func TestAProjectCAFileThatIsNotCertificatesIsNeverSent(t *testing.T) {
	s := newStub(t)
	const secret = "ghp_notARealTokenButTreatedAsOne"
	dir := writeProject(t, s, map[string]string{"runner-secrets.txt": "token=" + secret + "\n"})
	writeAlderYAML(t, dir, s, "    ca-file: runner-secrets.txt\n")

	r := s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "dev")
	expectCode(t, r, ExitFailed)
	if !strings.Contains(r.stderr, "ca-file") || !strings.Contains(r.stderr, "nothing was sent") {
		t.Errorf("stderr does not say why:\n%s", r.stderr)
	}
	if strings.Contains(r.stdout+r.stderr, secret) {
		t.Error("the file's content was printed")
	}
	if n := len(s.called("POST /api/v1/session")); n != 0 {
		t.Errorf("%d session request(s) sent; a ca-file that is not certificates must stop the command first", n)
	}
	for _, c := range s.calls {
		if strings.Contains(string(c.Body), secret) {
			t.Errorf("%s %s carried the file's content", c.Method, c.Path)
		}
	}
}

// The certificates themselves still go, and only they: a comment in the
// bundle is not part of what is sent.
func TestAProjectCAFileSendsOnlyTheCertificates(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/plan", 200, planJSON(t, counts{}))
	cert := testCertificatePEM(t)
	dir := writeProject(t, s, map[string]string{"certs/ca.pem": "# the dev CA, from the platform team\n" + cert})
	writeAlderYAML(t, dir, s, "    ca-file: certs/ca.pem\n")

	r := s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "dev")
	expectCode(t, r, ExitOK)
	var connect map[string]any
	_ = json.Unmarshal(s.called("POST /api/v1/session")[0].Body, &connect)
	if connect["caCertificate"] != cert {
		t.Errorf("caCertificate sent = %q, want exactly the certificate", connect["caCertificate"])
	}
}

// writeAlderYAML rewrites the dev environment of writeProject's alder.yaml
// with extra lines added to it.
func writeAlderYAML(t *testing.T, dir string, s *stub, extra string) {
	t.Helper()
	writeProjectFile(t, dir, "alder.yaml", `version: 1
managed:
  - base: ou=people,dc=alder,dc=test
    files: [people/*.ldif]
environments:
  dev:
    api-url: `+s.srv.URL+`
    host: ldap.dev.test
    bind-dn: cn=alder,ou=services,dc=alder,dc=test
    bind-password-env: ALDER_DEV_BIND_PASSWORD
`+extra)
}

func writeProjectFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
