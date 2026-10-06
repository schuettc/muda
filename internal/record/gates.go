package record

import (
	"encoding/json"
	"fmt"
	"github.com/schuettc/muda/internal/gates"
)

func (s *Store) SetGates(inv *gates.Inventory) error {
	if inv == nil {
		return problem("missing-inventory", "gates.toml", "nil inventory")
	}
	b, err := json.Marshal(inv)
	if err != nil {
		return err
	}
	return s.SetGatesJSON(b)
}

// SetGatesJSON is the CLI boundary: never unmarshal/remarshal through an older
// Inventory type, since additive raw security policies must survive unchanged.
func (s *Store) SetGatesJSON(b []byte) (err error) {
	s, release, err := s.mutation()
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := release(); err == nil {
			err = releaseErr
		}
	}()
	if err := s.refusePending(); err != nil {
		return err
	}
	if err := validateJSON(b, "gates.toml"); err != nil {
		return err
	}
	var old gatesFile
	if err := s.load("gates.toml", &old); err != nil {
		return err
	}
	// Replacing a historical inventory here would silently make the record
	// current while its scan stays at the historical commit.
	if old.HistoricalRef != "" {
		return problem("historical-gates", "gates.toml", fmt.Sprintf("records a historical baseline at %q; replace its inventory with record baseline --gates FILE --ref COMMIT (or without --ref, with --scan, for a current baseline)", old.HistoricalRef))
	}
	if err := validateJSON([]byte(old.InventoryJSON), "gates.toml"); err != nil {
		return err
	}
	return s.writeTOML("gates.toml", gatesFile{Schema: Schema, InventoryJSON: string(b)})
}
func (s *Store) GatesJSON() ([]byte, error) {
	doc, err := s.loadGates()
	if err != nil {
		return nil, err
	}
	return []byte(doc.InventoryJSON), nil
}

// loadGates reads gates.toml, validating its inventory and historical pair.
func (s *Store) loadGates() (gatesFile, error) {
	var doc gatesFile
	if err := s.load("gates.toml", &doc); err != nil {
		return doc, err
	}
	if err := validateJSON([]byte(doc.InventoryJSON), "gates.toml"); err != nil {
		return doc, err
	}
	_, err := HistoricalOf(doc.HistoricalRef, doc.SettingsRead)
	return doc, err
}
