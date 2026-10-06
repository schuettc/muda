package version

import tools "github.com/schuettc/tools-common"

// The release workflow stamps these at build time. Local builds are dev.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func Version() tools.Version {
	return tools.Version{Number: version, Commit: commit, Date: date}
}
