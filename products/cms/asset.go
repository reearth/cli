package cms

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	cmsapi "github.com/reearth/reearth-cms-api/go"
	"github.com/reearth/reearth-cms-api/go/cmsoapi"
	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/output"
)

func newCmdAsset(f *core.Factory, o *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "asset",
		Short: "List, show, upload and download assets",
		Long: `List, show, upload and download the assets of a project.

An asset is a file stored by CMS, such as an image or a GeoJSON file, that
items refer to by its ID. Its content is downloaded from the asset's URL.`,
	}
	cmd.AddCommand(newCmdAssetList(f, o), newCmdAssetGet(f, o), newCmdAssetCreate(f, o), newCmdAssetCat(f, o), newCmdAssetCp(f, o))
	return cmd
}

func newCmdAssetList(f *core.Factory, o *globalOptions) *cobra.Command {
	var lf listFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the assets of a project",
		Example: `  $ reearth cms asset list -p my-project
  $ reearth cms asset list --keyword park --sort updatedAt --dir desc
  $ reearth cms asset list --json=id,name,url`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := lf.check(); err != nil {
				return err
			}
			c, err := o.api(cmd.Context(), f, true)
			if err != nil {
				return err
			}
			f.IO.StartProgress("Fetching assets…")
			res, err := c.AssetFilterWithResponse(cmd.Context(), c.ws, c.project, &cmsoapi.AssetFilterParams{
				Page:    &lf.page,
				PerPage: &lf.perPage,
				Keyword: optional(lf.keyword),
				Sort:    optional(cmsoapi.AssetFilterParamsSort(lf.sort)),
				Dir:     optional(cmsoapi.AssetFilterParamsDir(lf.dir)),
			})
			f.IO.StopProgress()
			what := "project " + c.project
			if err != nil {
				return apiError(err, what)
			}
			if err := checkStatus(res.StatusCode(), res.Body, what); err != nil {
				return err
			}
			if res.JSON200 == nil {
				return unexpectedResponse(what)
			}
			assets := deref(res.JSON200.Items)

			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(assets, func() error {
				if len(assets) == 0 {
					f.IO.Info("No assets found")
					return nil
				}
				cs := f.IO.Color()
				// The API leaves the name, content type and update time out of
				// the list, so the name comes from the URL.
				t := p.Table("ID", "NAME", "SIZE", "PUBLIC", "CREATED")
				for _, a := range assets {
					t.Row(a.Id, assetName(deref(a.Name), a.Url), formatSize(deref(a.TotalSize)), a.Public,
						output.Cell{Text: p.Time(a.CreatedAt), Style: cs.Dim})
				}
				if err := t.Render(); err != nil {
					return err
				}
				pageHint(f, p.IsHuman(), hasNext(res.JSON200.Page, res.JSON200.PerPage, res.JSON200.TotalCount),
					lf.page, deref(res.JSON200.TotalCount), "assets")
				return nil
			})
		},
	}
	lf.add(cmd)
	return cmd
}

func newCmdAssetGet(f *core.Factory, o *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "get <asset-id>",
		Short:   "Show an asset",
		Example: `  $ reearth cms asset get 01j...`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := getAsset(cmd.Context(), f, o, args[0])
			if err != nil {
				return err
			}
			return printAsset(f, a)
		},
	}
}

// printAsset prints an asset: as the API returns it for machines, and as
// its details for people.
func printAsset(f *core.Factory, a *cmsapi.Asset) error {
	p, err := f.Printer()
	if err != nil {
		return err
	}
	return p.Print(a, func() error {
		return detail(f, p,
			[2]string{"id", a.ID},
			[2]string{"name", assetName(a.Name, a.URL)},
			[2]string{"url", a.URL},
			[2]string{"content type", a.ContentType},
			[2]string{"size", formatSize(float32(a.TotalSize))},
			[2]string{"project", a.ProjectID},
			[2]string{"created", p.Time(a.CreatedAt)},
			[2]string{"updated", p.Time(a.UpdatedAt)},
		)
	})
}

// assetName is an asset's file name, or the last part of its URL when the
// API leaves the name out.
func assetName(name, rawURL string) string {
	if name != "" || rawURL == "" {
		return name
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Path == "" || strings.HasSuffix(u.Path, "/") {
		return ""
	}
	return path.Base(u.Path)
}

// formatSize shows a size in bytes in KB, MB or GB, or "" for 0.
func formatSize(n float32) string {
	if n <= 0 {
		return ""
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%.0f B", n)
	}
	v, i := float64(n)/unit, 0
	for v >= unit && i < 2 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f %cB", v, "KMG"[i])
}

func getAsset(ctx context.Context, f *core.Factory, o *globalOptions, id string) (*cmsapi.Asset, error) {
	c, err := o.client(ctx, f)
	if err != nil {
		return nil, err
	}
	f.IO.StartProgress("Fetching the asset…")
	a, err := c.Asset(ctx, id)
	f.IO.StopProgress()
	if err != nil {
		return nil, apiError(err, "asset "+id)
	}
	return a, nil
}

func newCmdAssetCreate(f *core.Factory, o *globalOptions) *cobra.Command {
	var (
		file, fromURL string
		direct        bool
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Upload a file (-f) or a file at a URL (-u) as an asset",
		Long: `Upload a file (-f) or a file at a URL (-u) as an asset of the project.

A file is uploaded to a signed URL and then registered, which suits large
files. --direct sends it to the CMS API in the request instead.`,
		Example: `  $ reearth cms asset create -p my-project -f ./park.geojson
  $ reearth cms asset create -f ./photo.jpg --direct
  $ reearth cms asset create -u https://example.com/image.png`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if (file == "") == (fromURL == "") {
				return cmdutil.FlagErrorf("pass either -f or -u")
			}
			if direct && file == "" {
				return cmdutil.FlagErrorf("--direct needs -f")
			}
			project, err := o.requireProject(f)
			if err != nil {
				return err
			}
			c, err := o.client(cmd.Context(), f)
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			f.IO.StartProgress("Uploading the asset…")
			var a *cmsapi.Asset
			switch {
			case fromURL != "":
				a, err = registerUpload(ctx, c, func() (string, error) { return c.UploadAsset(ctx, project, fromURL) })
			case direct:
				a, err = uploadFile(file, func(r io.Reader) (*cmsapi.Asset, error) {
					return registerUpload(ctx, c, func() (string, error) {
						return c.UploadAssetDirectly(ctx, project, filepath.Base(file), r)
					})
				})
			default:
				a, err = uploadFile(file, func(r io.Reader) (*cmsapi.Asset, error) {
					return uploadSigned(ctx, f, c, project, filepath.Base(file), r)
				})
			}
			f.IO.StopProgress()
			if err != nil {
				return err
			}

			f.IO.Success("Created asset %s", a.ID)
			return printAsset(f, a)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "File to upload")
	cmd.Flags().StringVarP(&fromURL, "url", "u", "", "URL of a file for CMS to fetch")
	cmd.Flags().BoolVar(&direct, "direct", false, "Send the file to the CMS API instead of a signed URL (only with -f)")
	return cmd
}

func uploadFile(path string, upload func(io.Reader) (*cmsapi.Asset, error)) (*cmsapi.Asset, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, &cmdutil.Error{Exit: cmdutil.ExitUsage, Code: "cms.file_unreadable", Err: err,
			Message: err.Error(), Hint: "check the path given to -f"}
	}
	defer func() { _ = fh.Close() }()
	return upload(fh)
}

// registerUpload runs an upload that returns only the new asset's ID, and
// fetches the asset. When it cannot be fetched, the ID alone is returned.
func registerUpload(ctx context.Context, c *cmsapi.CMS, upload func() (string, error)) (*cmsapi.Asset, error) {
	id, err := upload()
	if err != nil {
		return nil, apiError(err, "asset upload")
	}
	if a, err := c.Asset(ctx, id); err == nil {
		return a, nil
	}
	return &cmsapi.Asset{ID: id}, nil
}

// uploadSigned uploads to a signed URL on the storage host, then registers
// the upload as an asset.
func uploadSigned(ctx context.Context, f *core.Factory, c *cmsapi.CMS, project, name string, r io.Reader) (*cmsapi.Asset, error) {
	up, err := c.CreateAssetUpload(ctx, project, name)
	if err != nil {
		return nil, apiError(err, "asset upload")
	}
	if err := c.WithHTTPClient(storageClient(f)).UploadToAssetUpload(ctx, up, r); err != nil {
		return nil, &cmdutil.Error{Exit: cmdutil.ExitError, Code: "cms.upload_failed", Err: err,
			Message: fmt.Sprintf("upload %s: %v", name, err)}
	}
	a, err := c.CreateAssetByToken(ctx, project, up.Token)
	if err != nil {
		return nil, apiError(err, "asset upload")
	}
	return a, nil
}

func newCmdAssetCat(f *core.Factory, o *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "cat <asset-id>",
		Short:   "Write an asset's content to stdout",
		Example: `  $ reearth cms asset cat 01j... > park.geojson`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := getAsset(cmd.Context(), f, o, args[0])
			if err != nil {
				return err
			}
			_, err = download(cmd.Context(), f, a, f.IO.Out)
			return err
		},
	}
}

func newCmdAssetCp(f *core.Factory, o *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "cp <asset-id> <destination>",
		Short:   "Download an asset's content to a file",
		Example: `  $ reearth cms asset cp 01j... ./park.geojson`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, dest := args[0], args[1]
			a, err := getAsset(cmd.Context(), f, o, id)
			if err != nil {
				return err
			}
			fh, err := os.Create(dest)
			if err != nil {
				return &cmdutil.Error{Exit: cmdutil.ExitError, Code: "cms.write_failed", Err: err, Message: err.Error()}
			}
			f.IO.StartProgress("Downloading the asset…")
			n, err := download(cmd.Context(), f, a, fh)
			f.IO.StopProgress()
			if cerr := fh.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				_ = os.Remove(dest)
				return err
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(struct {
				ID    string `json:"id"`
				Path  string `json:"path"`
				Bytes int64  `json:"bytes"`
			}{id, dest, n}, func() error {
				f.IO.Success("Copied asset %s to %s", id, dest)
				return nil
			})
		},
	}
}

// download copies an asset's content from its URL, without credentials.
func download(ctx context.Context, f *core.Factory, a *cmsapi.Asset, w io.Writer) (int64, error) {
	if a.URL == "" {
		return 0, cmdutil.NewError(cmdutil.ExitError, "cms.asset_no_url", fmt.Sprintf("asset %s has no URL", a.ID), "")
	}
	failed := func(err error) error {
		return &cmdutil.Error{Exit: cmdutil.ExitError, Code: "cms.download_failed", Err: err,
			Message: fmt.Sprintf("download asset %s: %v", a.ID, err)}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return 0, failed(err)
	}
	resp, err := storageClient(f).Do(req)
	if err != nil {
		return 0, failed(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, failed(errors.New(resp.Status))
	}
	n, err := io.Copy(w, resp.Body)
	if err != nil {
		return n, failed(err)
	}
	return n, nil
}
