package visualizer

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
)

// maxPackageSize is the upload limit of the Visualizer server
// (pluginPackageSizeLimit in usecase/interactor/plugin_upload.go).
const maxPackageSize = 10 << 20

// packageDir is where pack writes by default, as the TS template's zip script does.
const packageDir = "package"

type packOptions struct {
	Dir      string   `json:"dir"`
	Template string   `json:"template"`
	ID       string   `json:"id"`
	Version  string   `json:"version"`
	File     string   `json:"file"`
	Size     int64    `json:"size"`
	Files    []string `json:"files"`

	output string
}

func newCmdPluginPack(f *core.Factory) *cobra.Command {
	o := &packOptions{}
	cmd := &cobra.Command{
		Use:   "pack",
		Short: "Zip a plugin for uploading to Visualizer",
		Long: `Validate the plugin in --dir (see ` + "`reearth visualizer plugin validate`" + `) and
zip it for uploading to Visualizer. A plugin with problems is not packed.

The zip holds reearth.yml at its top level and:

  js plugin     every file in --dir except hidden files, node_modules,
                package/ and .zip files
  ts template   dist/ (build it first with ` + "`yarn build`" + `), with public/reearth.yml,
                README.md and LICENSE

It is written to <dir>/package/<id>-<version>.zip unless --output is given,
replacing a file of that name. Visualizer accepts packages up to 10 MB.`,
		Example: `  $ reearth visualizer plugin pack
  $ reearth visualizer plugin pack --dir my-plugin --output my-plugin.zip`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.run(f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&o.Dir, "dir", ".", "Plugin directory")
	fl.StringVarP(&o.output, "output", "o", "", "Zip file to write (default: <dir>/package/<id>-<version>.zip)")
	return cmd
}

func (o *packOptions) run(f *core.Factory) error {
	v, err := validatePlugin(o.Dir)
	if err != nil {
		return err
	}
	if !v.Valid {
		for _, i := range v.Issues {
			f.IO.Warn("%s: %s", i.Path, i.Message)
		}
		return invalidError(v)
	}
	o.Template, o.ID, o.Version = v.Template, v.ID, v.Version

	o.File = o.output
	if o.File == "" {
		o.File = filepath.Join(o.Dir, packageDir, o.ID+"-"+o.Version+".zip")
	}
	files, err := packFiles(v, o.File)
	if err != nil {
		return err
	}
	if o.Size, err = writeZip(o.File, files, maxPackageSize); errors.Is(err, errTooLarge) {
		return cmdutil.NewError(cmdutil.ExitError, "visualizer.package_too_large",
			fmt.Sprintf("the package would be %.1f MB; Visualizer accepts up to 10 MB", float64(o.Size)/(1<<20)),
			"remove large files from the plugin, or load large data from a URL at run time")
	} else if err != nil {
		return err
	}
	for name := range files {
		o.Files = append(o.Files, name)
	}
	slices.Sort(o.Files)

	pr, err := f.Printer()
	if err != nil {
		return err
	}
	return pr.Print(o, func() error {
		f.IO.Success("Packed %s %s into %s (%d files, %s)", o.ID, o.Version, o.File, len(o.Files), humanSize(o.Size))
		return nil
	})
}

// packFiles maps each path in the zip to the file to read it from.
func packFiles(v *validation, output string) (map[string]string, error) {
	out := map[string]string{}
	skip := func(p string) bool {
		abs, _ := filepath.Abs(p)
		absOut, _ := filepath.Abs(output)
		return abs == absOut
	}
	walk := func(root string, exclude func(rel string, d fs.DirEntry) bool) error {
		return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			if rel == "." {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(d.Name(), ".") || exclude(rel, d) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type().IsRegular() && !skip(p) {
				out[rel] = p
			}
			return nil
		})
	}

	if v.Template == "ts" {
		// The build output, as the template's scripts/zip.mjs packs it.
		if err := walk(v.root, func(rel string, d fs.DirEntry) bool { return false }); err != nil {
			return nil, err
		}
		for _, name := range []string{"README.md", "LICENSE"} {
			if _, err := os.Stat(filepath.Join(v.Dir, name)); err == nil {
				out[name] = filepath.Join(v.Dir, name)
			}
		}
	} else {
		err := walk(v.Dir, func(rel string, d fs.DirEntry) bool {
			return rel == "node_modules" || rel == packageDir || (!d.IsDir() && strings.EqualFold(filepath.Ext(rel), ".zip"))
		})
		if err != nil {
			return nil, err
		}
	}
	// Always the manifest that was validated, even if a build copied an older one.
	out[manifestFile] = filepath.Join(v.Dir, filepath.FromSlash(v.Manifest))
	return out, nil
}

var errTooLarge = errors.New("package too large")

// writeZip writes files to path through a temporary file, so a failed write
// or a zip over limit never replaces an existing package. It returns the
// size of the zip, also with errTooLarge.
func writeZip(path string, files map[string]string, limit int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pack-*.zip")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	zw := zip.NewWriter(tmp)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names) // the same plugin gives the same order
	for _, name := range names {
		if err := addZipFile(zw, name, files[name]); err != nil {
			_ = tmp.Close()
			return 0, err
		}
	}
	if err := zw.Close(); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	st, err := tmp.Stat()
	if err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if st.Size() > limit {
		return st.Size(), errTooLarge
	}
	return st.Size(), os.Rename(tmp.Name(), path)
}

func addZipFile(zw *zip.Writer, name, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	h, err := zip.FileInfoHeader(st)
	if err != nil {
		return err
	}
	h.Name, h.Method = name, zip.Deflate
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, in)
	return err
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
