package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

var projectFileNames = []string{".reearth.yaml", ".reearth.yml"}

// Project is a per-directory config file (.reearth.yaml), meant to be committed.
//
//	account: work
//	workspace: 01hq...
//	cms:
//	  project: my-project
type Project struct {
	Account   string                    `yaml:"account,omitempty"`
	Workspace string                    `yaml:"workspace,omitempty"`
	Products  map[string]map[string]any `yaml:",inline"`

	path string
}

// FindProject searches dir and its parents for a project file. It returns nil if none is found.
func FindProject(dir string) (*Project, error) {
	for {
		for _, name := range projectFileNames {
			p := filepath.Join(dir, name)
			b, err := os.ReadFile(p)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			pr := &Project{path: p}
			if err := yaml.Unmarshal(b, pr); err != nil {
				return nil, fmt.Errorf("parse %s: %w", p, err)
			}
			if err := pr.validate(); err != nil {
				return nil, err
			}
			return pr, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil
		}
		dir = parent
	}
}

func (p *Project) Path() string { return p.path }

// Value returns a product-scoped value such as cms.project.
func (p *Project) Value(product, key string) (string, bool) {
	if p == nil {
		return "", false
	}
	v, ok := p.Products[product][key]
	if !ok || v == nil {
		return "", false
	}
	return fmt.Sprint(v), true
}

// secretLikeKeys are refused so that project files stay safe to commit.
var secretLikeKeys = []string{"token", "secret", "password", "credential", "api_key", "apikey"}

func (p *Project) validate() error {
	for product, values := range p.Products {
		for k := range values {
			lk := strings.ToLower(k)
			for _, s := range secretLikeKeys {
				if strings.Contains(lk, s) {
					return fmt.Errorf("%s: %s.%s looks like a secret; project files must not contain credentials (use `reearth login --with-token` instead)", p.path, product, k)
				}
			}
		}
	}
	return nil
}
