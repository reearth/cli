package cms

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	cmsapi "github.com/reearth/reearth-cms-api/go"
	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
)

func newCmdAsset(f *core.Factory, o *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "asset",
		Short: "Show, upload and download assets",
		Long: `Show, upload and download the assets of a project.

An asset is a file stored by CMS, such as an image or a GeoJSON file, that
items refer to by its ID. Its content is downloaded from the asset's URL.`,
	}
	cmd.AddCommand(newCmdAssetGet(f, o), newCmdAssetCreate(f, o), newCmdAssetCat(f, o), newCmdAssetCp(f, o))
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
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(a, nil)
		},
	}
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
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(a, nil)
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
		return nil, err
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
		return nil, fmt.Errorf("upload %s: %w", name, err)
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
				return err
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := storageClient(f).Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download asset %s: %s", a.ID, resp.Status)
	}
	return io.Copy(w, resp.Body)
}
