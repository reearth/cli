package corecmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/config"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/output"
)

func NewCmdConfig(f *core.Factory) *cobra.Command {
	var keys []string
	for _, s := range config.Settings {
		keys = append(keys, fmt.Sprintf("  %-14s %s", s.Key, s.Description))
	}
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read and change CLI settings",
		Long: "Read and change CLI settings stored in " + config.Path() + ".\n\n" +
			"Each setting can be overridden with the environment variable REEARTH_<KEY>.\n\n" +
			"Settings:\n" + strings.Join(keys, "\n"),
	}
	complete := func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var ks []string
		for _, s := range config.Settings {
			ks = append(ks, s.Key+"\t"+s.Description)
		}
		return ks, cobra.ShellCompDirectiveNoFileComp
	}

	get := &cobra.Command{
		Use:               "get <key>",
		Short:             "Print a setting",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: complete,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			v, _, err := cfg.Get(args[0])
			if err != nil {
				return cmdutil.FlagErrorf("%s", err.Error())
			}
			_, err = fmt.Fprintln(f.IO.Out, v)
			return err
		},
	}
	set := &cobra.Command{
		Use:               "set <key> <value>",
		Short:             "Change a setting",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: complete,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			if err := cfg.Set(args[0], args[1]); err != nil {
				return cmdutil.FlagErrorf("%s", err.Error())
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			f.IO.Success("Set %s to %s", args[0], f.IO.ErrColor().Bold(args[1]))
			return nil
		},
	}
	unset := &cobra.Command{
		Use:               "unset <key>",
		Short:             "Reset a setting to its default",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: complete,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			if err := cfg.Unset(args[0]); err != nil {
				return cmdutil.FlagErrorf("%s", err.Error())
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			f.IO.Success("Reset %s", args[0])
			return nil
		},
	}

	var showOrigin bool
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List settings and project values with their sources",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			type entry struct {
				Key    string `json:"key"`
				Value  string `json:"value"`
				Origin string `json:"origin"`
			}
			var entries []entry
			for _, s := range config.Settings {
				v, o, _ := cfg.Get(s.Key)
				origin := string(o)
				switch o {
				case config.OriginEnv:
					origin = "env " + config.SettingEnv(s.Key)
				case config.OriginConfig:
					origin = cfg.Path()
				}
				entries = append(entries, entry{s.Key, v, origin})
			}
			if proj, _ := f.Project(); proj != nil {
				if proj.Account != "" {
					entries = append(entries, entry{"account", proj.Account, proj.Path()})
				}
				if proj.Workspace != "" {
					entries = append(entries, entry{"workspace", proj.Workspace, proj.Path()})
				}
				products := make([]string, 0, len(proj.Products))
				for name := range proj.Products {
					products = append(products, name)
				}
				sort.Strings(products)
				for _, name := range products {
					ks := make([]string, 0, len(proj.Products[name]))
					for k := range proj.Products[name] {
						ks = append(ks, k)
					}
					sort.Strings(ks)
					for _, k := range ks {
						v, _ := proj.Value(name, k)
						entries = append(entries, entry{name + "." + k, v, proj.Path()})
					}
				}
			}
			return p.Print(entries, func() error {
				cs := f.IO.Color()
				t := p.Table()
				for _, e := range entries {
					if showOrigin {
						t.Row(e.Key, e.Value, output.Cell{Text: e.Origin, Style: cs.Dim})
					} else {
						t.Row(e.Key, e.Value)
					}
				}
				return t.Render()
			})
		},
	}
	list.Flags().BoolVar(&showOrigin, "show-origin", false, "Show where each value comes from")

	path := &cobra.Command{
		Use:   "path",
		Short: "Print the paths of the config, cache and data directories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := f.Printer()
			if err != nil {
				return err
			}
			paths := map[string]string{
				"config": config.Path(),
				"cache":  config.CacheDir(),
				"data":   config.DataDir(),
			}
			if proj, _ := f.Project(); proj != nil {
				paths["project"] = proj.Path()
			}
			return p.Print(paths, func() error {
				t := p.Table()
				for _, k := range []string{"config", "cache", "data", "project"} {
					if v, ok := paths[k]; ok {
						t.Row(output.Cell{Text: k, Style: f.IO.Color().Dim}, v)
					}
				}
				return t.Render()
			})
		},
	}

	cmd.AddCommand(get, set, unset, list, path)
	return cmd
}
