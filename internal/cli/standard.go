package cli

import (
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/schuettc/muda"
	"github.com/schuettc/muda/internal/content"
	tools "github.com/schuettc/tools-common"
)

// Standard prints the standard, or one W-number's catalog entry. The approved
// standard uses table rows rather than W-number headings; do not rewrite it.
func Standard() tools.Command {
	return tools.Command{
		Name: "standard", Summary: "read the delivery-waste standard", Synopsis: "[W-number]",
		Run: func(args []string, out, _ io.Writer) error {
			if err := validateStandardArgs(args); err != nil {
				return err
			}
			b, err := fs.ReadFile(muda.Content, "standard/standard.md")
			if err != nil {
				return err
			}
			text := string(b)
			if len(args) == 1 {
				for _, line := range strings.Split(text, "\n") {
					if strings.HasPrefix(line, "| "+args[0]+" |") {
						text = "| ID | Waste | Symptom | Detect | Example |\n| --- | --- | --- | --- | --- |\n" + line + "\n"
						_, err = io.WriteString(out, text)
						return err
					}
				}
				return fmt.Errorf("standard missing %s", args[0])
			}
			_, err = io.WriteString(out, text)
			return err
		},
	}
}

// validateStandardArgs is the raw (non-FlagSet) contract shared by the reader
// and the side-effect-free command recognizer.
func validateStandardArgs(args []string) error {
	if len(args) > 1 {
		return usage("expected at most one W-number")
	}
	if len(args) == 1 && !content.KnownWaste(args[0]) {
		return usage("unknown waste: " + args[0])
	}
	return nil
}
