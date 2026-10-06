package main

import (
	"os"

	"github.com/schuettc/muda/internal/cli"
	"github.com/schuettc/muda/internal/version"
	tools "github.com/schuettc/tools-common"
)

// NewApp registers the family commands and the delivery-waste commands that
// are ready.
func NewApp() *tools.App { return newApp(cli.DefaultEnv()) }

func newApp(env cli.Env) *tools.App {
	app := tools.New(tools.Config{
		Name: "muda", Domain: "muda.tools", Version: version.Version(),
	})
	app.Register(cli.Measure(env))
	app.Register(cli.Scan(env))
	app.Register(cli.Gates(env))
	app.Register(cli.Logs(env))
	app.Register(cli.Notices(env))
	app.Register(cli.Record())
	app.Register(cli.Compare(env))
	app.Register(cli.Recipes())
	app.Register(cli.Recipe())
	app.Register(cli.Standard())
	app.Register(cli.Skills())
	return app
}

func main() {
	os.Exit(NewApp().Dispatch(os.Args[1:], os.Stdout, os.Stderr))
}
