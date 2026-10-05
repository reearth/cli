package visualizer

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
)

type devOptions struct {
	port    int
	version string
	dir     string
}

func newDevCmd(f *core.Factory) *cobra.Command {
	var opts devOptions

	cmd := &cobra.Command{
		Use:   "dev [plugin-directory]",
		Short: "Start plugin development server",
		Long: `Start a local development server for testing Re:Earth Visualizer plugins.

This command downloads the Visualizer workbench (a standalone viewer) and serves
it alongside your plugin files. Your browser opens automatically to test the plugin.

The workbench is cached locally (~/.reearth-cli/reearth-viz-workbench/) and reused across runs.

NOTE: This is a demo implementation. For production use, please coordinate with
the CLI team for proper integration.`,
		Example: `  # Start dev server in current directory
  $ reearth visualizer plugin dev

  # Start dev server for a specific plugin
  $ reearth visualizer plugin dev ./my-plugin

  # Use a specific workbench version
  $ reearth visualizer plugin dev --version v1.0.0

  # Use nightly workbench (latest development build)
  $ reearth visualizer plugin dev --version nightly`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.dir = args[0]
			} else {
				var err error
				opts.dir, err = os.Getwd()
				if err != nil {
					return err
				}
			}

			return runDev(cmd.Context(), f, &opts)
		},
	}

	cmd.Flags().IntVarP(&opts.port, "port", "p", 3000, "Port to run the workbench server on")
	cmd.Flags().StringVar(&opts.version, "version", "latest", "Workbench version (latest, nightly, or specific version like v1.0.0)")

	return cmd
}

func runDev(ctx context.Context, f *core.Factory, opts *devOptions) error {
	// Ensure plugin directory exists
	if _, err := os.Stat(opts.dir); err != nil {
		return cmdutil.NewError(cmdutil.ExitError, "plugin.not_found",
			fmt.Sprintf("plugin directory not found: %s", opts.dir),
			"provide a valid plugin directory path")
	}

	f.IO.StartProgress(fmt.Sprintf("Setting up workbench (version: %s)...", opts.version))

	// Get workbench path
	workbenchPath, manifest, err := ensureWorkbench(ctx, f, opts.version)
	if err != nil {
		f.IO.StopProgress()
		return err
	}

	f.IO.StopProgress()
	f.IO.Success("Workbench ready (version: %s, plugin API: %s)", manifest.Version, manifest.PluginAPIVersion)

	// Start HTTP server
	return startDevServer(ctx, f, workbenchPath, opts.dir, opts.port)
}

type workbenchManifest struct {
	Version          string `json:"version"`
	PluginAPIVersion string `json:"pluginApiVersion"`
	Commit           string `json:"commit"`
}

func ensureWorkbench(ctx context.Context, f *core.Factory, version string) (string, *workbenchManifest, error) {
	cacheDir := filepath.Join(os.Getenv("HOME"), ".reearth-cli", "reearth-viz-workbench")
	versionDir := filepath.Join(cacheDir, version)
	manifestPath := filepath.Join(versionDir, "workbench-manifest.json")

	// Check if already cached
	if data, err := os.ReadFile(manifestPath); err == nil {
		var manifest workbenchManifest
		if json.Unmarshal(data, &manifest) == nil {
			f.IO.StopProgress()
			f.IO.Info("Using cached workbench from %s", versionDir)
			f.IO.StartProgress(fmt.Sprintf("Setting up workbench (version: %s)...", version))
			return versionDir, &manifest, nil
		}
	}

	// Download workbench
	f.IO.StopProgress()
	f.IO.StartProgress(fmt.Sprintf("Fetching release info for workbench %s from GitHub...", version))

	downloadURL, err := resolveDownloadURL(ctx, f, version)
	if err != nil {
		return "", nil, err
	}

	f.IO.StopProgress()
	f.IO.Info("Download URL resolved: %s", downloadURL)
	f.IO.StartProgress("Downloading and extracting workbench (this may take a minute)...")

	if err := os.MkdirAll(versionDir, 0755); err != nil {
		return "", nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	if err := downloadAndExtract(ctx, f, downloadURL, versionDir); err != nil {
		return "", nil, err
	}

	f.IO.StopProgress()
	f.IO.StartProgress(fmt.Sprintf("Setting up workbench (version: %s)...", version))

	// Read manifest
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read workbench manifest: %w", err)
	}

	var manifest workbenchManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", nil, fmt.Errorf("failed to parse workbench manifest: %w", err)
	}

	return versionDir, &manifest, nil
}

func resolveDownloadURL(ctx context.Context, f *core.Factory, version string) (string, error) {
	client := f.PublicHTTPClient()

	var apiURL string
	switch version {
	case "latest":
		apiURL = "https://api.github.com/repos/reearth/reearth-visualizer/releases/latest"
	case "nightly":
		apiURL = "https://api.github.com/repos/reearth/reearth-visualizer/releases/tags/workbench-nightly"
	default:
		// Specific version
		tag := version
		if tag[0] != 'v' {
			tag = "v" + tag
		}
		return fmt.Sprintf("https://github.com/reearth/reearth-visualizer/releases/download/%s/reearth-viz-workbench_%s.tar.gz", tag, tag), nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch release info from GitHub API: %w (URL: %s)", err, apiURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read error body for more details
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to fetch release info: %s (URL: %s, Response: %s)", resp.Status, apiURL, string(bodyBytes))
	}

	var release struct {
		Assets []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("failed to parse release info: %w", err)
	}

	// Find workbench asset
	for _, asset := range release.Assets {
		// Check if it's a .tar.gz file
		if !strings.HasSuffix(asset.Name, ".tar.gz") {
			continue
		}

		// Match nightly version
		if version == "nightly" && asset.Name == "reearth-viz-workbench_nightly.tar.gz" {
			return asset.BrowserDownloadURL, nil
		}

		// Match latest version (must start with "reearth-viz-workbench_" but not be nightly)
		if version == "latest" && strings.HasPrefix(asset.Name, "reearth-viz-workbench_") && asset.Name != "reearth-viz-workbench_nightly.tar.gz" {
			return asset.BrowserDownloadURL, nil
		}
	}

	return "", fmt.Errorf("workbench asset not found in release")
}

func downloadAndExtract(ctx context.Context, f *core.Factory, url, destDir string) error {
	client := f.PublicHTTPClient()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download workbench from %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to download workbench: %s (URL: %s, Response: %s)", resp.Status, url, string(bodyBytes))
	}

	// Extract tar.gz
	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to decompress workbench: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar: %w", err)
		}

		target := filepath.Join(destDir, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}

	return nil
}

func startDevServer(ctx context.Context, f *core.Factory, workbenchPath, pluginDir string, port int) error {
	mux := http.NewServeMux()

	// Serve workbench files
	workbenchFS := http.FileServer(http.Dir(workbenchPath))
	mux.Handle("/", workbenchFS)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	url := fmt.Sprintf("http://%s/workbench.html?dev-plugin=http://localhost:5173", addr)

	f.IO.Success("Development server started at %s", url)
	f.IO.Info("Press Ctrl+C to stop")
	fmt.Fprintln(f.IO.Out)

	// Open browser
	if f.OpenBrowser != nil {
		_ = f.OpenBrowser(url)
	}

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Start server in a goroutine
	errChan := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("server error: %w", err)
		}
		close(errChan)
	}()

	// Wait for context cancellation (Ctrl+C) or server error
	select {
	case <-ctx.Done():
		f.IO.Info("Shutting down server...")

		// Give the server 5 seconds to gracefully shutdown
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("server shutdown error: %w", err)
		}

		f.IO.Success("Server stopped gracefully")
		return nil
	case err := <-errChan:
		return err
	}
}
