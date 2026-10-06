package scan

import (
	"fmt"
	"strings"

	"github.com/schuettc/muda/internal/signal"
)

// CacheSite is the unit an uncached-install claim is about: a job in a
// workflow file and one package manager.
type CacheSite struct {
	Path, Job, Manager string
}

// uncachedSummary and unknownCacheWhat are the producer's only wording for
// uncached-install signals and unknown cache coverage notes; the readers
// below invert them exactly, so consumers never parse the wording.
func uncachedSummary(job, cmd string) string {
	return fmt.Sprintf("job %s: %s without cache", job, cmd)
}
func unknownCacheWhat(s CacheSite) string {
	return s.Path + " job " + s.Job + " " + s.Manager + " cache coverage"
}

// managerOf is the package manager whose install command cmd is, or "".
func managerOf(cmd string) string {
	for _, det := range installDetectors {
		if det.pattern.FindString(cmd) == cmd {
			return det.manager
		}
	}
	return ""
}

// UncachedInstallSite reads the site an uncached-install signal claims. ok is
// false for any other signal or for one this producer could not have made.
func UncachedInstallSite(s signal.Signal) (CacheSite, bool) {
	if s.ID != "uncached-install" || len(s.Evidence) != 1 {
		return CacheSite{}, false
	}
	rest, ok := strings.CutPrefix(s.Summary, "job ")
	if !ok {
		return CacheSite{}, false
	}
	job, rest, ok := strings.Cut(rest, ": ")
	if !ok {
		return CacheSite{}, false
	}
	cmd, ok := strings.CutSuffix(rest, " without cache")
	if !ok {
		return CacheSite{}, false
	}
	site := CacheSite{Path: s.Evidence[0].Path, Job: job, Manager: managerOf(cmd)}
	if site.Path == "" || site.Manager == "" || uncachedSummary(job, cmd) != s.Summary {
		return CacheSite{}, false
	}
	return site, true
}

// UnknownCacheSite reads the site an unknown cache coverage note names. ok is
// false for any other unavailable entry.
func UnknownCacheSite(u Unavailable) (CacheSite, bool) {
	rest, ok := strings.CutSuffix(u.What, " cache coverage")
	if !ok {
		return CacheSite{}, false
	}
	i := strings.LastIndex(rest, " ")
	if i < 0 {
		return CacheSite{}, false
	}
	manager := rest[i+1:]
	rest = rest[:i]
	j := strings.LastIndex(rest, " job ")
	if j < 0 {
		return CacheSite{}, false
	}
	site := CacheSite{Path: rest[:j], Job: rest[j+len(" job "):], Manager: manager}
	known := false
	for _, det := range installDetectors {
		known = known || det.manager == manager
	}
	if !known || site.Path == "" || site.Job == "" || strings.Contains(site.Job, " ") || unknownCacheWhat(site) != u.What {
		return CacheSite{}, false
	}
	return site, true
}
