package visualizer

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
)

type initOptions struct {
	dir      string
	template string
}

func newInitCmd(f *core.Factory) *cobra.Command {
	var opts initOptions

	cmd := &cobra.Command{
		Use:   "init [directory]",
		Short: "Initialize a new plugin from a template",
		Long: `Initialize a new Re:Earth Visualizer plugin from a template.

This command downloads a plugin template and sets up a new plugin project in the
specified directory. The template includes all necessary configuration files,
dependencies, and example code to get started quickly.

Currently supported templates:
  - ts (TypeScript, default): Modern TypeScript template with shadcn UI components

The TypeScript template is based on:
https://github.com/reearth-plugins/reearth-visualizer-plugin-shadcn-template`,
		Example: `  # Initialize plugin in current directory
  $ reearth vis plugin init

  # Initialize plugin in a specific directory
  $ reearth vis plugin init my-plugin

  # Explicitly specify TypeScript template
  $ reearth vis plugin init my-plugin --template ts`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.dir = args[0]
			} else {
				opts.dir = "."
			}

			return runInit(cmd.Context(), f, &opts)
		},
	}

	cmd.Flags().StringVarP(&opts.template, "template", "t", "ts", "Template to use (currently only 'ts' is supported)")

	return cmd
}

func runInit(ctx context.Context, f *core.Factory, opts *initOptions) error {
	// Validate template
	if opts.template != "ts" {
		return cmdutil.NewError(cmdutil.ExitError, "plugin.invalid_template",
			fmt.Sprintf("unsupported template: %s", opts.template),
			"currently only 'ts' template is supported")
	}

	// Check if directory exists and is not empty
	if err := checkDirectory(opts.dir); err != nil {
		return err
	}

	f.IO.StartProgress("Downloading plugin template...")

	// Download template
	templateURL := "https://github.com/reearth-plugins/reearth-visualizer-plugin-shadcn-template/archive/refs/heads/main.zip"
	zipData, err := downloadTemplate(ctx, f, templateURL)
	if err != nil {
		f.IO.StopProgress()
		return err
	}

	f.IO.StopProgress()
	f.IO.StartProgress("Extracting template files...")

	// Extract template
	if err := extractTemplate(zipData, opts.dir); err != nil {
		f.IO.StopProgress()
		return err
	}

	f.IO.StopProgress()
	f.IO.Success("Plugin initialized successfully in %s", opts.dir)
	f.IO.Info("")
	f.IO.Info("Done. Now run:")
	if opts.dir != "." {
		f.IO.Info("  cd %s", opts.dir)
	}
	f.IO.Info("  npm install")
	f.IO.Info("  npm run dev")
	f.IO.Info("")
	f.IO.Info("In another terminal, start the development server:")
	f.IO.Info("  reearth vis plugin dev%s", func() string {
		if opts.dir != "." {
			return " " + opts.dir
		}
		return ""
	}())

	return nil
}

func checkDirectory(dir string) error {
	// If directory doesn't exist, that's fine - we'll create it
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to check directory: %w", err)
	}

	// If it exists but is not a directory, that's an error
	if !info.IsDir() {
		return cmdutil.NewError(cmdutil.ExitError, "plugin.not_directory",
			fmt.Sprintf("%s exists but is not a directory", dir),
			"provide a valid directory path")
	}

	// Check if directory is empty
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	if len(entries) > 0 {
		return cmdutil.NewError(cmdutil.ExitError, "plugin.directory_not_empty",
			fmt.Sprintf("directory %s is not empty", dir),
			"provide an empty directory or a new directory path")
	}

	return nil
}

func downloadTemplate(ctx context.Context, f *core.Factory, url string) ([]byte, error) {
	client := f.PublicHTTPClient()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download template: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download template: %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read template: %w", err)
	}

	return data, nil
}

func extractTemplate(zipData []byte, destDir string) error {
	// Open zip reader
	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("failed to read zip: %w", err)
	}

	// Create destination directory if it doesn't exist
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Extract files
	for _, file := range zipReader.File {
		// Skip the root directory (e.g., "reearth-visualizer-plugin-shadcn-template-main/")
		parts := strings.Split(file.Name, "/")
		if len(parts) <= 1 {
			continue
		}

		// Remove the root directory from the path
		relativePath := strings.Join(parts[1:], "/")
		if relativePath == "" {
			continue
		}

		targetPath := filepath.Join(destDir, relativePath)

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
			continue
		}

		// Create parent directory if needed
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory: %w", err)
		}

		// Extract file
		if err := extractFile(file, targetPath); err != nil {
			return err
		}
	}

	return nil
}

func extractFile(file *zip.File, targetPath string) error {
	srcFile, err := file.Open()
	if err != nil {
		return fmt.Errorf("failed to open file in zip: %w", err)
	}
	defer srcFile.Close()

	destFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, srcFile); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}
