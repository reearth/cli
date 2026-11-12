package cms

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	cmsapi "github.com/reearth/reearth-cms-api/go"
	"github.com/reearth/reearth-cms-api/go/cmsoapi"
	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/output"
)

func newCmdModel(f *core.Factory, o *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "model",
		Short: "List and show models, and show their fields",
	}
	cmd.AddCommand(newCmdModelList(f, o), newCmdModelGet(f, o), newCmdModelSchema(f, o))
	return cmd
}

func newCmdModelList(f *core.Factory, o *globalOptions) *cobra.Command {
	var page, perPage int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the models of a project",
		Example: `  $ reearth cms model list -p my-project
  $ reearth cms model list --page 2 --per-page 20
  $ reearth cms model list --json=id,key,name`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkPage(page, perPage); err != nil {
				return err
			}
			project, err := o.requireProject(f)
			if err != nil {
				return err
			}
			c, err := o.client(cmd.Context(), f)
			if err != nil {
				return err
			}
			f.IO.StartProgress("Fetching models…")
			res, err := c.GetModelsPartially(cmd.Context(), project, page, perPage)
			f.IO.StopProgress()
			if err != nil {
				return apiError(err, "project "+project)
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(res.Models, func() error {
				if len(res.Models) == 0 {
					f.IO.Info("No models found")
					return nil
				}
				cs := f.IO.Color()
				t := p.Table("ID", "KEY", "NAME", "PUBLIC", "UPDATED")
				for _, m := range res.Models {
					t.Row(m.ID, m.Key, m.Name, m.Public, output.Cell{Text: p.Time(m.UpdatedAt), Style: cs.Dim})
				}
				if err := t.Render(); err != nil {
					return err
				}
				pageHint(f, p.IsHuman(), res.HasNext(), page, res.TotalCount, "models")
				return nil
			})
		},
	}
	addPageFlags(cmd, &page, &perPage)
	return cmd
}

func newCmdModelGet(f *core.Factory, o *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "get <model-id-or-key>",
		Short: "Show a model by ID, or by key within a project",
		Example: `  $ reearth cms model get 01j...
  $ reearth cms model get parks -p my-project`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := o.client(cmd.Context(), f)
			if err != nil {
				return err
			}
			f.IO.StartProgress("Fetching the model…")
			m, err := c.GetModel(cmd.Context(), args[0])
			// Not an ID: try it as a key in the project.
			if project := o.projectOr(f); errors.Is(err, cmsapi.ErrNotFound) && project != "" {
				m, err = c.GetModelByKey(cmd.Context(), project, args[0])
			}
			f.IO.StopProgress()
			if err != nil {
				return apiError(err, "model "+args[0])
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(m, func() error {
				return detail(f, p,
					[2]string{"id", m.ID},
					[2]string{"key", m.Key},
					[2]string{"name", m.Name},
					[2]string{"public", fmt.Sprint(m.Public)},
					[2]string{"project", m.ProjectID},
					[2]string{"schema", m.SchemaID},
					[2]string{"metadata schema", m.MetadataSchemaID},
					[2]string{"updated", p.Time(m.UpdatedAt)},
				)
			})
		},
	}
}

func newCmdModelSchema(f *core.Factory, o *globalOptions) *cobra.Command {
	var metadata bool
	cmd := &cobra.Command{
		Use:   "schema <model-id-or-key>",
		Short: "Show the fields of a model as a JSON Schema",
		Long: `Show the fields of a model: their keys, types, and whether they are
required, as the JSON Schema the API returns. Use the keys and types with
` + "`item create`" + ` and ` + "`item update`" + ` (-k, -t). --metadata shows the metadata
fields instead (-K, -T).

It calls the workspace-scoped API, so it needs a workspace (-w) as well as a
project (-p).`,
		Example: `  $ reearth cms model schema parks -w my-workspace -p my-project
  $ reearth cms model schema parks --metadata
  $ reearth cms model schema parks --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			model := args[0]
			c, err := o.api(cmd.Context(), f, true)
			if err != nil {
				return err
			}
			f.IO.StartProgress("Fetching the schema…")
			var (
				code int
				body []byte
				doc  *cmsoapi.JSONSchema
			)
			if metadata {
				res, rerr := c.MetadataSchemaByModelAsJSONWithResponse(cmd.Context(), c.ws, c.project, model)
				if err = rerr; err == nil {
					code, body, doc = res.StatusCode(), res.Body, res.JSON200
				}
			} else {
				res, rerr := c.SchemaByModelAsJSONWithResponse(cmd.Context(), c.ws, c.project, model)
				if err = rerr; err == nil {
					code, body, doc = res.StatusCode(), res.Body, res.JSON200
				}
			}
			f.IO.StopProgress()
			what := "model " + model
			if err != nil {
				return apiError(err, what)
			}
			if err := checkStatus(code, body, what); err != nil {
				return err
			}
			var s jsonSchema
			if doc == nil || json.Unmarshal(body, &s) != nil {
				return unexpectedResponse(what)
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(*doc, func() error {
				rows := s.rows("")
				if len(rows) == 0 {
					f.IO.Info("No fields")
					return nil
				}
				cs := f.IO.Color()
				t := p.Table("KEY", "TYPE", "REQUIRED", "TITLE", "OPTIONS")
				for _, r := range rows {
					t.Row(r.key, r.typ, r.required, output.Cell{Text: r.title, Style: cs.Dim}, r.options)
				}
				return t.Render()
			})
		},
	}
	cmd.Flags().BoolVar(&metadata, "metadata", false, "Show the metadata fields")
	return cmd
}

// jsonSchema is the part of a model's JSON Schema that describes its fields.
// Each property is a field; x- keywords carry the CMS field type.
type jsonSchema struct {
	Title      string                `json:"title"`
	Type       string                `json:"type"`
	FieldType  string                `json:"x-fieldType"`
	Required   bool                  `json:"x-required"`
	Multiple   bool                  `json:"x-multiple"`
	Options    []string              `json:"x-options"`
	Properties map[string]jsonSchema `json:"properties"`
	Items      *jsonSchema           `json:"items"`
}

type schemaRow struct {
	key, typ, title, options string
	required                 bool
}

// rows lists the fields by key, with the fields of a group under the
// group's key, as group.field, or group[].field for a multiple group.
func (s jsonSchema) rows(prefix string) []schemaRow {
	var rows []schemaRow
	for _, key := range slices.Sorted(maps.Keys(s.Properties)) {
		fd := s.Properties[key]
		typ := fd.FieldType
		if typ == "" && fd.Items != nil {
			typ = fd.Items.FieldType
		}
		if typ == "" {
			typ = fd.Type
		}
		name := prefix + key
		if fd.Multiple {
			typ += "[]"
		}
		rows = append(rows, schemaRow{key: name, typ: typ, title: fd.Title,
			options: strings.Join(fd.Options, ", "), required: fd.Required})
		if strings.TrimSuffix(typ, "[]") != "group" {
			continue
		}
		group := fd
		if fd.Multiple && fd.Items != nil {
			group, name = *fd.Items, name+"[]"
		}
		rows = append(rows, group.rows(name+".")...)
	}
	return rows
}

func addPageFlags(cmd *cobra.Command, page, perPage *int) {
	cmd.Flags().IntVar(page, "page", 1, "Page number")
	cmd.Flags().IntVar(perPage, "per-page", 50, "Results per page")
}

// maxPerPage is the most results the API returns in a page.
const maxPerPage = 100

func checkPage(page, perPage int) error {
	if page < 1 || perPage < 1 {
		return cmdutil.FlagErrorf("--page and --per-page must be 1 or more")
	}
	if perPage > maxPerPage {
		return cmdutil.FlagErrorf("--per-page must be %d or less", maxPerPage)
	}
	return nil
}

// listFlags are the paging, search and order flags of a list command.
type listFlags struct {
	page, perPage      int
	keyword, sort, dir string
}

func (l *listFlags) add(cmd *cobra.Command) {
	addPageFlags(cmd, &l.page, &l.perPage)
	cmd.Flags().StringVar(&l.keyword, "keyword", "", "Only show results that match a keyword")
	cmd.Flags().StringVar(&l.sort, "sort", "", "Sort by createdAt or updatedAt")
	cmd.Flags().StringVar(&l.dir, "dir", "", "Sort direction with --sort: asc or desc")
}

func (l *listFlags) check() error {
	if err := checkPage(l.page, l.perPage); err != nil {
		return err
	}
	if err := checkEnum("--sort", l.sort, "createdAt", "updatedAt"); err != nil {
		return err
	}
	if l.dir != "" && l.sort == "" {
		return cmdutil.FlagErrorf("--dir needs --sort")
	}
	return checkEnum("--dir", l.dir, "asc", "desc")
}

// checkEnum accepts "" or one of values for flag.
func checkEnum(flag, v string, values ...string) error {
	if v == "" || slices.Contains(values, v) {
		return nil
	}
	return cmdutil.FlagErrorf("%s must be %s, not %q", flag, strings.Join(values, " or "), v)
}

// optional returns a pointer to v, or nil for the zero value, for the
// optional parameters of the generated client.
func optional[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}

// hasNext reports whether a page of the generated client's results is
// followed by another.
func hasNext(page, perPage, total *int) bool {
	if page == nil || perPage == nil || total == nil || *perPage == 0 {
		return false
	}
	return *total > *page**perPage
}

// deref returns what p points at, or the zero value for nil.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// pageHint tells a person when there are more pages.
func pageHint(f *core.Factory, human, hasNext bool, page, total int, what string) {
	if human && hasNext {
		f.IO.Hint("page %d of %d %s; pass --page %d for the next", page, total, what, page+1)
	}
}
