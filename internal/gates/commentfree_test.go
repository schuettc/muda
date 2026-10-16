package gates

import "testing"

const commented = "# release\nname: release\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest # pinned\n    steps:\n      # the suite\n      - run: |\n          go test ./...\n          # not a comment\n"

// Comments carry no meaning to Actions: a file that differs only in
// comments hashes the same with them removed; any value, key or script
// text change (a # inside a block scalar is script text) does not.
func TestCommentFreeSHA256(t *testing.T) {
	base, err := commentFreeSHA256([]byte(commented))
	if err != nil || badDigest(base) {
		t.Fatalf("%q %v", base, err)
	}
	for name, raw := range map[string]string{
		"no comments":      "name: release\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: |\n          go test ./...\n          # not a comment\n",
		"changed comments": "# ship it\nname: release\non: push # trigger\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: |\n          go test ./...\n          # not a comment\n# trailing\n",
	} {
		got, err := commentFreeSHA256([]byte(raw))
		if err != nil || got != base {
			t.Errorf("%s: %s != %s (%v)", name, got, base, err)
		}
	}
	for name, raw := range map[string]string{
		"value":         "# release\nname: release\non: push\njobs:\n  test:\n    runs-on: ubuntu-22.04 # pinned\n    steps:\n      # the suite\n      - run: |\n          go test ./...\n          # not a comment\n",
		"run # text":    "# release\nname: release\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest # pinned\n    steps:\n      # the suite\n      - run: |\n          go test ./...\n          # still not a comment\n",
		"key order":     "# release\non: push\nname: release\njobs:\n  test:\n    runs-on: ubuntu-latest # pinned\n    steps:\n      # the suite\n      - run: |\n          go test ./...\n          # not a comment\n",
		"second doc":    commented + "---\nname: other\n",
		"quoted scalar": "# release\nname: \"release\"\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest # pinned\n    steps:\n      # the suite\n      - run: |\n          go test ./...\n          # not a comment\n",
	} {
		got, err := commentFreeSHA256([]byte(raw))
		if err != nil || got == base {
			t.Errorf("%s: hash %s equals the original (%v)", name, got, err)
		}
	}
}

func badDigest(s string) bool { return len(s) != 64 }

// Derive records the comment-free hash beside the raw one.
func TestDeriveRecordsCommentFreeHash(t *testing.T) {
	inv := derive(t, false, "# header\n"+deploy)
	want, err := commentFreeSHA256([]byte(deploy))
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Sources) != 1 || inv.Sources[0].CommentFreeSHA256 != want || inv.Sources[0].SHA256 == want {
		t.Fatalf("%+v; want comment-free %s", inv.Sources, want)
	}
}
