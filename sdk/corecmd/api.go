package corecmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
)

func NewCmdAPI(f *core.Factory) *cobra.Command {
	var (
		method  string
		fields  []string
		typed   []string
		headers []string
		input   string
		include bool
	)
	cmd := &cobra.Command{
		Use:   "api <product> <path>",
		Short: "Send an authenticated request to a product API",
		Long: `Send an authenticated HTTP request to a product API and print the response.

<path> is relative to the product's API base URL. Absolute URLs are refused
so that credentials are never sent to other hosts.

-f key=value adds a string field and -F key=value adds a typed field
(numbers, true, false, null). Fields go into the query string for GET and
into a JSON body otherwise.`,
		Example: `  $ reearth api hello /userinfo
  $ reearth api cms /projects -F page=2 --jq '.projects[].alias'
  $ reearth api cms /models/xxx/items -X POST --input item.json`,
		Args: cobra.ExactArgs(2),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			var names []string
			for _, p := range f.Products {
				if _, ok := p.(core.APIProduct); ok {
					names = append(names, p.Name()+"\t"+p.Short())
				}
			}
			return names, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			product, ok := f.FindProduct(args[0])
			if !ok {
				return cmdutil.FlagErrorf("unknown product %q", args[0])
			}
			method = strings.ToUpper(method)
			path := args[1]
			if strings.Contains(path, "://") {
				return cmdutil.FlagErrorf("<path> must be relative to the %s API base URL", product.Name())
			}
			base, err := f.BaseURL(product)
			if err != nil {
				return err
			}
			u, err := url.Parse(base + "/" + strings.TrimLeft(path, "/"))
			if err != nil {
				return cmdutil.FlagErrorf("invalid path: %v", err)
			}

			params := map[string]any{}
			for _, kv := range fields {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return cmdutil.FlagErrorf("invalid -f %q (want key=value)", kv)
				}
				params[k] = v
			}
			for _, kv := range typed {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return cmdutil.FlagErrorf("invalid -F %q (want key=value)", kv)
				}
				params[k] = typedValue(v)
			}

			var body io.Reader
			var bodyBytes []byte
			switch {
			case input != "":
				if len(params) > 0 {
					return cmdutil.FlagErrorf("--input cannot be combined with -f/-F")
				}
				if input == "-" {
					bodyBytes, err = io.ReadAll(f.IO.In)
				} else {
					bodyBytes, err = os.ReadFile(input)
				}
				if err != nil {
					return err
				}
			case len(params) > 0 && method != "" && method != http.MethodGet && method != http.MethodHead:
				bodyBytes, _ = json.Marshal(params)
			case len(params) > 0:
				if method == "" {
					method = http.MethodGet
				}
				q := u.Query()
				keys := make([]string, 0, len(params))
				for k := range params {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					q.Set(k, fmt.Sprint(params[k]))
				}
				u.RawQuery = q.Encode()
			}
			if method == "" {
				method = http.MethodGet
				if bodyBytes != nil {
					method = http.MethodPost
				}
			}
			if bodyBytes != nil {
				body = bytes.NewReader(bodyBytes)
			}

			req, err := http.NewRequestWithContext(cmd.Context(), method, u.String(), body)
			if err != nil {
				return err
			}
			if bodyBytes != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("Accept", "application/json, */*")
			for _, h := range headers {
				k, v, ok := strings.Cut(h, ":")
				if !ok {
					return cmdutil.FlagErrorf("invalid -H %q (want 'Name: value')", h)
				}
				req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
			}

			client, err := f.HTTPClient(cmd.Context(), product)
			if err != nil {
				return err
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer func() { _ = resp.Body.Close() }()
			respBody, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}

			if include {
				_, _ = fmt.Fprintf(f.IO.Out, "%s %s\n", resp.Proto, resp.Status)
				names := make([]string, 0, len(resp.Header))
				for k := range resp.Header {
					names = append(names, k)
				}
				sort.Strings(names)
				for _, k := range names {
					for _, v := range resp.Header[k] {
						_, _ = fmt.Fprintf(f.IO.Out, "%s: %s\n", k, v)
					}
				}
				_, _ = fmt.Fprintln(f.IO.Out)
			}

			mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
			isJSON := mt == "application/json" || strings.HasSuffix(mt, "+json")
			var decoded any
			if isJSON && len(respBody) > 0 && json.Unmarshal(respBody, &decoded) == nil && (p.IsMachine() || f.IO.IsStdoutTTY()) {
				if p.IsMachine() {
					err = p.Print(decoded, nil)
				} else {
					var buf bytes.Buffer
					if json.Indent(&buf, respBody, "", "  ") == nil {
						buf.WriteByte('\n')
						_, err = f.IO.Out.Write(buf.Bytes())
					}
				}
			} else {
				_, err = f.IO.Out.Write(respBody)
			}
			if err != nil {
				return err
			}

			if resp.StatusCode >= 400 {
				exit := cmdutil.ExitError
				switch resp.StatusCode {
				case http.StatusUnauthorized, http.StatusForbidden:
					exit = cmdutil.ExitAuth
				case http.StatusNotFound:
					exit = cmdutil.ExitNotFound
				}
				return cmdutil.NewError(exit, "api.http_"+strconv.Itoa(resp.StatusCode), "HTTP "+resp.Status, "")
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&method, "method", "X", "", "HTTP method (default GET, or POST with a body)")
	fl.StringArrayVarP(&fields, "field", "f", nil, "Add a string field key=value")
	fl.StringArrayVarP(&typed, "typed-field", "F", nil, "Add a typed field key=value (number, bool, null)")
	fl.StringArrayVarP(&headers, "header", "H", nil, "Add a request header 'Name: value'")
	fl.StringVar(&input, "input", "", "Read the request body from a file (\"-\" for stdin)")
	fl.BoolVarP(&include, "include", "i", false, "Print the status line and response headers")
	return cmd
}

func typedValue(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if i, err := strconv.ParseInt(v, 10, 64); err == nil {
		return i
	}
	if fl, err := strconv.ParseFloat(v, 64); err == nil {
		return fl
	}
	return v
}
