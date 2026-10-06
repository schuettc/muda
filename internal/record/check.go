package record

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"time"
)

// Snapshot returns all data without exposing arbitrary filename selection.
// Receipts are returned as complete markdown including their versioned front block.
func (s *Store) Snapshot() (map[string]any, error) {
	// An unfinished baseline set may be a mix; no reader gets it.
	if err := s.refusePending(); err != nil {
		return nil, err
	}
	var settings Settings
	var fs findingsFile
	var es exemptionsFile
	if err := s.load("muda.toml", &settings); err != nil {
		return nil, err
	}
	if err := s.load("findings.toml", &fs); err != nil {
		return nil, err
	}
	if err := s.load("exemptions.toml", &es); err != nil {
		return nil, err
	}
	doc, err := s.loadGates()
	if err != nil {
		return nil, err
	}
	g := []byte(doc.InventoryJSON)
	b, err := s.read("baseline.json")
	if err != nil {
		return nil, err
	}
	if err = validateJSON(b, "baseline.json"); err != nil {
		return nil, err
	}
	receipts, err := s.receipts()
	if err != nil {
		return nil, err
	}
	for name, body := range receipts {
		if err := noSecrets([]byte(body)); err != nil {
			return nil, err
		}
		if _, err := parseReceipt([]byte(body), "receipts/"+name); err != nil {
			return nil, err
		}
	}
	snap := map[string]any{"schema": Schema, "settings": settings, "gates": json.RawMessage(g), "baseline": json.RawMessage(b), "findings": fs.Findings, "exemptions": es.Exemptions, "receipts": receipts}
	if h, _ := HistoricalOf(doc.HistoricalRef, doc.SettingsRead); h != nil {
		snap["historical"] = *h
	}
	sc, err := s.readScan()
	if err != nil {
		return nil, err
	}
	if sc != nil {
		snap["scan"] = json.RawMessage(sc)
	}
	return snap, nil
}
func (s *Store) receipts() (map[string]string, error) {
	r, err := s.root()
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	if err = safe(r, "receipts"); err != nil {
		return nil, err
	}
	d, err := r.Open("receipts")
	if err != nil {
		return nil, err
	}
	defer func() { _ = d.Close() }()
	entries, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	result := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".md") || !ValidFindingID(strings.TrimSuffix(name, ".md")) {
			return nil, problem("invalid-receipt-file", "receipts", name)
		}
		b, err := s.read("receipts/" + name)
		if err != nil {
			return nil, err
		}
		result[name] = string(b)
	}
	return result, nil
}
func (s *Store) Check() []Problem {
	result := []Problem{}
	add := func(file string, err error) {
		if err == nil {
			return
		}
		var p Problem
		if errors.As(err, &p) {
			result = append(result, p)
		} else {
			result = append(result, Problem{"invalid-record", file, err.Error()})
		}
	}
	var settings Settings
	var validSettings *Settings
	if err := s.load("muda.toml", &settings); err != nil {
		add("muda.toml", err)
	} else if err = validateSettings(settings); err != nil {
		add("muda.toml", err)
	} else {
		validSettings = &settings
	}
	doc, err := s.loadGates()
	add("gates.toml", err)
	var g []byte
	hist := ""
	if err == nil {
		g, hist = []byte(doc.InventoryJSON), doc.HistoricalRef
	}
	b, err := s.read("baseline.json")
	if err == nil {
		err = validateJSON(b, "baseline.json")
	}
	add("baseline.json", err)
	validBaseline := b
	if err != nil {
		validBaseline = nil
	}
	sc, err := s.readScan()
	add("scan.json", err)
	if err != nil {
		sc = nil
	}
	checkBaselineSet(add, validSettings, validBaseline, g, sc, hist)
	add(pendingName, s.pendingProblem())
	var fs findingsFile
	add("findings.toml", s.load("findings.toml", &fs))
	var es exemptionsFile
	add("exemptions.toml", s.load("exemptions.toml", &es))
	ids := map[string]bool{}
	exemptions := map[string]Exemption{}
	for _, e := range es.Exemptions {
		add("exemptions.toml", validateExemption(e))
		if _, ok := exemptions[e.ID]; ok {
			add("exemptions.toml", problem("duplicate-exemption", "exemptions.toml", e.ID))
		}
		exemptions[e.ID] = e
	}
	for _, f := range fs.Findings {
		add("findings.toml", validateFinding(f))
		if ids[f.ID] {
			add("findings.toml", problem("duplicate-finding", "findings.toml", f.ID))
		}
		ids[f.ID] = true
		if f.ExemptionID != "" {
			e, ok := exemptions[f.ExemptionID]
			if !ok {
				add("findings.toml", problem("dangling-exemption", "findings.toml", f.ID+": "+f.ExemptionID))
			} else if e.Waste != f.Waste || e.Location != f.Location {
				add("findings.toml", problem("unmatched-exemption", "findings.toml", f.ID))
			} else if review, eErr := time.Parse("2006-01-02", e.ReviewBy); f.Status == ExemptStatus && eErr == nil && review.Before(today()) {
				// Only an exemption still relied on can be expired; one that
				// was superseded or released is history.
				add("exemptions.toml", problem("expired-exemption", "exemptions.toml", e.ID))
			}
		}
	}
	var inv struct {
		Gates []struct {
			ID string `json:"id"`
		} `json:"gates"`
	}
	if len(g) > 0 {
		add("gates.toml", json.Unmarshal(g, &inv))
	}
	for _, e := range es.Exemptions {
		matched := false
		for _, f := range fs.Findings {
			if f.Waste == e.Waste && f.Location == e.Location {
				matched = true
			}
		}
		for _, gate := range inv.Gates {
			if e.Location == "gate:"+gate.ID {
				matched = true
			}
		}
		// Compare's synthetic policy-proof IDs are gates for exemption purposes.
		if id, ok := strings.CutPrefix(e.Location, "gate:"); ok && IsPolicyProofID(id) {
			matched = true
		}
		if !matched {
			add("exemptions.toml", problem("unmatched-exemption", "exemptions.toml", e.ID))
		}
	}
	rs, e := s.receipts()
	add("receipts", e)
	names := make([]string, 0, len(rs))
	for n := range rs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		file := "receipts/" + name
		r, e := parseReceipt([]byte(rs[name]), file)
		if e != nil {
			add(file, e)
			continue
		}
		add(file, validateReceipt(r))
		if r.FindingID != strings.TrimSuffix(name, ".md") {
			add(file, problem("receipt-id-mismatch", file, r.FindingID))
		}
		if !ids[r.FindingID] {
			add(file, problem("dangling-receipt", file, r.FindingID))
		}
		add(file, noSecrets([]byte(rs[name])))
	}
	for _, f := range fs.Findings {
		if f.Status == Fixed {
			body, ok := rs[f.ID+".md"]
			var readErr error
			if !ok {
				readErr = os.ErrNotExist
			}
			add("findings.toml", fixProblem(f, []byte(body), readErr))
		}
	}
	for _, name := range []string{"muda.toml", "findings.toml", "exemptions.toml"} {
		if b, e := s.read(name); e == nil {
			add(name, noSecrets(b))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Detail < b.Detail
	})
	return result
}
