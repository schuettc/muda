package content_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/schuettc/muda"
	"github.com/schuettc/muda/internal/content"
)

func TestRecipesParse(t *testing.T) {
	rs, err := content.Recipes(muda.Content)
	if err != nil || len(rs) != 11 {
		t.Fatalf("%d recipes: %v", len(rs), err)
	}
	for i, r := range rs {
		if r.ID != fmt.Sprintf("R%d", i+1) || r.Title == "" || len(r.Sections) != 7 || len(r.StackSections) != len(r.Stacks) || (i > 0 && rs[i-1].ID == r.ID) {
			t.Fatalf("bad recipe %+v", r)
		}
	}
}
func checkFixture(t *testing.T, name, want string) {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".md")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := fs.ReadFile(muda.Content, "recipes/stacks.txt")
	if err != nil {
		t.Fatal(err)
	}
	f := libraryFixture(t)
	f["recipes/R1.md"] = &fstest.MapFile{Data: b}
	f["recipes/stacks.txt"] = &fstest.MapFile{Data: catalog}
	// This callback isolates checker diagnostics; assembled registration is
	// tested separately by cmd/muda.TestShippedContentIsClean.
	ps := content.Check(f, func(a []string) bool { return len(a) > 0 && a[0] != "nonexistent" })
	for _, p := range ps {
		if strings.Contains(p.Message, want) {
			return
		}
	}
	t.Fatalf("missing %q in %+v", want, ps)
}
func TestCheckFlagsMissingSection(t *testing.T) {
	checkFixture(t, "missing-section", "missing section: Evidence")
}
func TestCheckFlagsUnknownSignal(t *testing.T) {
	checkFixture(t, "unknown-signal", "unknown signal: made-up")
}
func TestCheckFlagsUnknownStack(t *testing.T) {
	checkFixture(t, "unknown-stack", "unknown stack: made-up")
}
func TestCheckFlagsUnknownWaste(t *testing.T) { checkFixture(t, "unknown-waste", "unknown waste: W99") }
func TestCheckFlagsBadCommand(t *testing.T) {
	checkFixture(t, "bad-command", "unknown command: nonexistent")
}

// fakeFacts swaps the shipped project-fact set for one fake word, so tests
// exercise the mechanism without naming a real project fact.
func fakeFacts(t *testing.T) {
	t.Helper()
	sum := sha256.Sum256([]byte("widgetco"))
	shipped := content.ProjectFacts
	content.ProjectFacts = content.NewFactGuard(hex.EncodeToString(sum[:]))
	t.Cleanup(func() { content.ProjectFacts = shipped })
}
func TestCheckFlagsProjectFacts(t *testing.T) {
	fakeFacts(t)
	checkFixture(t, "project-facts", "project facts are forbidden")
}

// The shipped set names no word in source: every entry is a SHA-256 digest.
func TestShippedProjectFactsAreHashes(t *testing.T) {
	hashes := content.ShippedFactHashes()
	if len(hashes) == 0 {
		t.Fatal("shipped project-fact set is empty")
	}
	hexHash := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, h := range hashes {
		if !hexHash.MatchString(h) {
			t.Errorf("not a 64-hex SHA-256 digest: %q", h)
		}
	}
}
func TestNoProjectFacts(t *testing.T) {
	for _, dir := range []string{"standard", "recipes", "skills"} {
		err := fs.WalkDir(muda.Content, dir, func(p string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() {
				return nil
			}
			b, e := fs.ReadFile(muda.Content, p)
			if e != nil {
				return e
			}
			// The same word-bounded pattern Check uses (single source).
			if m := content.ProjectFacts.Find(string(b)); m != "" {
				t.Errorf("%s: %s", p, m)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// Final review M7: project names are caught as words, not as substrings of
// ordinary words. Fake words stand in for the real set: "nf" plays a short
// word that ordinary words contain, "widgetco" a name with a plural.
func TestProjectFactsWordBoundaries(t *testing.T) {
	var hashes []string
	for _, w := range []string{"nf", "widgetco"} {
		sum := sha256.Sum256([]byte(w))
		hashes = append(hashes, hex.EncodeToString(sum[:]))
	}
	g := content.NewFactGuard(hashes...)
	for _, text := range []string{"conflict", "Conflicting edits", "influence", "inflate", "inflated", "unconflicted", "widgetcorp", "awidgetco", "nfx", ""} {
		if m := g.Find(text); m != "" {
			t.Errorf("ordinary word flagged: %q (%q)", text, m)
		}
	}
	for text, want := range map[string]string{
		"NF": "NF", "the nf season": "nf", "NF stats": "NF", "nf": "nf",
		"widgetco": "widgetco", "Widgetcos": "Widgetcos", "WIDGETCO": "WIDGETCO",
		"github.com/widgetco/muda": "widgetco", "see (Widgetco).": "Widgetco",
	} {
		if m := g.Find(text); m != want {
			t.Errorf("Find(%q) = %q, want %q", text, m, want)
		}
	}
}

func TestCheckSectionOrder(t *testing.T) {
	b, err := fs.ReadFile(muda.Content, "recipes/R1.md")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "## Symptom", "## TEMP")
	text = strings.ReplaceAll(text, "## Detect", "## Symptom")
	text = strings.ReplaceAll(text, "## TEMP", "## Detect")
	catalog, err := fs.ReadFile(muda.Content, "recipes/stacks.txt")
	if err != nil {
		t.Fatal(err)
	}
	f := libraryFixture(t)
	f["recipes/R1.md"] = &fstest.MapFile{Data: []byte(text)}
	f["recipes/stacks.txt"] = &fstest.MapFile{Data: catalog}
	if len(content.Check(f, func([]string) bool { return true })) == 0 {
		t.Fatal("out-of-order sections accepted")
	}
}

// Skills may hand evidence off with one `muda … > file.json` redirect (its
// target is validated by skills.Lint); recipes and the standard may not.
func TestCheckRedirectOnlyInSkills(t *testing.T) {
	f := fstest.MapFS{}
	err := fs.WalkDir(muda.Content, ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return e
		}
		b, e := fs.ReadFile(muda.Content, p)
		f[p] = &fstest.MapFile{Data: b}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	block := []byte("```sh\nmuda measure > measure.json\n```\n")
	f["skills/muda-measure/SKILL.md"] = &fstest.MapFile{Data: block}
	f["standard/redirect.md"] = &fstest.MapFile{Data: block}
	// Only a direct skills/<name>/SKILL.md is linted by skills.Lint; any other
	// Markdown under skills/ stays strict.
	f["skills/muda-measure/notes.md"] = &fstest.MapFile{Data: []byte("```sh\nmuda record show > .muda/baseline.json\n```\n")}
	f["skills/muda-measure/ref/SKILL.md"] = &fstest.MapFile{Data: block}
	f["skills/SKILL.md"] = &fstest.MapFile{Data: block}
	flagged := map[string]bool{}
	for _, p := range content.Check(f, func(a []string) bool { return len(a) >= 1 && (a[0] == "measure" || a[0] == "record") }) {
		flagged[p.Path] = true
	}
	for p, want := range map[string]bool{"skills/muda-measure/SKILL.md": false, "standard/redirect.md": true,
		"skills/muda-measure/notes.md": true, "skills/muda-measure/ref/SKILL.md": true, "skills/SKILL.md": true} {
		if flagged[p] != want {
			t.Errorf("%s flagged=%v, want %v", p, flagged[p], want)
		}
	}
}

// Final review m-9: R1 proves the drift fix with the stored-vs-fresh scan
// diff, not a bare muda scan.
func TestR1VerifyUsesScanDiff(t *testing.T) {
	rs, err := content.Recipes(muda.Content)
	if err != nil {
		t.Fatal(err)
	}
	verify := rs[0].Sections["Verify"]
	if rs[0].ID != "R1" || !strings.Contains(verify, "muda compare --before 2026-09-01..2026-09-08 --after 2026-09-08..2026-09-15 --gates --scan") {
		t.Fatalf("R1 Verify lacks compare --gates --scan:\n%s", verify)
	}
	for _, line := range strings.Split(verify, "\n") {
		if strings.TrimSpace(line) == "muda scan" {
			t.Fatalf("R1 Verify still proves the fix with a bare muda scan:\n%s", verify)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(verify), " "), "lists `toolchain-drift` as removed") {
		t.Fatalf("R1 Verify does not read the removal from the scan diff:\n%s", verify)
	}
}

// Validation stage 1: W1 Detect sections name concrete, comparable evidence.
func TestW1DetectIsConcrete(t *testing.T) {
	rs, err := content.Recipes(muda.Content)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"R2": {"muda logs 12345 --job 67890 --grep", "all or most stacks updated", "few did", "version string",
			"synthesized metadata", "no application change", "W1 is measured",
			// Fix round 1 (I-1, I-2, M-2).
			`--grep "(?i)update|no changes|deploy|version"`, "An empty grep is not evidence of no work; widen the pattern",
			"contains only the framework metadata resource", "Correlation alone is suspected",
			"If every deploy updated every stack", "A version change without that update pattern is only suspected"},
		"R3": {"muda logs 12345 --job 67890 --grep", "each image asset", "git diff <a> <b> --", "Dockerfile",
			"still rebuilt", "W1 is measured", "environment variables", "timestamps", "run IDs",
			"rotating value",
			// Fix round 1 (I-2, M-1, M-3).
			`--grep "(?i)build|push|skip|exist|found|cached|publish"`, "An empty grep is not evidence of no work; widen the pattern",
			"<IaC file that defines the asset>", "without step 2 it is only suspected",
			"R3 is not confirmed", "report it as unmatched",
			// Fix round 2 (N-1).
			"In the IaC file, only edits to that asset's construction or inputs count (its context, Dockerfile, build arguments or definition); edits elsewhere in it do not"},
	}
	seen := map[string]bool{}
	for _, r := range rs {
		if _, ok := want[r.ID]; !ok {
			continue
		}
		seen[r.ID] = true
		detect := strings.Join(strings.Fields(r.Sections["Detect"]), " ")
		for _, s := range want[r.ID] {
			if !strings.Contains(detect, s) {
				t.Errorf("%s Detect missing %q", r.ID, s)
			}
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("recipe %s not found", id)
		}
	}
}

// Plan T3: R2 and R6 name the read-only cloud check a human runs, in prose.
// muda reads no cloud provider; without the pasted output the finding stays
// suspected.
func TestCloudCheckProse(t *testing.T) {
	rs, err := content.Recipes(muda.Content)
	if err != nil {
		t.Fatal(err)
	}
	common := []string{"ask the human to run", "and paste the output", "Without that output, the finding stays suspected.",
		"muda itself reads no cloud provider and holds no cloud credentials."}
	want := map[string][]string{
		"R2": append([]string{"the deployed template or change set for each updated stack", "get-template or describe-change-set"}, common...),
		"R6": append([]string{"the image digests in each stage's registry", "describe-images"}, common...),
	}
	seen := map[string]bool{}
	for _, r := range rs {
		phrases, ok := want[r.ID]
		if !ok {
			continue
		}
		seen[r.ID] = true
		raw := r.Sections["Detect"]
		detect := strings.Join(strings.Fields(raw), " ")
		for _, s := range phrases {
			if !strings.Contains(detect, s) {
				t.Errorf("%s Detect missing %q", r.ID, s)
			}
		}
		// The cloud check is prose for the human, never a fenced command.
		if fence := strings.SplitN(raw, "```", 3); len(fence) == 3 && strings.Contains(fence[1], "describe") {
			t.Errorf("%s puts the cloud check in a code fence", r.ID)
		}
		if strings.Contains(raw, "\u2014") {
			t.Errorf("%s Detect contains an em dash", r.ID)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("recipe %s not found", id)
		}
	}
}

// Stage 2: R11 names the W4 cache race and the evidence that settles it.
func TestR11CacheNeverWarms(t *testing.T) {
	rs, err := content.Recipes(muda.Content)
	if err != nil {
		t.Fatal(err)
	}
	var r *content.Recipe
	for i := range rs {
		if rs[i].ID == "R11" {
			r = &rs[i]
		}
	}
	if r == nil {
		t.Fatal("recipe R11 not found")
	}
	if strings.Join(r.Waste, ",") != "W4" || !slices.Contains(r.Signals, "slow-step") || !slices.Contains(r.Stacks, "github-actions") {
		t.Fatalf("R11 metadata: %+v %+v %+v", r.Waste, r.Signals, r.Stacks)
	}
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	want := map[string][]string{
		"Detect": {`muda logs 12345 --job 67890 --grep "(?i)cache|restore|reserve|saving"`, "muda measure",
			"Unable to reserve", "restored cache size", "cache list", "R11 is measured", "Otherwise it is suspected",
			"against the size of the entry the full job saves"},
		"Fix": {"restore-only", "its own key", "Delete the poisoned entry", "Never drop or rename a required check or job",
			"on by default", "cannot be overwritten"},
		"Traps": {"A hit is not a warm cache", "next key change", "storage limit", "default branch", "not accessed",
			// Live run: a warm cache changed what the gate ran, not only its time.
			"A cache fix can change what a gate executes, not just how fast; compare executed work"},
		"Verify": {"muda compare --before 2026-09-01..2026-09-08 --after 2026-09-08..2026-09-15 --gates", "muda record check", "full-size entry", "gate inventory",
			"the same work as before"},
		"Evidence": {"Illustrative example only",
			// Live run: the fix's warm cache made the gate replay results.
			"most test packages print `(cached)`", "`-count=1` restores a full run"},
	}
	for section, phrases := range want {
		text := flat(r.Sections[section])
		for _, s := range phrases {
			if !strings.Contains(text, s) {
				t.Errorf("R11 %s missing %q", section, s)
			}
		}
	}
	if strings.Contains(r.Body, "\u2014") {
		t.Error("R11 contains an em dash")
	}
}

// Release prep P3: go is a known stack with sections in R1, R5, R6 and R11
// that state general Go-toolchain facts.
func TestGoStack(t *testing.T) {
	stacks, err := content.Stacks(muda.Content)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(stacks, "go") {
		t.Fatalf("go missing from the stack catalog: %v", stacks)
	}
	rs, err := content.Recipes(muda.Content)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"R1": {"go-version-file: go.mod", "`toolchain` directive", "GOTOOLCHAIN", "`stable`", "drifts",
			// Fix round 1, I1: the behaviour depends on the setup-go major.
			"pinned setup-go major", "From v6, setup-go reads the `toolchain` directive and exports `GOTOOLCHAIN=local`",
			"Setting `GOTOOLCHAIN=local` before a v6 or later setup step drops the toolchain pin",
			"installs the `go`-line version", "Up to v5, setup-go reads only the `go` line", "at run time"},
		"R5": {"(cached)", "-count=1", "-race", "t.Parallel()", "`-p`",
			// Fix round 1, I2, M2, M3.
			"A new runner is not a fresh run when GOCACHE is restored", "Only `-count=1` or an empty GOCACHE guarantees a fresh run",
			"invalidates", "often several times", "`-p` defaults to GOMAXPROCS", "only to tests that call `t.Parallel()`",
			// Live run: the R11 cache fix enables this cache; cross-reference it.
			"see R11"},
		"R6": {"CGO_ENABLED=0", "GOOS", "GOARCH", "-trimpath", "-ldflags", "compare artifact hashes",
			// Fix round 1, I4, M1.
			"`CGO_ENABLED=0`, `-trimpath`, an explicit GOOS and GOARCH and the same Go version produce the same artifact",
			"`-buildvcs`", "dirty", "smoke or integration tests", "`needs:` edge", "optional"},
		"R11": {"on by default", "go.sum", "module cache", "build cache", "cache: false",
			"compare what was built, not only the size",
			// Fix round 1, I3, M5.
			"`go.sum` in older majors, `go.mod` from v6.3.0", "`cache-dependency-path` overrides it",
			"only jobs on the same OS, arch and Go version share", "GOOS/GOARCH, build tags and flags such as `-race`", "`go build -x`",
			// Live run: a warm build cache turns on the test-result cache.
			"also enables Go's test-result cache", "`GOFLAGS: -count=1`",
			"the gate still runs every test (0 `(cached)`)"},
	}
	forbidden := map[string][]string{
		"R1": {"read `go version` in the job log rather than the setup line, and set"},
		"R6": {"let the other legs test against those artifacts"},
	}
	var got []string
	for _, r := range rs {
		if !slices.Contains(r.Stacks, "go") {
			continue
		}
		got = append(got, r.ID)
		section := r.StackSections["go"]
		flat := strings.Join(strings.Fields(section), " ")
		for _, s := range want[r.ID] {
			if !strings.Contains(flat, s) {
				t.Errorf("%s go section missing %q", r.ID, s)
			}
		}
		for _, s := range forbidden[r.ID] {
			if strings.Contains(flat, s) {
				t.Errorf("%s go section still says %q", r.ID, s)
			}
		}
		for i, line := range strings.Split(section, "\n") {
			if len(line) > 80 {
				t.Errorf("%s go section line %d is %d characters; wrap at 80", r.ID, i+1, len(line))
			}
		}
		if !strings.Contains(flat, "Before:") || !strings.Contains(flat, "After:") {
			t.Errorf("%s go section lacks the Before/After voice", r.ID)
		}
		if strings.Contains(section, "\u2014") {
			t.Errorf("%s go section contains an em dash", r.ID)
		}
		if m := content.ProjectFacts.Find(section); m != "" {
			t.Errorf("%s go section names a project fact: %s", r.ID, m)
		}
	}
	if strings.Join(got, ",") != "R1,R5,R6,R11" {
		t.Fatalf("recipes with the go stack: %v", got)
	}
}
