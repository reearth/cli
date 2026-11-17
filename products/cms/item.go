package cms

import (
	"context"
	"encoding/json"
	"fmt"

	cmsapi "github.com/reearth/reearth-cms-api/go"
	"github.com/reearth/reearth-cms-api/go/cmsoapi"
	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/output"
)

func newCmdItem(f *core.Factory, o *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "item",
		Short: "List, show, create, update, delete and publish items",
		Long: `List, show, create, update, delete and publish the items of a model.

A model is given by its ID or key with -m; commands on one item need it too,
as the API reaches an item through its model. Field values
are given as -k <key> -t <type> -v <value>, repeated once per field, and
metadata fields as -K, -T and -V. Values are sent as strings. For the
field types and the values they take, see ` + "`reearth docs search \"CMS フィールド\"`" + `.

update, delete and publish ask for confirmation unless --yes is passed, and
are refused in safe mode (see ` + "`reearth cms --help`" + `).`,
	}
	cmd.AddCommand(newCmdItemList(f, o), newCmdItemGet(f, o), newCmdItemCreate(f, o),
		newCmdItemUpdate(f, o), newCmdItemDelete(f, o), newCmdItemPublish(f, o))
	return cmd
}

func newCmdItemList(f *core.Factory, o *globalOptions) *cobra.Command {
	var (
		model, ref, asset string
		lf                listFlags
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the items of a model",
		Example: `  $ reearth cms item list -m 01j...
  $ reearth cms item list -m parks -p my-project --page 2
  $ reearth cms item list -m parks --keyword tokyo --sort updatedAt --dir desc
  $ reearth cms item list -m parks --ref public
  $ reearth cms item list -m parks --asset --json=id,fields`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := lf.check(); err != nil {
				return err
			}
			if err := checkEnum("--ref", ref, "latest", "public"); err != nil {
				return err
			}
			if err := checkEnum("--asset", asset, "true", "false", "all"); err != nil {
				return err
			}
			res, err := listItems(cmd.Context(), f, o, model, ref, asset, lf)
			if err != nil {
				return err
			}
			page := lf.page

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
	addModelFlag(cmd, &model)
	lf.add(cmd)
	cmd.Flags().StringVar(&ref, "ref", "", "Which version of the items: latest or public")
	cmd.Flags().StringVar(&asset, "asset", "", "Include the data of referenced assets: true, or all for their files too")
	cmd.Flags().Lookup("asset").NoOptDefVal = "true"
	return cmd
}

// listItems lists items through the generated client, which takes every
// search and order parameter.
func listItems(ctx context.Context, f *core.Factory, o *globalOptions, model, ref, asset string, lf listFlags) (*cmsapi.Items, error) {
	c, err := o.api(ctx, f, true)
	if err != nil {
		return nil, err
	}
	f.IO.StartProgress("Fetching items…")
	res, err := c.ItemFilterWithResponse(ctx, c.ws, c.project, model, &cmsoapi.ItemFilterParams{
		Page:    &lf.page,
		PerPage: &lf.perPage,
		Keyword: optional(lf.keyword),
		Sort:    optional(cmsoapi.ItemFilterParamsSort(lf.sort)),
		Dir:     optional(cmsoapi.ItemFilterParamsDir(lf.dir)),
		Ref:     optional(cmsoapi.ItemFilterParamsRef(ref)),
		Asset:   optional(cmsoapi.AssetParam(asset)),
	})
	f.IO.StopProgress()
	what := "model " + model
	if err != nil {
		return nil, apiError(err, what)
	}
	if err := checkStatus(res.StatusCode(), res.Body, what); err != nil {
		return nil, err
	}
	// The SDK's Items is the same JSON; the views are written for it.
	var items cmsapi.Items
	if err := json.Unmarshal(res.Body, &items); err != nil {
		return nil, unexpectedResponse(what)
	}
	return &items, nil
}

func newCmdItemGet(f *core.Factory, o *globalOptions) *cobra.Command {
	var model string
	cmd := &cobra.Command{
		Use:   "get <item-id>",
		Short: "Show an item, with the data of its assets",
		Example: `  $ reearth cms item get 01j... -m parks
  $ reearth cms item get 01j... -m parks -p my-project`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
	addModelFlag(cmd, &model)
	return cmd
}

// addModelFlag adds the required -m: the model, or the model of the item.
func addModelFlag(cmd *cobra.Command, model *string) {
	cmd.Flags().StringVarP(model, "model", "m", "", "Model ID or key (required)")
	_ = cmd.MarkFlagRequired("model")
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
		Example: `  $ reearth cms item create -m parks -k title -t text -v "Hello"
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
			it, err := c.CreateItem(cmd.Context(), model, fields, meta)
			f.IO.StopProgress()
			if err != nil {
				return apiError(err, "model "+model)
			}
			f.IO.Success("Created item %s", it.ID)
			return printItem(f, it)
		},
	}
	addModelFlag(cmd, &model)
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
		Example: `  $ reearth cms item update 01j... -m parks -k title -t text -v "Updated"
  $ reearth cms item update 01j... -m parks -K status -T select -V draft --yes`,
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
			it, err := c.UpdateItemByModel(cmd.Context(), model, id, fields, meta)
			f.IO.StopProgress()
			if err != nil {
				return apiError(err, "item "+id)
			}
			f.IO.Success("Updated item %s", it.ID)
			return printItem(f, it)
		},
	}
	addModelFlag(cmd, &model)
	ff.add(cmd)
	return cmd
}

func newCmdItemDelete(f *core.Factory, o *globalOptions) *cobra.Command {
	var model string
	cmd := &cobra.Command{
		Use:     "delete <item-id>",
		Short:   "Delete an item",
		Example: `  $ reearth cms item delete 01j... -m parks --yes`,
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
	addModelFlag(cmd, &model)
	return cmd
}

func newCmdItemPublish(f *core.Factory, o *globalOptions) *cobra.Command {
	var model string
	cmd := &cobra.Command{
		Use:   "publish <item-id>",
		Short: "Publish an item",
		Long:  "Publish the latest version of an item, so the public API serves it.",
		Example: `  $ reearth cms item publish 01j... -m parks
  $ reearth cms item publish 01j... -m parks --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if err := checkSafeMode(f, "item publish"); err != nil {
				return err
			}
			c, err := o.api(cmd.Context(), f, true)
			if err != nil {
				return err
			}
			if err := f.Confirm(fmt.Sprintf("Publish item %s?", id)); err != nil {
				return err
			}
			f.IO.StartProgress("Publishing the item…")
			res, err := c.ItemPublishWithResponse(cmd.Context(), c.ws, c.project, model, id, &cmsoapi.ItemPublishParams{})
			f.IO.StopProgress()
			what := "item " + id
			if err != nil {
				return apiError(err, what)
			}
			if err := checkStatus(res.StatusCode(), res.Body, what); err != nil {
				return err
			}
			var it cmsapi.Item
			if err := json.Unmarshal(res.Body, &it); err != nil {
				return unexpectedResponse(what)
			}
			f.IO.Success("Published item %s", id)
			return printItem(f, &it)
		},
	}
	addModelFlag(cmd, &model)
	return cmd
}
