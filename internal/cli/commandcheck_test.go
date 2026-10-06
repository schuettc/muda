package cli

import (
	"bytes"
	"io"
	"testing"

	tools "github.com/schuettc/tools-common"
)

func TestCommandCheckerRawAndUnknownContracts(t *testing.T) {
	app := tools.New(tools.Config{Name: "muda"})
	app.Register(Standard())
	app.Register(Record())
	app.Register(tools.Command{Name: "new-command", Synopsis: "REQUIRED", Run: func([]string, io.Writer, io.Writer) error { t.Fatal("handler executed"); return nil }})
	known, err := CommandChecker(app)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range [][]string{{"help", "nonexistent"}, {"record", "finding", "unknown"}, {"standard", "--"}, {"standard", "--", "W1"}, {"standard", "--W1"}, {"new-command"}, {"new-command", "arg"}} {
		if known(a) {
			t.Errorf("accepted %v", a)
		}
	}
	for _, a := range [][]string{{"help"}, {"help", "standard"}, {"help", "record"}, {"standard"}, {"standard", "W1"}, {"record", "check"}, {"record", "finding", "set", "F-001", "--file", "input.json"}, {"standard", "--help"}, {"commands", "--json"}, {"help", "record", "check"}, {"-h", "standard"}} {
		if !known(a) {
			t.Errorf("rejected %v", a)
		}
	}
	// Shared raw standard validation must match the actual handler.
	for _, a := range [][]string{{"standard", "--"}, {"standard", "--", "W1"}, {"standard", "--W1"}, {"standard", "W1"}} {
		var out, errs bytes.Buffer
		if got := app.Dispatch(a, &out, &errs) == 0; got != known(a) {
			t.Errorf("consumer mismatch %v: %v", a, got)
		}
	}
}
