package cms

import (
	"fmt"
	"strings"

	cmsapi "github.com/reearth/reearth-cms-api/go"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/output"
)

func newCmdItem(f *core.Factory, o *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "item",
		Short: "List, show, create, update and delete items",
		Long: `List, show, create, update and delete the items of a model.

A model is given by ID, or by key together with a project (-p). Field values
are given as -k <key> -t <type> -v <value>, repeated once per field, and
metadata fields as -K, -T and -V. Values are sent as text; CMS converts them
to the field's type.

update and delete ask for confirmation unless --yes is passed, and are
refused in safe mode (see ` + "`reearth cms --help`" + `).`,
	}
	cmd.AddCommand(newCmdItemList(f, o), newCmdItemGet(f, o), newCmdItemCreate(f, o),
		newCmdItemUpdate(f, o), newCmdItemDelete(f, o))
	return cmd
}

func newCmdItemList(f *core.Factory, o *globalOptions) *cobra.Command {
	var (
		model         string
		page, perPage int
		asset         bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the items of a model",
		Example: `  $ reearth cms item list -m 01j...
  $ reearth cms item list -m parks -p my-project --page 2
  $ reearth cms item list -m parks --asset --json id,fields`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkPage(page, perPage); err != nil {
				return err
			}
			c, err := o.client(cmd.Context(), f)
			if err != nil {
				return err
			}
			f.IO.StartProgress("Fetching items…")
			var res *cmsapi.Items
			if project := o.projectOr(f); project != "" {
				res, err = c.GetItemsPartiallyByKey(cmd.Context(), project, model, page, perPage, asset)
			} else {
				res, err = c.GetItemsPartially(cmd.Context(), model, page, perPage, asset)
			}
			f.IO.StopProgress()
			if err != nil {
				return apiError(err, "model "+model)
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(res.Items, func() error {
				if len(res.Items) == 0 {
					f.IO.Info("No items found")
					return nil
				}
				cs := f.IO.Color()
				t := p.Table("ID", "FIELDS", "UPDATED")
				for _, it := range res.Items {
					t.Row(it.ID, fieldSummary(it.Fields), output.Cell{Text: p.Time(it.UpdatedAt), Style: cs.Dim})
				}
				if err := t.Render(); err != nil {
					return err
				}
				pageHint(f, p.IsHuman(), res.HasNext(), page, res.TotalCount, "items")
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&model, "model", "m", "", "Model ID, or key with -p (required)")
	_ = cmd.MarkFlagRequired("model")
	addPageFlags(cmd, &page, &perPage)
	cmd.Flags().BoolVar(&asset, "asset", false, "Include the data of referenced assets")
	return cmd
}

func newCmdItemGet(f *core.Factory, o *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "get <item-id>",
		Short:   "Show an item, with the data of its assets",
		Example: `  $ reearth cms item get 01j...`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := o.client(cmd.Context(), f)
			if err != nil {
				return err
			}
			f.IO.StartProgress("Fetching the item…")
			it, err := c.GetItem(cmd.Context(), args[0], true)
			f.IO.StopProgress()
			if err != nil {
				return apiError(err, "item "+args[0])
			}
			return printItem(f, it)
		},
	}
}

// fieldFlags are the -k/-t/-v and -K/-T/-V flags of create and update.
type fieldFlags struct {
	ids, keys, types, values                 []string
	metaIDs, metaKeys, metaTypes, metaValues []string
}

func (ff *fieldFlags) add(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringArrayVar(&ff.ids, "id", nil, "Field ID, in the order of -k (optional)")
	fl.StringArrayVarP(&ff.keys, "key", "k", nil, "Field key, once per field")
	fl.StringArrayVarP(&ff.types, "type", "t", nil, "Field type, once per field")
	fl.StringArrayVarP(&ff.values, "value", "v", nil, "Field value, once per field")
	fl.StringArrayVar(&ff.metaIDs, "meta-id", nil, "Metadata field ID, in the order of -K (optional)")
	fl.StringArrayVarP(&ff.metaKeys, "meta-key", "K", nil, "Metadata field key, once per field")
	fl.StringArrayVarP(&ff.metaTypes, "meta-type", "T", nil, "Metadata field type, once per field")
	fl.StringArrayVarP(&ff.metaValues, "meta-value", "V", nil, "Metadata field value, once per field")
}

func (ff *fieldFlags) parse() (fields, meta []*cmsapi.Field, err error) {
	if len(ff.keys) == 0 && len(ff.metaKeys) == 0 {
		return nil, nil, cmdutil.FlagErrorf("at least one field is required: -k, -t, -v or -K, -T, -V")
	}
	if fields, err = toFields(ff.ids, ff.keys, ff.types, ff.values, "-k, -t and -v"); err != nil {
		return nil, nil, err
	}
	if meta, err = toFields(ff.metaIDs, ff.metaKeys, ff.metaTypes, ff.metaValues, "-K, -T and -V"); err != nil {
		return nil, nil, err
	}
	return fields, meta, nil
}

func toFields(ids, keys, types, values []string, flags string) ([]*cmsapi.Field, error) {
	if len(keys) != len(types) || len(keys) != len(values) {
		return nil, cmdutil.FlagErrorf("%s must be given the same number of times", flags)
	}
	var fields []*cmsapi.Field
	for i := range keys {
		fd := &cmsapi.Field{Key: keys[i], Type: types[i], Value: values[i]}
		if i < len(ids) {
			fd.ID = ids[i]
		}
		fields = append(fields, fd)
	}
	return fields, nil
}

func newCmdItemCreate(f *core.Factory, o *globalOptions) *cobra.Command {
	var (
		model string
		ff    fieldFlags
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an item",
		Example: `  $ reearth cms item create -m 01j... -k title -t text -v "Hello"
  $ reearth cms item create -m parks -p my-project -k title -t text -v "Hello" -k count -t number -v 10
  $ reearth cms item create -m parks -k title -t text -v "Hello" -K status -T select -V published`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fields, meta, err := ff.parse()
			if err != nil {
				return err
			}
			c, err := o.client(cmd.Context(), f)
			if err != nil {
				return err
			}
			f.IO.StartProgress("Creating the item…")
			var it *cmsapi.Item
			if project := o.projectOr(f); project != "" {
				it, err = c.CreateItemByKey(cmd.Context(), project, model, fields, meta)
			} else {
				it, err = c.CreateItem(cmd.Context(), model, fields, meta)
			}
			f.IO.StopProgress()
			if err != nil {
				return apiError(err, "model "+model)
			}
			f.IO.Success("Created item %s", it.ID)
			return printItem(f, it)
		},
	}
	cmd.Flags().StringVarP(&model, "model", "m", "", "Model ID, or key with -p (required)")
	_ = cmd.MarkFlagRequired("model")
	ff.add(cmd)
	return cmd
}

func newCmdItemUpdate(f *core.Factory, o *globalOptions) *cobra.Command {
	var ff fieldFlags
	cmd := &cobra.Command{
		Use:   "update <item-id>",
		Short: "Update the given fields of an item",
		Example: `  $ reearth cms item update 01j... -k title -t text -v "Updated"
  $ reearth cms item update 01j... -K status -T select -V draft --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if err := checkSafeMode(f, "item update"); err != nil {
				return err
			}
			fields, meta, err := ff.parse()
			if err != nil {
				return err
			}
			if err := f.Confirm(fmt.Sprintf("Update item %s?", id)); err != nil {
				return err
			}
			c, err := o.client(cmd.Context(), f)
			if err != nil {
				return err
			}
			f.IO.StartProgress("Updating the item…")
			it, err := c.UpdateItem(cmd.Context(), id, fields, meta)
			f.IO.StopProgress()
			if err != nil {
				return apiError(err, "item "+id)
			}
			f.IO.Success("Updated item %s", it.ID)
			return printItem(f, it)
		},
	}
	ff.add(cmd)
	return cmd
}

func newCmdItemDelete(f *core.Factory, o *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "delete <item-id>",
		Short:   "Delete an item",
		Example: `  $ reearth cms item delete 01j... --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if err := checkSafeMode(f, "item delete"); err != nil {
				return err
			}
			if err := f.Confirm(fmt.Sprintf("Delete item %s?", id)); err != nil {
				return err
			}
			c, err := o.client(cmd.Context(), f)
			if err != nil {
				return err
			}
			f.IO.StartProgress("Deleting the item…")
			err = c.DeleteItem(cmd.Context(), id)
			f.IO.StopProgress()
			if err != nil {
				return apiError(err, "item "+id)
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(struct {
				ID      string `json:"id"`
				Deleted bool   `json:"deleted"`
			}{id, true}, func() error {
				f.IO.Success("Deleted item %s", id)
				return nil
			})
		},
	}
}

// printItem prints an item as it is for machines, and for people as YAML
// with group fields nested under the field that holds them.
func printItem(f *core.Factory, it *cmsapi.Item) error {
	p, err := f.Printer()
	if err != nil {
		return err
	}
	return p.Print(it, func() error {
		enc := yaml.NewEncoder(f.IO.Out)
		enc.SetIndent(2)
		defer func() { _ = enc.Close() }()
		return enc.Encode(itemView(p, it))
	})
}

func itemView(p *output.Printer, it *cmsapi.Item) map[string]any {
	groups := map[string][]*cmsapi.Field{}
	var top []*cmsapi.Field
	for _, fd := range it.Fields {
		if fd.Group != "" {
			groups[fd.Group] = append(groups[fd.Group], fd)
		} else {
			top = append(top, fd)
		}
	}
	return map[string]any{
		"id":        it.ID,
		"modelId":   it.ModelID,
		"createdAt": p.Time(it.CreatedAt),
		"updatedAt": p.Time(it.UpdatedAt),
		"fields":    fieldsView(top, groups),
	}
}

func fieldsView(fields []*cmsapi.Field, groups map[string][]*cmsapi.Field) []map[string]any {
	out := make([]map[string]any, 0, len(fields))
	for _, fd := range fields {
		v := fd.Value
		if fd.Type == "group" {
			var nested [][]map[string]any
			for _, gid := range stringsOf(fd.Value) {
				if g, ok := groups[gid]; ok {
					nested = append(nested, fieldsView(g, groups))
				}
			}
			v = nested
		}
		out = append(out, map[string]any{"key": fd.Key, "type": fd.Type, "value": v})
	}
	return out
}

// stringsOf reads a group field's value: one group ID or a list of them.
func stringsOf(v any) []string {
	switch v := v.(type) {
	case string:
		return []string{v}
	case []string:
		return v
	case []any:
		var out []string
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// fieldSummary shows an item's top-level values in one table cell.
func fieldSummary(fields []*cmsapi.Field) string {
	const maxLen = 60
	var parts []string
	for _, fd := range fields {
		if fd.Group == "" && fd.Type != "group" {
			parts = append(parts, fmt.Sprintf("%s=%v", fd.Key, fd.Value))
		}
	}
	s := strings.Join(parts, " ")
	if r := []rune(s); len(r) > maxLen {
		s = string(r[:maxLen-1]) + "…"
	}
	return s
}
