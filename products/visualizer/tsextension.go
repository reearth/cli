package visualizer

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

const tsManifest = "public/" + manifestFile

// addTSExtension adds e to a TS template project, given as files keyed by
// slash-separated path. It needs public/reearth.yml and package.json, and
// adds src/extensions/<id>/ with one UI named "main", like the template's
// scripts/manage.mjs. Unlike manage.mjs it also adds the extension to the
// "build" and "dev-build" scripts, so `yarn build` packages it.
func addTSExtension(files map[string][]byte, e extensionSpec) error {
	manifest, ok := files[tsManifest]
	if !ok {
		return fmt.Errorf("%s not found", tsManifest)
	}
	pkg, ok := files["package.json"]
	if !ok {
		return fmt.Errorf("package.json not found")
	}

	entry, err := execTemplate("templates/ts/extension.yml.tmpl", e)
	if err != nil {
		return err
	}
	if files[tsManifest], err = appendExtension(manifest, entry); err != nil {
		return err
	}
	if files["package.json"], err = addTSScripts(pkg, e.ID); err != nil {
		return err
	}

	// templates/ts/X.tmpl -> src/extensions/<id>/X (extension.ts -> <id>.ts)
	dir := "src/extensions/" + e.ID + "/"
	return fs.WalkDir(templates, "templates/ts", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == "templates/ts/extension.yml.tmpl" {
			return err
		}
		rel := strings.TrimPrefix(p, "templates/ts/")
		b, err := fs.ReadFile(templates, p)
		if err != nil {
			return err
		}
		if name, ok := strings.CutSuffix(rel, ".tmpl"); ok {
			if b, err = execTemplate(p, e); err != nil {
				return err
			}
			rel = name
		}
		if rel == "extension.ts" {
			rel = e.ID + ".ts"
		}
		files[dir+rel] = b
		return nil
	})
}

var (
	reBuildScript    = regexp.MustCompile(`("build"\s*:\s*")([^"]*)(")`)
	reDevBuildScript = regexp.MustCompile(`("dev-build"\s*:\s*")([^"]*)(")`)
)

// addTSScripts adds the scripts for extension ext to package.json, editing
// lines so the file's order and formatting stay as they are.
func addTSScripts(pkg []byte, ext string) ([]byte, error) {
	var m struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(pkg, &m); err != nil {
		return nil, fmt.Errorf("package.json: %w", err)
	}
	if _, ok := m.Scripts["build"]; !ok {
		return nil, fmt.Errorf("package.json has no \"build\" script; is this the Visualizer plugin TS template?")
	}
	if _, ok := m.Scripts["build:"+ext]; ok {
		return nil, fmt.Errorf("package.json already has a build:%s script", ext)
	}

	lines := strings.Split(string(pkg), "\n")
	at := -1
	for i, l := range lines {
		if reBuildScript.MatchString(l) {
			at = i
			break
		}
	}
	if at < 0 {
		return nil, fmt.Errorf("package.json: \"build\" script not found")
	}
	indent := lines[at][:len(lines[at])-len(strings.TrimLeft(lines[at], " \t"))]
	env := "cross-env EXTENSION_NAME=" + ext
	add := []string{
		fmt.Sprintf(`%s"dev:%s:main": "%s UI_NAME=main vite -c configs/ui.ts",`, indent, ext, env),
		fmt.Sprintf(`%s"build:%s:main": "%s UI_NAME=main vite build -c configs/ui.ts",`, indent, ext, env),
		fmt.Sprintf(`%s"build:%s:extension": "%s vite build -c configs/extension.ts",`, indent, ext, env),
		fmt.Sprintf(`%s"build:%[2]s": "run-s build:%[2]s:main build:%[2]s:extension",`, indent, ext),
	}

	// "build": "run-s build:a zip" -> "run-s build:a build:<ext> zip"
	lines[at] = reBuildScript.ReplaceAllStringFunc(lines[at], func(s string) string {
		sm := reBuildScript.FindStringSubmatch(s)
		v := sm[2] + " build:" + ext
		if base, ok := strings.CutSuffix(sm[2], " zip"); ok {
			v = base + " build:" + ext + " zip"
		}
		return sm[1] + v + sm[3]
	})
	// dev-build watches every extension; manage.mjs keeps one dev server.
	watch := fmt.Sprintf("'yarn build:%[1]s:main --watch' 'yarn build:%[1]s:extension --watch' ", ext)
	for i, l := range lines {
		if reDevBuildScript.MatchString(l) && strings.Contains(l, "'vite preview") {
			lines[i] = strings.Replace(l, "'vite preview", watch+"'vite preview", 1)
		}
	}

	lines = append(lines[:at], append(add, lines[at:]...)...)
	out := []byte(strings.Join(lines, "\n"))
	if !json.Valid(out) {
		return nil, fmt.Errorf("package.json: could not add scripts for %s", ext)
	}
	return out, nil
}
