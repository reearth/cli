package visualizer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

// The official TS template (reearth-plugins/reearth-visualizer-plugin-shadcn-template)
// ships one extension named "demo". These are the places that name appears.
const tsTemplateExtension = "demo"

var (
	reScriptName  = regexp.MustCompile(`([:=])` + tsTemplateExtension + `([:" '])`)
	rePackageName = regexp.MustCompile(`"name":\s*"[^"]*"`)
)

// renderTS downloads the template and renames it for o.
func renderTS(ctx context.Context, x *fetcher, o *initOptions) (map[string][]byte, error) {
	url := tsTemplateURL()
	tgz, cached, err := x.get(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("download TS template: %w", err)
	}
	files, err := untar(tgz)
	if err != nil && cached {
		// A corrupt cached copy: download it once more.
		x.drop(url)
		if tgz, _, err = x.get(ctx, url); err != nil {
			return nil, fmt.Errorf("download TS template: %w", err)
		}
		files, err = untar(tgz)
	}
	if err != nil {
		return nil, fmt.Errorf("TS template: %w", err)
	}

	src, ok := files[tsManifest]
	if !ok {
		return nil, fmt.Errorf("TS template: %s not found", tsManifest)
	}
	if files[tsManifest], err = editManifest(src, manifestEdit{
		ID: o.ID, Name: o.Name,
		ExtensionID: o.ExtensionID, ExtensionType: o.Type, ExtensionName: o.Name,
	}); err != nil {
		return nil, err
	}

	ext := o.ExtensionID
	oldDir := "src/extensions/" + tsTemplateExtension + "/"
	out := make(map[string][]byte, len(files))
	for name, b := range files {
		if rest, ok := strings.CutPrefix(name, oldDir); ok {
			if rest == tsTemplateExtension+".ts" {
				rest = ext + ".ts"
				b = bytes.ReplaceAll(b, []byte("@distui/"+tsTemplateExtension+"/"), []byte("@distui/"+ext+"/"))
			}
			name = "src/extensions/" + ext + "/" + rest
		}
		if name == "package.json" {
			b = renamePackageJSON(b, o.ID, ext)
		}
		out[name] = b
	}
	for _, e := range o.More {
		if err := addTSExtension(out, e); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func renamePackageJSON(b []byte, pkgName, ext string) []byte {
	lines := strings.Split(string(b), "\n")
	inScripts := false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, `"name":`) && !inScripts:
			lines[i] = rePackageName.ReplaceAllString(l, fmt.Sprintf(`"name": %q`, pkgName))
		case strings.HasPrefix(t, `"scripts":`):
			inScripts = true
		case inScripts && strings.HasPrefix(t, "}"):
			inScripts = false
		case inScripts:
			lines[i] = reScriptName.ReplaceAllString(l, "${1}"+ext+"${2}")
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

// untar reads a GitHub tarball and strips its top-level directory.
func untar(tgz []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		_, name, ok := strings.Cut(h.Name, "/")
		if !ok || name == "" {
			continue
		}
		name = path.Clean(name)
		if strings.HasPrefix(name, "../") || path.IsAbs(name) || strings.HasPrefix(name, ".git/") {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, 32<<20))
		if err != nil {
			return nil, err
		}
		files[name] = b
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("archive is empty")
	}
	return files, nil
}
