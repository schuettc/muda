package content

import (
	"reflect"
	"testing"
)

func TestShellCommands(t *testing.T) {
	got, err := shellCommands("# example\nmuda logs 123 \\\n --job 456 --step \"a quoted step\"\nmuda scan --ref 'feature/example' # comment\n")
	want := [][]string{{"muda", "logs", "123", "--job", "456", "--step", "a quoted step"}, {"muda", "scan", "--ref", "feature/example"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%v: %v", got, err)
	}
	for _, text := range []string{"muda scan | cat", "muda scan; muda gates", "muda scan $(whoami)", "muda scan --ref \"$REF\"", "muda scan 'unterminated", "muda scan \\"} {
		if _, err := shellCommands(text); err == nil {
			t.Errorf("accepted %q", text)
		}
	}
}
