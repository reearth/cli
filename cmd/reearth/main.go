// Command reearth is the unified Re:Earth CLI.
package main

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/internal/extension"
	"github.com/reearth/cli/internal/update"
	"github.com/reearth/cli/products/hello"
	"github.com/reearth/cli/sdk/app"
	"github.com/reearth/cli/sdk/build"
	"github.com/reearth/cli/sdk/core"
)

const name = "reearth"

func main() {
	var root *cobra.Command
	isBuiltin := func(n string) bool {
		if root == nil {
			return false
		}
		c, _, err := root.Find([]string{n})
		return err == nil && c != root
	}
	exts := extension.NewManager(build.UserAgent(name), isBuiltin)
	dispatch := extension.Dispatch(exts)

	var notifier *update.Notifier
	os.Exit(app.Run(app.Options{
		Name: name,
		Products: []core.Product{
			hello.Product{},
		},
		Extra: func(f *core.Factory) []*cobra.Command {
			return []*cobra.Command{update.NewCmdUpgrade(f), extension.NewCmd(f, exts)}
		},
		Dispatch: func(f *core.Factory, r *cobra.Command, args []string) (bool, int) {
			root = r
			return dispatch(f, r, args)
		},
		BeforeRun: func(f *core.Factory, args []string) {
			notifier = update.Start(f, args)
		},
		AfterRun: func(f *core.Factory, _ *cobra.Command, _ error) {
			notifier.Notify(f)
		},
		DoctorChecks: []func(context.Context) []core.Check{update.DoctorCheck(name)},
	}, os.Args[1:]))
}
