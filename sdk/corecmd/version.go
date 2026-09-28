package corecmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/build"
	"github.com/reearth/cli/sdk/core"
)

type versionJSON struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func NewCmdVersion(f *core.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := f.Printer()
			if err != nil {
				return err
			}
			v := versionJSON{build.Version, build.Commit, build.Date, runtime.Version(), runtime.GOOS, runtime.GOARCH}
			return p.Print(v, func() error {
				_, err := fmt.Fprintln(f.IO.Out, VersionLine(f.AppName, f.IO.Color().Dim))
				return err
			})
		},
	}
}

// VersionLine renders "reearth 1.2.3 (abc1234, 2026-09-28)".
func VersionLine(app string, dim func(string) string) string {
	s := app + " " + build.Version
	detail := build.Commit
	if build.Date != "" {
		if detail != "" {
			detail += ", "
		}
		detail += build.Date
	}
	if detail != "" {
		s += " " + dim("("+detail+")")
	}
	return s
}
