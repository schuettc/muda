package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestAppCommands(t *testing.T) {
	var out, errs bytes.Buffer
	if code := NewApp().Dispatch([]string{"commands", "--json"}, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	var doc []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("invalid command JSON: %v: %s", err, out.String())
	}
	got := map[string]bool{}
	for _, command := range doc {
		got[command.Name] = true
	}
	for _, name := range []string{"version", "help", "update", "man", "commands", "measure"} {
		if !got[name] {
			t.Errorf("missing command %s in %s", name, out.String())
		}
	}
	var version bytes.Buffer
	if code := NewApp().Dispatch([]string{"version"}, &version, &errs); code != 0 || version.String() != "muda dev\n" {
		t.Fatalf("version: exit %d output %q error %s", code, version.String(), errs.String())
	}
}
