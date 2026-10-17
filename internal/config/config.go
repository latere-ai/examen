// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package config reads what an operator sets: every EXAMEN_* variable of
// specs/011-distribution.md, once, at start, into one typed value. No other
// package of the module reads the environment.
//
// A value that does not parse is an error that names its variable and never
// its value: a variable may hold a secret, and a database URL holds a
// password. The refusals at start that need nothing but the settings and the
// files they name are made here too, so a server that cannot run says so
// before it opens anything.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"latere.ai/x/pkg/authz"
)

// The defaults of the table of specs/011-distribution.md.
const (
	DefaultAddr          = ":8080"
	DefaultBasePath      = "/v1"
	DefaultAudience      = "examen"
	DefaultToposAudience = "topos"
	DefaultCellaAudience = "cella"
	DefaultLuxAudience   = "lux"
	DefaultArcaAudience  = "arca"
	DefaultOrigoAudience = "origo"
	DefaultWorkers       = 20
)

// The token sources of specs/004-identity-and-authorization.md, as the
// start log names them.
const (
	TokenSourceMinter = "minter"
	TokenSourceFiles  = "token files"
	TokenSourceNone   = "none"
)

// The key sources of specs/004-identity-and-authorization.md.
const (
	KeySourceLux      = "lux"
	KeySourceEndpoint = "endpoint"
)

// Settings are the settings of the process.
type Settings struct {
	Addr string // EXAMEN_ADDR
	// BasePath is the prefix of every route of the API: empty, or a path
	// that starts with a slash and does not end with one.
	BasePath string // EXAMEN_BASE_PATH
	// PublicURL is where readers reach this server, with no trailing
	// slash; empty when the installation names none.
	PublicURL string // EXAMEN_PUBLIC_URL
	// TrustedProxies are the address ranges from which X-Forwarded-For
	// names a public read's client address.
	TrustedProxies []netip.Prefix // EXAMEN_TRUSTED_PROXIES
	LogLevel       slog.Level     // EXAMEN_LOG_LEVEL

	// DatabaseURL is the direct endpoint of the database: migrations run
	// over it, because the migrator holds a session lock across its
	// statements. DatabasePoolURL is the endpoint the serving pool opens, a
	// transaction-mode pooler's where an installation has one. Both carry a
	// password and are never logged.
	DatabaseURL     string // EXAMEN_DATABASE_URL
	DatabasePoolURL string // EXAMEN_DATABASE_POOL_URL

	// OIDCIssuers are the issuers whose tokens are accepted, each without
	// a trailing slash. OIDCAudiences are the audiences a token may be
	// addressed to, the first the primary one.
	OIDCIssuers   []string // EXAMEN_OIDC_ISSUERS
	OIDCAudiences []string // EXAMEN_OIDC_AUDIENCE

	// AuthorizerURL is the endpoint that is asked what a caller may do,
	// and AuthorizerToken the bearer it requires, read once from the file
	// AuthorizerTokenFile names. With no URL the owner policy decides.
	AuthorizerURL       string // EXAMEN_AUTHORIZER_URL
	AuthorizerTokenFile string // EXAMEN_AUTHORIZER_TOKEN_FILE
	AuthorizerToken     Secret

	// MinterURL is the endpoint that issues owner tokens, with the bearer
	// read from MinterTokenFile.
	MinterURL       string // EXAMEN_MINTER_URL
	MinterTokenFile string // EXAMEN_MINTER_TOKEN_FILE
	MinterToken     Secret

	// SoleOwner is the one owner of an installation that receives that
	// owner's tokens from files in OwnerTokenDir instead of a minter.
	SoleOwner     string // EXAMEN_SOLE_OWNER
	OwnerTokenDir string // EXAMEN_OWNER_TOKEN_DIR

	// KeysURL is an installation's key endpoint, with the bearer read
	// from KeysTokenFile. With no URL, Examen creates its Keys at Lux.
	KeysURL       string // EXAMEN_KEYS_URL
	KeysTokenFile string // EXAMEN_KEYS_TOKEN_FILE
	KeysToken     Secret

	// The roots of the cores Examen composes; an empty one is a core this
	// installation has no address for.
	ToposURL         string // EXAMEN_TOPOS_URL
	CellaURL         string // EXAMEN_CELLA_URL
	CellaEnvironment string // EXAMEN_CELLA_ENVIRONMENT
	LuxURL           string // EXAMEN_LUX_URL
	ArcaURL          string // EXAMEN_ARCA_URL
	// ArcaPublicURL is where readers reach Arca; ArcaURL when unset.
	ArcaPublicURL string // EXAMEN_ARCA_PUBLIC_URL
	OrigoURL      string // EXAMEN_ORIGO_URL

	// The audiences an owner token for each core is asked for.
	ToposAudience string // EXAMEN_TOPOS_AUDIENCE
	CellaAudience string // EXAMEN_CELLA_AUDIENCE
	LuxAudience   string // EXAMEN_LUX_AUDIENCE
	ArcaAudience  string // EXAMEN_ARCA_AUDIENCE
	OrigoAudience string // EXAMEN_ORIGO_AUDIENCE

	// Workers is how many work items one replica runs at once.
	Workers int // EXAMEN_WORKERS

	// EventsURL is the installation's event sink, and EventsSecretFile the
	// file holding its signing secret, or two while one is rotated. The
	// file is reread when it changes, so only its path is kept.
	EventsURL        string // EXAMEN_EVENTS_URL
	EventsSecretFile string // EXAMEN_EVENTS_SECRET_FILE
}

// ServingURL is the URL the serving pool opens: the pooled one when it is
// set, and the direct one otherwise.
func (s Settings) ServingURL() string {
	if s.DatabasePoolURL != "" {
		return s.DatabasePoolURL
	}
	return s.DatabaseURL
}

// TokenSource names where owner tokens come from: the minter, the token
// files of the sole owner, or nowhere.
func (s Settings) TokenSource() string {
	switch {
	case s.MinterURL != "":
		return TokenSourceMinter
	case s.SoleOwner != "":
		return TokenSourceFiles
	}
	return TokenSourceNone
}

// KeySource names where Lux Keys come from: the installation's key
// endpoint, or Lux itself.
func (s Settings) KeySource() string {
	if s.KeysURL != "" {
		return KeySourceEndpoint
	}
	return KeySourceLux
}

// Cores lists the cores this installation has an address for, in the
// order of the table.
func (s Settings) Cores() []string {
	var out []string
	for _, c := range []struct{ name, url string }{
		{"topos", s.ToposURL}, {"cella", s.CellaURL}, {"lux", s.LuxURL}, {"arca", s.ArcaURL}, {"origo", s.OrigoURL},
	} {
		if c.url != "" {
			out = append(out, c.name)
		}
	}
	return out
}

// Secret is a value that is never printed: a formatted, logged or encoded
// Secret shows a placeholder.
type Secret struct{ value string }

// NewSecret wraps a value.
func NewSecret(v string) Secret { return Secret{value: v} }

// Reveal is the value, for the one call that sends it.
func (s Secret) Reveal() string { return s.value }

// Empty reports whether there is no value.
func (s Secret) Empty() bool { return s.value == "" }

const redacted = "[redacted]"

func (s Secret) String() string { return redacted }

// GoString keeps %#v from printing the value.
func (s Secret) GoString() string { return redacted }

// LogValue keeps a structured log line from printing the value.
func (s Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// FromEnv reads the settings `examend serve` runs with. getenv is
// os.Getenv, or a test's own. Every problem is reported, each naming its
// variable and never its value.
func FromEnv(getenv func(string) string) (Settings, error) {
	s := Settings{
		Addr: DefaultAddr, BasePath: DefaultBasePath, LogLevel: slog.LevelInfo,
		OIDCAudiences: []string{DefaultAudience},
		ToposAudience: DefaultToposAudience, CellaAudience: DefaultCellaAudience, LuxAudience: DefaultLuxAudience,
		ArcaAudience: DefaultArcaAudience, OrigoAudience: DefaultOrigoAudience,
		Workers: DefaultWorkers,
	}
	r := reader{getenv: getenv}

	if v := r.text("EXAMEN_ADDR"); v != "" {
		s.Addr = r.addr("EXAMEN_ADDR", v)
	}
	if v, ok := r.lookup("EXAMEN_BASE_PATH"); ok {
		s.BasePath = r.basePath("EXAMEN_BASE_PATH", v)
	}
	s.PublicURL = r.publicURL("EXAMEN_PUBLIC_URL")
	s.TrustedProxies = r.prefixes("EXAMEN_TRUSTED_PROXIES")
	s.LogLevel = r.level("EXAMEN_LOG_LEVEL")
	// The two database variables are read by name, here and nowhere else,
	// so the pooled endpoint and the direct one stay apart.
	s.DatabaseURL = r.database("EXAMEN_DATABASE_URL", getenv("EXAMEN_DATABASE_URL"))
	s.DatabasePoolURL = r.database("EXAMEN_DATABASE_POOL_URL", getenv("EXAMEN_DATABASE_POOL_URL"))
	s.OIDCIssuers = r.issuers("EXAMEN_OIDC_ISSUERS")
	if v := r.list("EXAMEN_OIDC_AUDIENCE"); len(v) > 0 {
		s.OIDCAudiences = v
	}
	s.AuthorizerURL = r.endpoint("EXAMEN_AUTHORIZER_URL")
	s.AuthorizerTokenFile = r.text("EXAMEN_AUTHORIZER_TOKEN_FILE")
	s.MinterURL = r.endpoint("EXAMEN_MINTER_URL")
	s.MinterTokenFile = r.text("EXAMEN_MINTER_TOKEN_FILE")
	s.SoleOwner = r.subject("EXAMEN_SOLE_OWNER")
	s.OwnerTokenDir = r.text("EXAMEN_OWNER_TOKEN_DIR")
	s.KeysURL = r.endpoint("EXAMEN_KEYS_URL")
	s.KeysTokenFile = r.text("EXAMEN_KEYS_TOKEN_FILE")
	s.ToposURL = r.endpoint("EXAMEN_TOPOS_URL")
	s.CellaURL = r.endpoint("EXAMEN_CELLA_URL")
	s.CellaEnvironment = r.text("EXAMEN_CELLA_ENVIRONMENT")
	s.LuxURL = r.endpoint("EXAMEN_LUX_URL")
	s.ArcaURL = r.endpoint("EXAMEN_ARCA_URL")
	s.ArcaPublicURL = r.endpoint("EXAMEN_ARCA_PUBLIC_URL")
	if s.ArcaPublicURL == "" {
		s.ArcaPublicURL = s.ArcaURL
	}
	s.OrigoURL = r.endpoint("EXAMEN_ORIGO_URL")
	r.name("EXAMEN_TOPOS_AUDIENCE", &s.ToposAudience)
	r.name("EXAMEN_CELLA_AUDIENCE", &s.CellaAudience)
	r.name("EXAMEN_LUX_AUDIENCE", &s.LuxAudience)
	r.name("EXAMEN_ARCA_AUDIENCE", &s.ArcaAudience)
	r.name("EXAMEN_ORIGO_AUDIENCE", &s.OrigoAudience)
	r.count("EXAMEN_WORKERS", &s.Workers)
	s.EventsURL = r.httpURL("EXAMEN_EVENTS_URL")
	s.EventsSecretFile = r.text("EXAMEN_EVENTS_SECRET_FILE")

	r.errs = append(r.errs, s.refusals()...)
	if len(r.errs) > 0 {
		return Settings{}, errors.Join(r.errs...)
	}
	if err := s.readFiles(); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// Database are the settings `examend migrate` runs with: the direct
// endpoint of the database and the log level, and nothing else, so a
// migration job runs with the database alone configured.
type Database struct {
	URL      string     // EXAMEN_DATABASE_URL
	LogLevel slog.Level // EXAMEN_LOG_LEVEL
}

// DatabaseFromEnv reads the settings of `examend migrate`.
func DatabaseFromEnv(getenv func(string) string) (Database, error) {
	r := reader{getenv: getenv}
	d := Database{
		URL:      r.database("EXAMEN_DATABASE_URL", getenv("EXAMEN_DATABASE_URL")),
		LogLevel: r.level("EXAMEN_LOG_LEVEL"),
	}
	if d.URL == "" && len(r.errs) == 0 {
		r.errs = append(r.errs, errDatabaseURL)
	}
	if len(r.errs) > 0 {
		return Database{}, errors.Join(r.errs...)
	}
	return d, nil
}

var errDatabaseURL = errors.New("EXAMEN_DATABASE_URL is not set: name the database this server keeps everything in")

// refusals are the cases of specs/011-distribution.md that stop the start
// and need nothing but the settings.
func (s Settings) refusals() []error {
	var errs []error
	if s.DatabaseURL == "" {
		errs = append(errs, errDatabaseURL)
	}
	if len(s.OIDCIssuers) == 0 {
		errs = append(errs, errors.New("EXAMEN_OIDC_ISSUERS is not set: name the issuers whose tokens this server accepts"))
	}
	pair := func(set bool, setName string, missing bool, missingName, why string) {
		if set && missing {
			errs = append(errs, fmt.Errorf("%s is set and %s is not: %s", setName, missingName, why))
		}
	}
	pair(s.AuthorizerURL != "", "EXAMEN_AUTHORIZER_URL", s.AuthorizerTokenFile == "", "EXAMEN_AUTHORIZER_TOKEN_FILE",
		"the authorizer takes a bearer, read from that file")
	pair(s.AuthorizerTokenFile != "", "EXAMEN_AUTHORIZER_TOKEN_FILE", s.AuthorizerURL == "", "EXAMEN_AUTHORIZER_URL",
		"name the endpoint the bearer is for")
	pair(s.MinterURL != "", "EXAMEN_MINTER_URL", s.MinterTokenFile == "", "EXAMEN_MINTER_TOKEN_FILE",
		"the minter takes a bearer, read from that file")
	pair(s.MinterTokenFile != "", "EXAMEN_MINTER_TOKEN_FILE", s.MinterURL == "", "EXAMEN_MINTER_URL",
		"name the endpoint the bearer is for")
	pair(s.SoleOwner != "", "EXAMEN_SOLE_OWNER", s.OwnerTokenDir == "", "EXAMEN_OWNER_TOKEN_DIR",
		"the sole owner's tokens are read from that directory")
	pair(s.OwnerTokenDir != "", "EXAMEN_OWNER_TOKEN_DIR", s.SoleOwner == "", "EXAMEN_SOLE_OWNER",
		"name the owner whose tokens the directory holds")
	pair(s.KeysURL != "", "EXAMEN_KEYS_URL", s.KeysTokenFile == "", "EXAMEN_KEYS_TOKEN_FILE",
		"a key endpoint takes a bearer, read from that file")
	pair(s.KeysTokenFile != "", "EXAMEN_KEYS_TOKEN_FILE", s.KeysURL == "", "EXAMEN_KEYS_URL",
		"a bearer with no endpoint means Examen would create its Keys at Lux while the installation believes it asks for them")
	pair(s.CellaURL != "", "EXAMEN_CELLA_URL", s.CellaEnvironment == "", "EXAMEN_CELLA_ENVIRONMENT",
		"name the Cella Environment the sandboxes run in")
	pair(s.CellaEnvironment != "", "EXAMEN_CELLA_ENVIRONMENT", s.CellaURL == "", "EXAMEN_CELLA_URL",
		"name the Cella the environment belongs to")
	pair(s.EventsURL != "", "EXAMEN_EVENTS_URL", s.EventsSecretFile == "", "EXAMEN_EVENTS_SECRET_FILE",
		"events would go unsigned")
	pair(s.EventsSecretFile != "", "EXAMEN_EVENTS_SECRET_FILE", s.EventsURL == "", "EXAMEN_EVENTS_URL",
		"a signing secret with no sink signs events nobody receives")
	if s.MinterURL != "" && s.SoleOwner != "" {
		errs = append(errs, errors.New("EXAMEN_MINTER_URL and EXAMEN_SOLE_OWNER are both set: two token sources would make it unclear whose token a request carries; keep one"))
	}
	// Each of these carries a bearer, an owner token or a Key, or, for
	// the events, what an owner's experiments did; over plain HTTP any of
	// them is readable on the way.
	for _, e := range []struct{ name, url string }{
		{"EXAMEN_AUTHORIZER_URL", s.AuthorizerURL}, {"EXAMEN_MINTER_URL", s.MinterURL}, {"EXAMEN_KEYS_URL", s.KeysURL},
		{"EXAMEN_TOPOS_URL", s.ToposURL}, {"EXAMEN_CELLA_URL", s.CellaURL}, {"EXAMEN_LUX_URL", s.LuxURL},
		{"EXAMEN_ARCA_URL", s.ArcaURL}, {"EXAMEN_ORIGO_URL", s.OrigoURL}, {"EXAMEN_EVENTS_URL", s.EventsURL},
		// Readers follow it with an Arca link token in the path. Unset, it
		// is EXAMEN_ARCA_URL, whose own row names the refusal.
		{"EXAMEN_ARCA_PUBLIC_URL", publicArca(s)},
	} {
		if e.url != "" && !secureOrLoopback(e.url) {
			errs = append(errs, fmt.Errorf("%s is not https and its host is not loopback: what it carries would be readable on the way", e.name))
		}
	}
	return errs
}

// publicArca is EXAMEN_ARCA_PUBLIC_URL when it names an address of its own.
func publicArca(s Settings) string {
	if s.ArcaPublicURL == s.ArcaURL {
		return ""
	}
	return s.ArcaPublicURL
}

// readFiles reads every token file the settings name and checks the
// owner token directory and the events secret, once, at start. An error
// names the variable, never the file's content.
func (s *Settings) readFiles() error {
	var errs []error
	for _, f := range []struct {
		name, path string
		into       *Secret
	}{
		{"EXAMEN_AUTHORIZER_TOKEN_FILE", s.AuthorizerTokenFile, &s.AuthorizerToken},
		{"EXAMEN_MINTER_TOKEN_FILE", s.MinterTokenFile, &s.MinterToken},
		{"EXAMEN_KEYS_TOKEN_FILE", s.KeysTokenFile, &s.KeysToken},
	} {
		if f.path == "" {
			continue
		}
		token, err := ReadToken(f.name, f.path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		*f.into = token
	}
	if s.EventsSecretFile != "" {
		if _, err := ReadSecrets("EXAMEN_EVENTS_SECRET_FILE", s.EventsSecretFile); err != nil {
			errs = append(errs, err)
		}
	}
	if s.OwnerTokenDir != "" {
		if fi, err := os.Stat(s.OwnerTokenDir); err != nil || !fi.IsDir() {
			errs = append(errs, errors.New("EXAMEN_OWNER_TOKEN_DIR names no directory that can be read"))
		}
	}
	return errors.Join(errs...)
}

// ReadToken reads a file that holds one bearer. The error names the
// variable, never the file's content.
func ReadToken(name, path string) (Secret, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Secret{}, fmt.Errorf("%s names a file that cannot be read", name)
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return Secret{}, fmt.Errorf("%s names an empty file", name)
	}
	return NewSecret(token), nil
}

// ReadSecrets reads a file that holds one signing secret, or two separated
// by a comma while one is rotated. The error names the variable, never the
// file's content.
func ReadSecrets(name, path string) ([]Secret, error) {
	token, err := ReadToken(name, path)
	if err != nil {
		return nil, err
	}
	entries := strings.Split(token.Reveal(), ",")
	if len(entries) > 2 {
		return nil, fmt.Errorf("%s names a file with more than two secrets", name)
	}
	out := make([]Secret, 0, len(entries))
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			return nil, fmt.Errorf("%s names a file with an empty secret", name)
		}
		out = append(out, NewSecret(e))
	}
	return out, nil
}

// reader reads variables and collects what does not parse.
type reader struct {
	getenv func(string) string
	errs   []error
}

func (r *reader) fail(name, why string) { r.errs = append(r.errs, fmt.Errorf("%s %s", name, why)) }

// lookup is a variable's value, trimmed, and whether it was set at all.
func (r *reader) lookup(name string) (string, bool) {
	raw := r.getenv(name)
	return strings.TrimSpace(raw), raw != ""
}

func (r *reader) text(name string) string {
	v, _ := r.lookup(name)
	return v
}

// list is a comma-separated list, each entry trimmed and empty ones
// dropped.
func (r *reader) list(name string) []string {
	var out []string
	for entry := range strings.SplitSeq(r.text(name), ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			out = append(out, entry)
		}
	}
	return out
}

func (r *reader) addr(name, v string) string {
	if _, _, err := net.SplitHostPort(v); err != nil {
		r.fail(name, "is not a host and a port, such as :8080")
		return DefaultAddr
	}
	return v
}

func (r *reader) count(name string, into *int) {
	v := r.text(name)
	if v == "" {
		return
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		r.fail(name, "is not a whole number above zero")
		return
	}
	*into = n
}

// name reads a value that may hold no space or comma, such as an audience.
func (r *reader) name(name string, into *string) {
	v := r.text(name)
	if v == "" {
		return
	}
	if strings.ContainsAny(v, " ,\t") {
		r.fail(name, "is not one name with no space or comma")
		return
	}
	*into = v
}

func (r *reader) level(name string) slog.Level {
	switch r.text(name) {
	case "", "info":
		return slog.LevelInfo
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	r.fail(name, "is not debug, info, warn or error")
	return slog.LevelInfo
}

// database checks a database URL's shape. The parse error is not kept: it
// quotes the URL, which holds a password.
func (r *reader) database(name, raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		r.fail(name, "is not a postgres:// URL with a host")
		return ""
	}
	return v
}

// absolute parses an absolute http or https URL with a host and no user
// part. The parse error is not kept: it quotes the value.
func absolute(v string) (*url.URL, bool) {
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Opaque != "" {
		return nil, false
	}
	return u, true
}

func (r *reader) httpURL(name string) string {
	v := r.text(name)
	if v == "" {
		return ""
	}
	if _, ok := absolute(v); !ok {
		r.fail(name, "is not an http or https URL with a host and no user part")
		return ""
	}
	return v
}

// endpoint is the root of a service Examen calls, with no trailing slash,
// so the service's own paths join it.
func (r *reader) endpoint(name string) string {
	v := r.text(name)
	if v == "" {
		return ""
	}
	u, ok := absolute(v)
	if !ok || u.RawQuery != "" || u.Fragment != "" {
		r.fail(name, "is not an http or https URL with a host, and no user part, query or fragment")
		return ""
	}
	return strings.TrimRight(v, "/")
}

func (r *reader) publicURL(name string) string { return r.endpoint(name) }

func (r *reader) basePath(name, v string) string {
	v = strings.TrimRight(v, "/")
	if v != "" && (!strings.HasPrefix(v, "/") || strings.ContainsAny(v, "?#{} ") || strings.Contains(v, "//")) {
		r.fail(name, "is not a path that begins with a slash")
		return DefaultBasePath
	}
	return v
}

func (r *reader) issuers(name string) []string {
	var out []string
	for _, v := range r.list(name) {
		u, ok := absolute(v)
		if !ok || u.RawQuery != "" || u.Fragment != "" {
			r.fail(name, "has an entry that is not an http or https URL with a host")
			return nil
		}
		issuer := strings.TrimRight(v, "/")
		if slices.Contains(out, issuer) {
			r.fail(name, "lists an issuer twice")
			return nil
		}
		out = append(out, issuer)
	}
	return out
}

func (r *reader) prefixes(name string) []netip.Prefix {
	var out []netip.Prefix
	for i, v := range r.list(name) {
		p, err := netip.ParsePrefix(v)
		if err != nil {
			r.fail(name, fmt.Sprintf("has an entry at position %d that is not an address range in CIDR form", i+1))
			return nil
		}
		out = append(out, p.Masked())
	}
	return out
}

func (r *reader) subject(name string) string {
	v := r.text(name)
	if v == "" {
		return ""
	}
	issuer, sub, ok := authz.SplitSubject(v)
	if !ok || issuer == "" || sub == "" {
		r.fail(name, "is not a rendered subject, an issuer and a sub joined by |")
		return ""
	}
	return v
}

// secureOrLoopback reports whether a URL is https, or names a loopback
// host.
func secureOrLoopback(raw string) bool {
	u, ok := absolute(raw)
	if !ok {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// LookupEnv reads a variable that is not Examen's own configuration, such
// as the container engine a test helper reaches. It is the one read of the
// environment outside FromEnv and DatabaseFromEnv.
func LookupEnv(name string) (string, bool) { return os.LookupEnv(name) }

// Variables lists every variable FromEnv reads, in the order it reads
// them.
func Variables() []string {
	var names []string
	seen := map[string]bool{}
	record := func(name string) string {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
		return ""
	}
	// The read fails on the missing required variables, which is what an
	// empty environment is; the names it asked for are the answer.
	_, _ = FromEnv(record)
	_, _ = DatabaseFromEnv(record)
	return names
}
