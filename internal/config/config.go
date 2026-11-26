// Package config loads and saves rexws's two small JSON files:
// config.json (projects and worktree providers) and state.json (workflow
// metadata). Git and Rex remain the source of truth for what exists.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Provider describes how to create and remove a worktree. The templates are
// rendered and executed in Milestone 3; rexws only stores them today.
type Provider struct {
	Create  []string `json:"create"`
	Remove  []string `json:"remove"`
	Workdir string   `json:"workdir,omitempty"`
}

// Project is one registered Git repository.
type Project struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	Provider     string `json:"provider,omitempty"`
	WorktreeRoot string `json:"worktree_root,omitempty"`
}

// Config is the content of config.json.
type Config struct {
	DefaultProvider string              `json:"default_provider"`
	RexBin          string              `json:"rex_bin,omitempty"`
	RexClient       string              `json:"rex_client,omitempty"`
	Providers       map[string]Provider `json:"providers"`
	Statuses        []StatusDef         `json:"statuses"`
	DefaultStatus   Status              `json:"default_status"`
	Projects        []Project           `json:"projects"`
}

// Default returns the built-in configuration used when no config.json exists.
func Default() Config {
	cfg := Config{
		DefaultProvider: "git",
		Providers: map[string]Provider{
			"git": {
				Create: []string{"git", "-C", "{{.RepoPath}}", "worktree", "add", "-b", "{{.Branch}}", "{{.TargetPath}}"},
				Remove: []string{"git", "-C", "{{.RepoPath}}", "worktree", "remove", "{{.WorktreePath}}"},
			},
		},
	}
	cfg.applyStatusDefaults()
	return cfg
}

// Load reads config.json. A missing file yields the default configuration
// without creating anything on disk.
func Load() (Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.DefaultProvider == "" {
		cfg.DefaultProvider = "git"
	}
	if cfg.Providers == nil {
		cfg.Providers = Default().Providers
	}
	cfg.applyStatusDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Save writes config.json atomically.
func (c Config) Save() error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	return writeJSON(path, c)
}

// Validate rejects configurations that cannot be resolved consistently.
func (c Config) Validate() error {
	if err := c.validateStatuses(); err != nil {
		return err
	}
	names := make(map[string]string, len(c.Projects))
	paths := make(map[string]string, len(c.Projects))
	for i, p := range c.Projects {
		if p.Name == "" {
			return fmt.Errorf("project %d has no name", i)
		}
		if !filepath.IsAbs(p.Path) {
			return fmt.Errorf("project %q path %q is not absolute", p.Name, p.Path)
		}
		if other, ok := names[p.Name]; ok {
			return fmt.Errorf("project name %q is used by both %s and %s", p.Name, other, p.Path)
		}
		if other, ok := paths[p.Path]; ok {
			return fmt.Errorf("project path %q is registered as both %q and %q", p.Path, other, p.Name)
		}
		names[p.Name] = p.Path
		paths[p.Path] = p.Name

		if p.Provider != "" {
			if _, ok := c.Providers[p.Provider]; !ok {
				return fmt.Errorf("project %q names unknown provider %q", p.Name, p.Provider)
			}
		}
		if p.WorktreeRoot != "" && !filepath.IsAbs(p.WorktreeRoot) {
			return fmt.Errorf("project %q worktree_root %q is not absolute", p.Name, p.WorktreeRoot)
		}
	}
	if _, ok := c.Providers[c.DefaultProvider]; !ok && len(c.Projects) > 0 {
		return fmt.Errorf("default provider %q is not defined", c.DefaultProvider)
	}
	return nil
}

// RegisterProject adds a repository to the configuration. It returns the
// stored project and false when the path is already registered.
func (c *Config) RegisterProject(path, name, provider, worktreeRoot string) (Project, bool, error) {
	path = filepath.Clean(path)
	for _, p := range c.Projects {
		if p.Path == path {
			if name != "" && name != p.Name {
				return Project{}, false, fmt.Errorf("project %q is already registered at %s", p.Name, path)
			}
			if provider != "" && provider != p.Provider {
				return Project{}, false, fmt.Errorf("project %q is already registered with provider %q", p.Name, p.Provider)
			}
			if worktreeRoot != "" && worktreeRoot != p.WorktreeRoot {
				return Project{}, false, fmt.Errorf("project %q is already registered with worktree_root %q", p.Name, p.WorktreeRoot)
			}
			return p, false, nil
		}
	}
	if name == "" {
		name = filepath.Base(path)
	}
	if strings.ContainsAny(name, "/\\") {
		return Project{}, false, fmt.Errorf("project name %q must not contain a path separator", name)
	}
	for _, p := range c.Projects {
		if p.Name == name {
			return Project{}, false, fmt.Errorf("project name %q is already used by %s", name, p.Path)
		}
	}
	project := Project{Name: name, Path: path, Provider: provider, WorktreeRoot: worktreeRoot}
	c.Projects = append(c.Projects, project)
	if err := c.Validate(); err != nil {
		c.Projects = c.Projects[:len(c.Projects)-1]
		return Project{}, false, err
	}
	return project, true, nil
}

// Project returns the registered project with the given name.
func (c Config) Project(name string) (Project, bool) {
	for _, p := range c.Projects {
		if p.Name == name {
			return p, true
		}
	}
	return Project{}, false
}

// ProviderFor resolves the provider for a project using the documented order:
// project-level provider, then default_provider, then built-in git.
func (c Config) ProviderFor(p Project) string {
	if p.Provider != "" {
		return p.Provider
	}
	if c.DefaultProvider != "" {
		return c.DefaultProvider
	}
	return "git"
}

// RexBinary returns the rex executable to run. Precedence is REXWS_REX_BIN,
// then rex_bin in config.json, then "rex" from PATH.
func (c Config) RexBinary() string {
	if bin := os.Getenv(EnvRexBin); bin != "" {
		return bin
	}
	if c.RexBin != "" {
		return c.RexBin
	}
	return "rex"
}

// RexAppClient returns the Rex app client to target. Precedence is
// REXWS_REX_CLIENT, then rex_client in config.json, then empty, which lets the
// caller fall back to the first connected Rex app.
func (c Config) RexAppClient() string {
	if client := os.Getenv(EnvRexClient); client != "" {
		return client
	}
	return c.RexClient
}

// SortedProjects returns a copy of the projects sorted by name.
func (c Config) SortedProjects() []Project {
	projects := slices.Clone(c.Projects)
	slices.SortFunc(projects, func(a, b Project) int { return strings.Compare(a.Name, b.Name) })
	return projects
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
