package notices

import (
	"os"
	"strings"
	"testing"
)

// The messages and table below are carried over from
// archive/go-port:internal/audit/annotations_test.go. Every expected value
// there was the output of the Python original (muda.audit.annotations
// .signature at main@80b76e6) on the same message.
const (
	node20A = "Node.js 20 is deprecated. The following actions target Node.js 20 but are being " +
		"forced to run on Node.js 24: actions/cache@v4, actions/checkout@v4, " +
		"actions/setup-python@v5, aws-actions/configure-aws-credentials@v4. For more " +
		"information see: https://github.blog/changelog/2025-09-19-deprecation-of-node-20-" +
		"on-github-actions-runners/"
	// Same warning, later scan with bumped action majors: must collapse to one signature.
	node20B = "Node.js 20 is deprecated. The following actions target Node.js 20 but are being " +
		"forced to run on Node.js 24: actions/cache@v6, actions/checkout@v7, " +
		"actions/setup-python@v7, aws-actions/configure-aws-credentials@v6. For more " +
		"information see: https://github.blog/changelog/2025-09-19-deprecation-of-node-20-" +
		"on-github-actions-runners/"
	appID      = "Input 'app-id' has been deprecated with message: Use 'client-id' instead."
	caFallback = "no usable prod CA read role (CODEARTIFACT_ROLE_ARN_PROD) — prod presence is " +
		"asserted via the Publish run's success (its prod twine upload is part of the same " +
		"atomic job)."
	caFallbackAlt = "no usable CODEARTIFACT_ROLE_ARN — trusting the green Publish run (its twine upload " +
		"would have failed on a build/upload problem)."
	ubuntu = `"The ubuntu-latest label will migrate to Ubuntu 26 beginning October 19, 2026. ` +
		`For more information, see https://github.com/actions/runner-images/issues/14748"`
)

func TestSignatureCollapsesActionMajors(t *testing.T) {
	if Signature(node20A) != Signature(node20B) {
		t.Error("node20 A/B differ")
	}
}

func TestSignatureDistinctWarningsDoNotCollapse(t *testing.T) {
	sigs := map[string]bool{}
	for _, m := range []string{node20A, appID, caFallback, caFallbackAlt, ubuntu} {
		sigs[Signature(m)] = true
	}
	if len(sigs) != 5 {
		t.Errorf("got %d distinct signatures", len(sigs))
	}
}

func TestSignatureCollapsesSemverAndDates(t *testing.T) {
	if Signature("runner version 2.337.0 released") != Signature("runner version 2.320.1 released") {
		t.Error("semver")
	}
	if Signature("built on 2026-09-19") != Signature("built on 2026-01-02") {
		t.Error("dates")
	}
}

func TestSignatureCollapsesHexAndPaths(t *testing.T) {
	if Signature("sha 69f01c4d6f2776f51ac25bab0cf7948f88a6ad57 done") != Signature("sha 0011223344556677889900aabbccddeeff001122 done") {
		t.Error("hex")
	}
	if Signature("wrote /home/runner/work/lib-b/out.txt") != Signature("wrote /home/ci/build/other.txt") {
		t.Error("paths")
	}
}

func TestSignatureCollapsesWhitespaceAndTruncates(t *testing.T) {
	if got := Signature("a   b\n\tc"); got != "a b c" {
		t.Errorf("got %q", got)
	}
	if got := Signature(strings.Repeat("x ", 500)); len([]rune(got)) > 200 {
		t.Errorf("len %d", len([]rune(got)))
	}
}

// TestSignatureMatchesPort is the port's TestSignatureMatchesPython table,
// unchanged: the normaliser must stay byte-for-byte identical.
func TestSignatureMatchesPort(t *testing.T) {
	cases := []struct{ msg, want string }{
		{"https://github.com/actions/runner-images/issues/14748", "https:/<path>"},
		{"see http://example.com/a/b.html for details", "see http:/<path> for details"},
		{"/home/runner/work/x.txt is missing", "<path> is missing"},
		{"/", "/"},
		{"a/b/c relative path", "a/b/c relative path"},
		{"file:///etc/passwd", "file:/<path>"},
		{"C:/Users/x/file", "C:/Users/x/file"},
		{"path=/opt/hostedtoolcache/node/24.21.0/x64", "path=<path>"},
		{"(/usr/bin/git) exited", "(<path>) exited"},
		{"a //double slash", "a <path> slash"},
		{"node 20.19.4 and v24", "node <v> and <v>"},
		{"1.2.3.4 and 10.0", "<v> and <v>"},
		{"abc2.3 x1v2 dev1.2", "abc<v> x1<v> de<v>"},
		{"@v4 actions/checkout@v4.2.1 @2.337.0", "<v> actions/checkout<v> <v>"},
		{"version 20 and 24", "version 20 and 24"},
		{"built 2026-09-19T12:34:56.789Z done", "built <date> done"},
		{"2026-09-19 12:34:56+00:00 then", "<date> then"},
		{"on 2026-09-19and", "on <date>and"},
		{"20260919 not a date", "20260919 not a date"},
		{"sha deadbeefdeadbeef done", "sha <hex> done"},
		{"short abcdef12345 hex", "short abcdef12345 hex"},
		{"é0123456789abcdef é", "é0123456789abcdef é"},
		{"0123456789abcdefg", "0123456789abcdefg"},
		{"x_0123456789abcdef", "x_0123456789abcdef"},
		{"-0123456789abcdef-", "-<hex>-"},
		{"ver ١٢.٣ arabic", "ver <v> arabic"},
		{"tabs\tand nbsp em  spaces\u001f", "tabs and nbsp em spaces"},
		{"  leading and trailing  ", "leading and trailing"},
		{"Node.js 20 actions/cache@v4, https://github.blog/changelog/2025-09-19-deprecation-of-node-20-on-github-actions-runners/", "Node.js 20 actions/cache<v>, https:/<path><date>-deprecation-of-node-20-on-github-actions-runners/"},
		{"v1.2.3-rc.1 and 1.2.3+build.5", "<v>-rc.1 and <v>+build.5"},
		{"ünïcode/path/é and ü/x", "ünïcode/path/é and ü/x"},
		{"a:/b c:/d", "a:/b c:/d"},
		{"__/x _/y", "__/x _/y"},
		{"/a/b:/c/d", "<path>:/c/d"},
		{"ends with version 1.2.", "ends with version <v>."},
		{"émoji 😀 v2 😀/x", "émoji 😀 <v> 😀<path>"},
		{"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},
		{"ééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééé v1", "ééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééé "},
	}
	for _, c := range cases {
		if got := Signature(c.msg); got != c.want {
			t.Errorf("Signature(%q)\n got %q\nwant %q", c.msg, got, c.want)
		}
	}
}

// TestSignatureNoLookbehind: RE2 has no lookbehind, so _PATH's (?<![\w:])
// is an explicit scan. Pin the boundary cases and that no regexp is used.
func TestSignatureNoLookbehind(t *testing.T) {
	cases := []struct{ msg, want string }{
		{"/at/start", "<path>"},                          // no preceding char at all
		{"x/after/word", "x/after/word"},                 // \w before the slash
		{"é/after/unicode-word", "é/after/unicode-word"}, // Unicode \w
		{"s:/after/colon", "s:/after/colon"},             // ':' before the slash
		{"://two", ":/<path>"},                           // second slash follows '/'
		{"-/after/dash", "-<path>"},                      // '-' is not \w
		{"/", "/"},                                       // a lone slash needs one path char
	}
	for _, c := range cases {
		if got := Signature(c.msg); got != c.want {
			t.Errorf("Signature(%q) = %q, want %q", c.msg, got, c.want)
		}
	}
	src, err := os.ReadFile("signature.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "regexp.") {
		t.Error("signature.go uses package regexp")
	}
}
