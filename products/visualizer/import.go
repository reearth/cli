package visualizer

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
)

const (
	// importChunkSize is the chunk size the server's import upload expects:
	// every chunk but the last must be exactly this size.
	importChunkSize = 16 << 20
	// maxImportSize is the largest zip the server imports.
	maxImportSize = 500 << 20
)

// importPollInterval is how often waitForImport checks the import status.
var importPollInterval = 2 * time.Second

type importChunkResponse struct {
	ProjectID string `json:"projectId"`
	Received  int    `json:"received"`
	Total     int    `json:"total"`
	Completed bool   `json:"completed"`
}

func newCmdProjectImport(f *core.Factory) *cobra.Command {
	var workspace string
	var noWait bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Import a project from an export zip",
		Long: `Import a project from a zip made by "project export" or the web app's
export, as a new project in the workspace.

The zip is uploaded the way the server's web app does: straight to storage
through a signed URL on the hosted service, or in chunks to a self-hosted
server. Then the server imports it in the background. The command waits for
the import to finish and prints the new project; with --no-wait it prints the
project ID right after the upload, and "project view <id>" shows the import
status (UPLOADING, PROCESSING, SUCCESS or FAILED).`,
		Example: `  $ reearth vis project import city-parks.zip
  $ reearth vis project import city-parks.zip --workspace 01hq... --no-wait`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout <= 0 {
				return cmdutil.FlagErrorf("--timeout must be greater than 0")
			}
			path := args[0]
			file, err := os.Open(path)
			if err != nil {
				return cmdutil.FlagErrorf("%v", err)
			}
			defer func() { _ = file.Close() }()
			info, err := file.Stat()
			if err != nil {
				return err
			}
			size := info.Size()
			if size == 0 {
				return cmdutil.FlagErrorf("%s is empty", path)
			}
			if size > maxImportSize {
				return cmdutil.FlagErrorf("%s is %s; the server imports zips up to %s", path, humanBytes(size), humanBytes(maxImportSize))
			}
			if magic := make([]byte, 4); readFull(file, magic) != nil || !bytes.Equal(magic, []byte("PK\x03\x04")) {
				return cmdutil.FlagErrorf("%s is not a zip file", path)
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				return err
			}

			ws, err := resolveWorkspace(f, workspace)
			if err != nil {
				return err
			}
			c, err := newClient(cmd.Context(), f)
			if err != nil {
				return err
			}

			projectID, uploaded, err := uploadImportToURL(cmd.Context(), f, c, ws, file, size)
			if err != nil {
				return err
			}
			if !uploaded {
				res, err := uploadImport(cmd.Context(), f, c, ws, file, size)
				if err != nil {
					return err
				}
				projectID = res.ProjectID
			}
			if noWait {
				p, err := f.Printer()
				if err != nil {
					return err
				}
				return p.Print(map[string]any{"projectId": projectID, "importStatus": "PROCESSING"}, func() error {
					f.IO.Success("Uploaded %s; the import is running", path)
					f.IO.Hint("check it with: reearth vis project view %s", projectID)
					_, err := fmt.Fprintln(f.IO.Out, projectID)
					return err
				})
			}

			pr, err := waitForImport(cmd.Context(), f, c, projectID, timeout)
			if err != nil {
				return err
			}
			f.IO.Success("Imported %s as project %s", path, pr.Name)
			return printProject(f, pr)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&workspace, "workspace", "w", "", workspaceUsage)
	fl.BoolVar(&noWait, "no-wait", false, "Return after the upload without waiting for the import")
	fl.DurationVar(&timeout, "timeout", 10*time.Minute, "How long to wait for the import")
	return cmd
}

func readFull(r io.Reader, b []byte) error {
	_, err := io.ReadFull(r, b)
	return err
}

type importUploadResponse struct {
	ProjectID   string `json:"projectId"`
	UploadURL   string `json:"uploadUrl"`
	ContentType string `json:"contentType"`
}

// uploadImportToURL uploads the zip through a signed storage URL, as the
// hosted web app does. It reports false, without an error, when the server
// imports through chunked upload instead.
func uploadImportToURL(ctx context.Context, f *core.Factory, c *client, ws string, file io.ReaderAt, size int64) (string, bool, error) {
	var res importUploadResponse
	err := c.doJSON(ctx, http.MethodPost, "/workspaces/"+url.PathEscape(ws)+"/projects/import-url", nil, nil, &res)
	if e := (*cmdutil.Error)(nil); errors.As(err, &e) && e.Code == "visualizer.unsupported" {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	if err := putImport(ctx, f, &res, file, size); err != nil {
		return "", false, discardImport(ctx, c, res.ProjectID, err)
	}
	return res.ProjectID, true, nil
}

func putImport(ctx context.Context, f *core.Factory, res *importUploadResponse, file io.ReaderAt, size int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, res.UploadURL, io.NewSectionReader(file, 0, size))
	if err != nil {
		return err
	}
	// Each attempt reads the file from the start, so the client can retry
	// the upload after a temporary storage error.
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(io.NewSectionReader(file, 0, size)), nil }
	req.ContentLength = size
	req.Header.Set("Content-Type", res.ContentType)
	f.IO.StartProgress("Uploading…")
	// The URL carries its own authorization: the Re:Earth token must not go
	// to storage, so this uses the client without credentials.
	resp, err := streamClient(f.PublicHTTPClient()).Do(req)
	f.IO.StopProgress()
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("upload to storage failed: %s: %s", resp.Status, bytes.TrimSpace(b))
	}
	return nil
}

func discardImport(ctx context.Context, c *client, id string, cause error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	path := "/projects/" + url.PathEscape(id)
	if err := c.doJSON(ctx, http.MethodDelete, path, nil, nil, nil); err != nil {
		return &cmdutil.Error{
			Exit:    cmdutil.ExitCode(cause),
			Code:    "visualizer.import_upload_failed",
			Message: fmt.Sprintf("%v; the unfinished project %s could not be removed", cause, id),
			Hint:    "delete it with: reearth vis project delete " + id,
			Err:     cause,
		}
	}
	_ = c.doJSON(ctx, http.MethodDelete, path, url.Values{"permanent": {"true"}}, nil, nil)
	return cause
}

// uploadImport sends the zip in chunks, in order, and returns the response
// to the last one.
func uploadImport(ctx context.Context, f *core.Factory, c *client, ws string, file io.Reader, size int64) (*importChunkResponse, error) {
	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, err
	}
	fileID := "cli-" + hex.EncodeToString(idBytes)
	total := int((size + importChunkSize - 1) / importChunkSize)
	path := "/workspaces/" + url.PathEscape(ws) + "/projects/import"

	buf := make([]byte, importChunkSize)
	var res importChunkResponse
	for i := 0; i < total; i++ {
		n, err := io.ReadFull(file, buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, err
		}
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		_ = w.WriteField("file_id", fileID)
		_ = w.WriteField("chunk_num", strconv.Itoa(i))
		_ = w.WriteField("total_chunks", strconv.Itoa(total))
		part, err := w.CreateFormFile("file", "project.zip")
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(buf[:n]); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}

		f.IO.StartProgress(fmt.Sprintf("Uploading (%d/%d)…", i+1, total))
		err = c.do(ctx, http.MethodPost, path, nil, &body, w.FormDataContentType(), &res)
		f.IO.StopProgress()
		if err != nil {
			return nil, err
		}
	}
	if !res.Completed {
		return nil, fmt.Errorf("the server did not report the upload as complete (%d of %d chunks received)", res.Received, res.Total)
	}
	return &res, nil
}

// waitForImport polls the project until its import ends.
func waitForImport(ctx context.Context, f *core.Factory, c *client, projectID string, timeout time.Duration) (*project, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	f.IO.StartProgress("Importing…")
	defer f.IO.StopProgress()

	for {
		var pr project
		err := c.doJSON(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID), nil, nil, &pr)
		if e := (*cmdutil.Error)(nil); errors.As(err, &e) && e.Exit == cmdutil.ExitNotFound {
			return nil, cmdutil.NewError(cmdutil.ExitError, "visualizer.import_failed",
				fmt.Sprintf("import of project %s failed: the project was removed", projectID), "")
		}
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			return nil, err
		}
		switch pr.ImportStatus {
		case "SUCCESS", "":
			return &pr, nil
		case "FAILED":
			msg := pr.ImportError
			if msg == "" {
				msg = "no reason given"
			}
			return nil, cmdutil.NewError(cmdutil.ExitError, "visualizer.import_failed",
				fmt.Sprintf("import of project %s failed: %s", projectID, msg), "")
		}
		select {
		case <-ctx.Done():
		case <-time.After(importPollInterval):
			continue
		}
		break
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, ctx.Err()
	}
	return nil, cmdutil.NewError(cmdutil.ExitError, "visualizer.import_timeout",
		fmt.Sprintf("the import of project %s did not finish within %s", projectID, timeout),
		fmt.Sprintf("check it later with: reearth vis project view %s", projectID))
}
