// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bufio"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// env is a test's environment.
type env map[string]string

func (e env) get(name string) string { return e[name] }

// minimal is the smallest environment a server starts with.
func minimal() env {
	return env{
		"EXAMEN_DATABASE_URL": "postgres://examen@db.example.com/examen",
		"EXAMEN_OIDC_ISSUERS": "https://issuer.example.com",
	}
}

// file writes a file of a test's own and answers its path.
func file(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The table of specs/011-distribution.md, and the reference of
// docs/configuration.md an operator reads, each list every variable the
// binary reads and none it does not.
func TestTableMatchesWhatIsRead(t *testing.T) {
	read := Variables()
	slices.Sort(read)
	for _, doc := range []string{
		filepath.Join("..", "..", "specs", "011-distribution.md"),
		filepath.Join("..", "..", "docs", "configuration.md"),
	} {
		table := variablesListed(t, doc)
		slices.Sort(table)
		if len(table) < 30 || !slices.Equal(table, read) {
			t.Errorf("the table of %s lists\n  %v\nand the binary reads\n  %v", doc, table, read)
		}
	}
}

// variablesListed is the variables a markdown table names, one per row
// that begins with the variable in code.
func variablesListed(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	row := regexp.MustCompile("^\\| `(EXAMEN_[A-Z_]+)` \\|")
	var table []string
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		if m := row.FindStringSubmatch(scan.Text()); m != nil {
			table = append(table, m[1])
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	return table
}

// A missing required variable and a value that does not parse each stop
// the start with the variable's name and never its value.
func TestErrorsNameTheVariableOnly(t *testing.T) {
	const marker = "canary-value-0042"
	cases := []struct {
		name, variable, value string
	}{
		{"the address", "EXAMEN_ADDR", marker},
		{"the base path", "EXAMEN_BASE_PATH", marker},
		{"the public url", "EXAMEN_PUBLIC_URL", "ftp://" + marker},
		{"a public url with a query", "EXAMEN_PUBLIC_URL", "https://examen.example.com/?" + marker},
		{"a proxy range", "EXAMEN_TRUSTED_PROXIES", "10.0.0.0/8," + marker},
		{"the log level", "EXAMEN_LOG_LEVEL", marker},
		{"the database", "EXAMEN_DATABASE_URL", "mysql://user:" + marker + "@db.example.com/x"},
		{"a database url that is not one", "EXAMEN_DATABASE_URL", "postgres://%zz" + marker},
		{"the pool", "EXAMEN_DATABASE_POOL_URL", "postgres:///" + marker},
		{"an issuer", "EXAMEN_OIDC_ISSUERS", marker},
		{"the authorizer", "EXAMEN_AUTHORIZER_URL", "mailto:" + marker},
		{"the minter", "EXAMEN_MINTER_URL", "https://minter.example.com/#" + marker},
		{"the sole owner", "EXAMEN_SOLE_OWNER", marker},
		{"a sole owner with no sub", "EXAMEN_SOLE_OWNER", "https://issuer.example.com|"},
		{"the key endpoint", "EXAMEN_KEYS_URL", marker},
		{"topos", "EXAMEN_TOPOS_URL", marker},
		{"cella", "EXAMEN_CELLA_URL", marker},
		{"lux", "EXAMEN_LUX_URL", "https://user:" + marker + "@lux.example.com"},
		{"arca", "EXAMEN_ARCA_URL", marker},
		{"arca's public address", "EXAMEN_ARCA_PUBLIC_URL", marker},
		{"origo", "EXAMEN_ORIGO_URL", marker},
		{"the file store declaration", "EXAMEN_FILESTORE_DOWNLOAD_ONLY", marker},
		{"an audience", "EXAMEN_TOPOS_AUDIENCE", "two " + marker},
		{"another audience", "EXAMEN_ARCA_AUDIENCE", "a," + marker},
		{"the workers", "EXAMEN_WORKERS", marker},
		{"no workers", "EXAMEN_WORKERS", "0"},
		{"the event sink", "EXAMEN_EVENTS_URL", marker},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := minimal()
			e[tc.variable] = tc.value
			_, err := FromEnv(e.get)
			if err == nil {
				t.Fatalf("%s=%q was accepted", tc.variable, tc.value)
			}
			if !strings.Contains(err.Error(), tc.variable) {
				t.Errorf("the error does not name %s: %v", tc.variable, err)
			}
			if strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), tc.value) {
				t.Errorf("the error repeats the value: %v", err)
			}
		})
	}
	for _, required := range []string{"EXAMEN_DATABASE_URL", "EXAMEN_OIDC_ISSUERS"} {
		e := minimal()
		delete(e, required)
		if _, err := FromEnv(e.get); err == nil || !strings.Contains(err.Error(), required) {
			t.Errorf("without %s: %v", required, err)
		}
	}
}

// The defaults are the table's.
func TestDefaults(t *testing.T) {
	s, err := FromEnv(minimal().get)
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{
		Addr: ":8080", BasePath: "/v1", LogLevel: slog.LevelInfo,
		DatabaseURL: "postgres://examen@db.example.com/examen",
		OIDCIssuers: []string{"https://issuer.example.com"}, OIDCAudiences: []string{"examen"},
		ToposAudience: "topos", CellaAudience: "cella", LuxAudience: "lux", ArcaAudience: "arca", OrigoAudience: "origo",
		Workers: 20,
	}
	if fmt.Sprintf("%#v", s) != fmt.Sprintf("%#v", want) {
		t.Fatalf("defaults\n got %#v\nwant %#v", s, want)
	}
	if s.ServingURL() != s.DatabaseURL || s.TokenSource() != TokenSourceNone || s.KeySource() != KeySourceNone || len(s.Cores()) != 0 {
		t.Errorf("derived: %q %q %q %v", s.ServingURL(), s.TokenSource(), s.KeySource(), s.Cores())
	}
}

// Every variable set, read into its field.
func TestEveryVariableIsRead(t *testing.T) {
	authorizerToken := file(t, "authorizer", "authorizer-bearer\n")
	minterToken := file(t, "minter", "minter-bearer")
	keysToken := file(t, "keys", " keys-bearer ")
	secrets := file(t, "events", "one,two")
	e := env{
		"EXAMEN_ADDR":                    "127.0.0.1:9000",
		"EXAMEN_BASE_PATH":               "/api/",
		"EXAMEN_PUBLIC_URL":              "https://examen.example.com/prefix/",
		"EXAMEN_TRUSTED_PROXIES":         "10.1.2.3/8, fd00::/8",
		"EXAMEN_LOG_LEVEL":               "debug",
		"EXAMEN_DATABASE_URL":            "postgres://examen@db.example.com/examen",
		"EXAMEN_DATABASE_POOL_URL":       "postgresql://examen@pool.example.com/examen",
		"EXAMEN_OIDC_ISSUERS":            "https://a.example.com/, https://b.example.com",
		"EXAMEN_OIDC_AUDIENCE":           "examen, api.example.com",
		"EXAMEN_AUTHORIZER_URL":          "https://authorizer.example.com/decide",
		"EXAMEN_AUTHORIZER_TOKEN_FILE":   authorizerToken,
		"EXAMEN_MINTER_URL":              "http://127.0.0.1:7000/",
		"EXAMEN_MINTER_TOKEN_FILE":       minterToken,
		"EXAMEN_KEYS_URL":                "http://localhost:7001",
		"EXAMEN_KEYS_TOKEN_FILE":         keysToken,
		"EXAMEN_TOPOS_URL":               "https://topos.example.com",
		"EXAMEN_CELLA_URL":               "https://cella.example.com",
		"EXAMEN_CELLA_ENVIRONMENT":       "env-queued",
		"EXAMEN_LUX_URL":                 "https://lux.example.com",
		"EXAMEN_ARCA_URL":                "http://[::1]:7002",
		"EXAMEN_ARCA_PUBLIC_URL":         "https://files.example.com",
		"EXAMEN_FILESTORE_DOWNLOAD_ONLY": "true",
		"EXAMEN_ORIGO_URL":               "https://origo.example.com",
		"EXAMEN_TOPOS_AUDIENCE":          "t", "EXAMEN_CELLA_AUDIENCE": "c", "EXAMEN_LUX_AUDIENCE": "l",
		"EXAMEN_ARCA_AUDIENCE": "a", "EXAMEN_ORIGO_AUDIENCE": "o",
		"EXAMEN_WORKERS":            "3",
		"EXAMEN_EVENTS_URL":         "https://sink.example.com/events",
		"EXAMEN_EVENTS_SECRET_FILE": secrets,
	}
	s, err := FromEnv(e.get)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case s.Addr != "127.0.0.1:9000", s.BasePath != "/api", s.PublicURL != "https://examen.example.com/prefix":
		t.Errorf("addresses: %q %q %q", s.Addr, s.BasePath, s.PublicURL)
	case !slices.Equal(s.TrustedProxies, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fd00::/8")}):
		t.Errorf("proxies %v", s.TrustedProxies)
	case s.LogLevel != slog.LevelDebug:
		t.Errorf("level %v", s.LogLevel)
	case s.ServingURL() != e["EXAMEN_DATABASE_POOL_URL"]:
		t.Errorf("the serving URL is not the pooled one")
	case !slices.Equal(s.OIDCIssuers, []string{"https://a.example.com", "https://b.example.com"}):
		t.Errorf("issuers %v", s.OIDCIssuers)
	case !slices.Equal(s.OIDCAudiences, []string{"examen", "api.example.com"}):
		t.Errorf("audiences %v", s.OIDCAudiences)
	case s.AuthorizerToken.Reveal() != "authorizer-bearer", s.MinterToken.Reveal() != "minter-bearer", s.KeysToken.Reveal() != "keys-bearer":
		t.Errorf("the token files were not read")
	case s.MinterURL != "http://127.0.0.1:7000" || s.TokenSource() != TokenSourceMinter || s.KeySource() != KeySourceEndpoint:
		t.Errorf("sources: %q %q %q", s.MinterURL, s.TokenSource(), s.KeySource())
	case !slices.Equal(s.Cores(), []string{"topos", "cella", "lux", "arca", "origo"}):
		t.Errorf("cores %v", s.Cores())
	case s.CellaEnvironment != "env-queued" || s.ArcaPublicURL != "https://files.example.com" || !s.FileStoreDownloadOnly:
		t.Errorf("cella and arca: %q %q", s.CellaEnvironment, s.ArcaPublicURL)
	case s.ToposAudience != "t" || s.CellaAudience != "c" || s.LuxAudience != "l" || s.ArcaAudience != "a" || s.OrigoAudience != "o":
		t.Errorf("core audiences %+v", s)
	case s.Workers != 3 || s.EventsURL == "" || s.EventsSecretFile != secrets:
		t.Errorf("workers and events %+v", s)
	}

	// A sole owner with its token directory, and Arca's public address
	// defaulting to Arca's own.
	owner := minimal()
	maps.Copy(owner, env{
		"EXAMEN_SOLE_OWNER": "https://issuer.example.com|alice", "EXAMEN_OWNER_TOKEN_DIR": t.TempDir(),
		"EXAMEN_ARCA_URL": "https://arca.example.com", "EXAMEN_BASE_PATH": "/", "EXAMEN_LOG_LEVEL": "warn",
	})
	if s, err = FromEnv(owner.get); err != nil {
		t.Fatal(err)
	}
	if s.TokenSource() != TokenSourceNone || s.ArcaPublicURL != "https://arca.example.com" || s.BasePath != "" || s.LogLevel != slog.LevelWarn || s.FileStoreDownloadOnly {
		t.Errorf("sole owner: %q %q %q %v", s.TokenSource(), s.ArcaPublicURL, s.BasePath, s.LogLevel)
	}
	owner["EXAMEN_LOG_LEVEL"] = "error"
	if s, err = FromEnv(owner.get); err != nil || s.LogLevel != slog.LevelError {
		t.Errorf("error level: %v %v", s.LogLevel, err)
	}
}

// The refusals at start that need the settings and their files alone.
func TestRefusals(t *testing.T) {
	empty := file(t, "empty", "\n")
	token := file(t, "token", "a-bearer")
	missing := filepath.Join(t.TempDir(), "none")
	notDir := file(t, "file", "x")
	cases := []struct {
		name  string
		set   env
		names string
	}{
		{"an authorizer and no token file", env{"EXAMEN_AUTHORIZER_URL": "https://a.example.com"}, "EXAMEN_AUTHORIZER_TOKEN_FILE"},
		{"a token file and no authorizer", env{"EXAMEN_AUTHORIZER_TOKEN_FILE": token}, "EXAMEN_AUTHORIZER_URL"},
		{"a missing token file", env{"EXAMEN_AUTHORIZER_URL": "https://a.example.com", "EXAMEN_AUTHORIZER_TOKEN_FILE": missing}, "EXAMEN_AUTHORIZER_TOKEN_FILE"},
		{"an empty token file", env{"EXAMEN_AUTHORIZER_URL": "https://a.example.com", "EXAMEN_AUTHORIZER_TOKEN_FILE": empty}, "EXAMEN_AUTHORIZER_TOKEN_FILE"},
		{"a minter and no token file", env{"EXAMEN_MINTER_URL": "https://m.example.com"}, "EXAMEN_MINTER_TOKEN_FILE"},
		{"a minter token file and no minter", env{"EXAMEN_MINTER_TOKEN_FILE": token}, "EXAMEN_MINTER_URL"},
		{"an empty minter token file", env{"EXAMEN_MINTER_URL": "https://m.example.com", "EXAMEN_MINTER_TOKEN_FILE": empty}, "EXAMEN_MINTER_TOKEN_FILE"},
		{"a minter and a sole owner", env{
			"EXAMEN_MINTER_URL": "https://m.example.com", "EXAMEN_MINTER_TOKEN_FILE": token,
			"EXAMEN_SOLE_OWNER": "https://issuer.example.com|alice", "EXAMEN_OWNER_TOKEN_DIR": t.TempDir(),
		}, "EXAMEN_MINTER_URL and EXAMEN_SOLE_OWNER"},
		{"a sole owner and no directory", env{"EXAMEN_SOLE_OWNER": "https://issuer.example.com|alice"}, "EXAMEN_OWNER_TOKEN_DIR"},
		{"a directory and no sole owner", env{"EXAMEN_OWNER_TOKEN_DIR": t.TempDir()}, "EXAMEN_SOLE_OWNER"},
		{"a directory that is a file", env{"EXAMEN_SOLE_OWNER": "https://issuer.example.com|alice", "EXAMEN_OWNER_TOKEN_DIR": notDir}, "EXAMEN_OWNER_TOKEN_DIR"},
		{"a key endpoint and no token file", env{"EXAMEN_KEYS_URL": "https://k.example.com"}, "EXAMEN_KEYS_TOKEN_FILE"},
		{"a key token file and no endpoint", env{"EXAMEN_KEYS_TOKEN_FILE": token}, "EXAMEN_KEYS_URL"},
		{"an unreadable key token file", env{"EXAMEN_KEYS_URL": "https://k.example.com", "EXAMEN_KEYS_TOKEN_FILE": missing}, "EXAMEN_KEYS_TOKEN_FILE"},
		{"cella and no environment", env{"EXAMEN_CELLA_URL": "https://c.example.com"}, "EXAMEN_CELLA_ENVIRONMENT"},
		{"an environment and no cella", env{"EXAMEN_CELLA_ENVIRONMENT": "e"}, "EXAMEN_CELLA_URL"},
		{"a sink and no secret", env{"EXAMEN_EVENTS_URL": "https://s.example.com"}, "EXAMEN_EVENTS_SECRET_FILE"},
		{"a secret and no sink", env{"EXAMEN_EVENTS_SECRET_FILE": token}, "EXAMEN_EVENTS_URL"},
		{"an empty secret file", env{"EXAMEN_EVENTS_URL": "https://s.example.com", "EXAMEN_EVENTS_SECRET_FILE": empty}, "EXAMEN_EVENTS_SECRET_FILE"},
		{"three secrets", env{"EXAMEN_EVENTS_URL": "https://s.example.com", "EXAMEN_EVENTS_SECRET_FILE": file(t, "three", "a,b,c")}, "more than two"},
		{"an empty second secret", env{"EXAMEN_EVENTS_URL": "https://s.example.com", "EXAMEN_EVENTS_SECRET_FILE": file(t, "half", "a,")}, "empty secret"},
		{"an http sink elsewhere", env{"EXAMEN_EVENTS_URL": "http://sink.example.com", "EXAMEN_EVENTS_SECRET_FILE": token}, "EXAMEN_EVENTS_URL is not https"},
		{"an http authorizer elsewhere", env{"EXAMEN_AUTHORIZER_URL": "http://a.example.com", "EXAMEN_AUTHORIZER_TOKEN_FILE": token}, "EXAMEN_AUTHORIZER_URL is not https"},
		{"an http minter elsewhere", env{"EXAMEN_MINTER_URL": "http://10.0.0.1", "EXAMEN_MINTER_TOKEN_FILE": token}, "EXAMEN_MINTER_URL is not https"},
		{"an http key endpoint elsewhere", env{"EXAMEN_KEYS_URL": "http://k.example.com", "EXAMEN_KEYS_TOKEN_FILE": token}, "EXAMEN_KEYS_URL is not https"},
		{"an http core elsewhere", env{"EXAMEN_LUX_URL": "http://lux.example.com"}, "EXAMEN_LUX_URL is not https"},
		{"an http arca elsewhere", env{"EXAMEN_ARCA_URL": "http://arca.example.com"}, "EXAMEN_ARCA_URL is not https"},
		{"an http topos elsewhere", env{"EXAMEN_TOPOS_URL": "http://topos.example.com"}, "EXAMEN_TOPOS_URL is not https"},
		{"an http cella elsewhere", env{"EXAMEN_CELLA_URL": "http://cella.example.com", "EXAMEN_CELLA_ENVIRONMENT": "e"}, "EXAMEN_CELLA_URL is not https"},
		{"an http origo elsewhere", env{"EXAMEN_ORIGO_URL": "http://origo.example.com"}, "EXAMEN_ORIGO_URL is not https"},
		{"an http issuer elsewhere", env{"EXAMEN_OIDC_ISSUERS": "https://a.example.com, http://issuer.example.com"}, "EXAMEN_OIDC_ISSUERS names an issuer that is not https"},
		{"an http public arca elsewhere", env{"EXAMEN_ARCA_URL": "https://arca.example.com", "EXAMEN_ARCA_PUBLIC_URL": "http://files.example.com"}, "EXAMEN_ARCA_PUBLIC_URL is not https"},
		{"an issuer twice", env{"EXAMEN_OIDC_ISSUERS": "https://a.example.com, https://a.example.com/"}, "EXAMEN_OIDC_ISSUERS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := minimal()
			maps.Copy(e, tc.set)
			_, err := FromEnv(e.get)
			if err == nil || !strings.Contains(err.Error(), tc.names) {
				t.Fatalf("want a refusal naming %s, got %v", tc.names, err)
			}
			if strings.Contains(err.Error(), "a-bearer") {
				t.Fatalf("the refusal repeats a file's content: %v", err)
			}
		})
	}
	// Plain HTTP to a loopback host, by name or address, is the one that
	// stays on the machine.
	local := minimal()
	maps.Copy(local, env{
		"EXAMEN_OIDC_ISSUERS": "http://127.0.0.1:5556/dex, http://localhost:9/issuer, http://[::1]:9",
		"EXAMEN_TOPOS_URL":    "http://localhost:1", "EXAMEN_CELLA_URL": "http://127.0.0.1:2", "EXAMEN_CELLA_ENVIRONMENT": "e",
		"EXAMEN_LUX_URL": "http://[::1]:3", "EXAMEN_ARCA_URL": "http://localhost:4", "EXAMEN_ORIGO_URL": "http://127.0.0.1:5",
	})
	if _, err := FromEnv(local.get); err != nil {
		t.Errorf("plain HTTP to loopback hosts: %v", err)
	}
	// An insecure Arca is named once, under its own variable.
	e := minimal()
	e["EXAMEN_ARCA_URL"] = "http://arca.example.com"
	if _, err := FromEnv(e.get); err == nil || strings.Contains(err.Error(), "EXAMEN_ARCA_PUBLIC_URL") {
		t.Errorf("an insecure Arca with no public address: %v", err)
	}
}

// migrate reads the database and the log level, and nothing else.
func TestDatabaseFromEnv(t *testing.T) {
	d, err := DatabaseFromEnv(env{"EXAMEN_DATABASE_URL": "postgres://examen@db.example.com/examen", "EXAMEN_LOG_LEVEL": "debug"}.get)
	if err != nil || d.URL == "" || d.LogLevel != slog.LevelDebug {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := DatabaseFromEnv(env{}.get); err == nil || !strings.Contains(err.Error(), "EXAMEN_DATABASE_URL is not set") {
		t.Errorf("no database: %v", err)
	}
	if _, err := DatabaseFromEnv(env{"EXAMEN_DATABASE_URL": "nope"}.get); err == nil || strings.Contains(err.Error(), "nope") ||
		strings.Contains(err.Error(), "is not set") {
		t.Errorf("a database that does not parse: %v", err)
	}
	var asked []string
	if _, err := DatabaseFromEnv(func(name string) string { asked = append(asked, name); return "" }); err == nil {
		t.Error("an empty environment was accepted")
	}
	slices.Sort(asked)
	if !slices.Equal(slices.Compact(asked), []string{"EXAMEN_DATABASE_URL", "EXAMEN_LOG_LEVEL"}) {
		t.Errorf("migrate read %v", asked)
	}
}

// Two secrets while one is rotated, each trimmed.
func TestReadSecrets(t *testing.T) {
	got, err := ReadSecrets("X", file(t, "s", " one , two \n"))
	if err != nil || len(got) != 2 || got[0].Reveal() != "one" || got[1].Reveal() != "two" {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := ReadSecrets("X", filepath.Join(t.TempDir(), "none")); err == nil || !strings.HasPrefix(err.Error(), "X ") {
		t.Errorf("a missing file: %v", err)
	}
}

// A variable that is not Examen's own is read at use.
func TestLookupEnv(t *testing.T) {
	t.Setenv("EXAMEN_TEST_LOOKUP", "value")
	if v, ok := LookupEnv("EXAMEN_TEST_LOOKUP"); !ok || v != "value" {
		t.Fatalf("%q %v", v, ok)
	}
}

// A secret never prints.
func TestSecretsDoNotPrint(t *testing.T) {
	s := NewSecret("the-bearer-value")
	for _, out := range []string{
		fmt.Sprint(s), fmt.Sprintf("%v %+v %#v %s %x", s, s, s, s, s), s.LogValue().String(),
	} {
		if strings.Contains(out, "the-bearer-value") {
			t.Fatalf("a secret printed: %s", out)
		}
	}
	var b strings.Builder
	slog.New(slog.NewJSONHandler(&b, nil)).Info("x", "token", s)
	if strings.Contains(b.String(), "the-bearer-value") {
		t.Fatalf("a secret was logged: %s", b.String())
	}
	if s.Empty() || !NewSecret("").Empty() {
		t.Error("Empty")
	}
}
