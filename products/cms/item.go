package cms

import (
	"fmt"

	cmsapi "github.com/reearth/reearth-cms-api/go"
	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/output"
)

func newCmdItem(f *core.Factory, o *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "item",
		Short: "List, show, create, update and delete items",
		Long: `List, show, create, update and delete the items of a model.

A model is given by ID, or by key together with a project (-p). get, update
and delete take the item's model with -m; it is required with a workspace
(-w), whose API reaches items only through their model. Field values
are given as -k <key> -t <type> -v <value>, repeated once per field, and
metadata fields as -K, -T and -V. Values are sent as strings. For the
field types and the values they take, see ` + "`reearth docs search \"CMS フィールド\"`" + `.

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
  $ reearth cms item list -m parks --asset --json=id,fields`,
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
				it := newItemTable(res.Items)
				var dropped int
				if p.IsHuman() {
					// The ID and UPDATED columns, with the margin and gaps.
					idW := 0
					for _, item := range res.Items {
						idW = max(idW, len(item.ID))
					}
					dropped = it.fit(f.IO.TerminalWidth(), 2+idW+3+len("12 months ago"))
				}
				headers := append(append([]string{"ID"}, it.keys...), "UPDATED")
				t := p.Table(headers...)
				for i, item := range res.Items {
					row := []any{item.ID}
					for _, k := range it.keys {
						row = append(row, it.values[i][k])
					}
					t.Row(append(row, output.Cell{Text: p.Time(item.UpdatedAt), Style: cs.Dim})...)
				}
				if err := t.Render(); err != nil {
					return err
				}
				if dropped > 0 {
					f.IO.Hint("%d more fields do not fit; see them with `%s cms item get <id>` or --json", dropped, f.AppName)
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
	var model string
	cmd := &cobra.Command{
		Use:   "get <item-id>",
		Short: "Show an item, with the data of its assets",
		Example: `  $ reearth cms item get 01j...
  $ reearth cms item get 01j... -m parks -w my-workspace -p my-project`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := o.checkItemModel(f, model); err != nil {
				return err
			}
			c, err := o.client(cmd.Context(), f)
			if err != nil {
				return err
			}
			f.IO.StartProgress("Fetching the item…")
			it, err := c.GetItemByModel(cmd.Context(), model, args[0], true)
			f.IO.StopProgress()
			if err != nil {
				return apiError(err, "item "+args[0])
			}
			return printItem(f, it)
		},
	}
	addItemModelFlag(cmd, &model)
	return cmd
}

// addItemModelFlag adds -m to a command that takes an item by ID.
func addItemModelFlag(cmd *cobra.Command, model *string) {
	cmd.Flags().StringVarP(model, "model", "m", "", "The item's model ID, or key with -p (required with a workspace)")
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
	var (
		model string
		ff    fieldFlags
	)
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
			if err := o.checkItemModel(f, model); err != nil {
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
			it, err := c.UpdateItemByModel(cmd.Context(), model, id, fields, meta)
			f.IO.StopProgress()
			if err != nil {
				return apiError(err, "item "+id)
			}
			f.IO.Success("Updated item %s", it.ID)
			return printItem(f, it)
		},
	}
	addItemModelFlag(cmd, &model)
	ff.add(cmd)
	return cmd
}

func newCmdItemDelete(f *core.Factory, o *globalOptions) *cobra.Command {
	var model string
	cmd := &cobra.Command{
		Use:     "delete <item-id>",
		Short:   "Delete an item",
		Example: `  $ reearth cms item delete 01j... --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if err := checkSafeMode(f, "item delete"); err != nil {
				return err
			}
			if err := o.checkItemModel(f, model); err != nil {
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
			err = c.DeleteItemByModel(cmd.Context(), model, id)
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
	addItemModelFlag(cmd, &model)
	return cmd
}
