package visualizer

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/core"
)

type story struct {
	ID                string     `json:"id"`
	ProjectID         string     `json:"projectId"`
	Title             string     `json:"title"`
	PublishedAlias    string     `json:"publishedAlias,omitempty"`
	PublishmentStatus string     `json:"publishmentStatus"`
	PublishedAt       *time.Time `json:"publishedAt,omitempty"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

type storyPublishResult struct {
	Story story  `json:"story"`
	URL   string `json:"url,omitempty"`
}

func newCmdStory(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "story",
		Short: "Publish and unpublish a project's story",
		Long: `Publish and unpublish a project's story.

A project has one story, which is published separately from the project's
map, with its own alias and URL. Story commands take the project ID, read
like the project commands: the argument, REEARTH_VISUALIZER_PROJECT, then
visualizer.project in .reearth.yaml. unpublish only takes the argument.`,
	}
	cmd.AddCommand(newCmdStoryPublish(f), newCmdStoryUnpublish(f))
	return cmd
}

func newCmdStoryPublish(f *core.Factory) *cobra.Command {
	var alias string
	var index bool
	cmd := &cobra.Command{
		Use:   "publish [project-id]",
		Short: "Publish a project's story and print its URL",
		Long: `Publish a project's story, or update it with the latest changes, and print
its published URL.

By default the story is published as "limited": search engines are asked not
to index it. --index publishes it as "public". --alias sets the alias in the
story's URL; it is kept when omitted, and a story published for the first
time gets "s-<story-id>".`,
		Example: `  $ reearth vis story publish 01hq...
  $ reearth vis story publish 01hq... --index --alias city-parks-story`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			status := "limited"
			if index {
				status = "public"
			}
			return publishStory(cmd, f, args, status, alias)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&index, "index", false, "Let search engines index the published story")
	fl.StringVar(&alias, "alias", "", "Alias in the story's URL (default: keep the current one)")
	return cmd
}

func newCmdStoryUnpublish(f *core.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "unpublish <project-id>",
		Short: "Take a project's published story offline",
		Long: `Take a project's published story offline. Its alias is kept, so publishing
it again brings back the same URL. Asks for confirmation unless --yes is
passed.`,
		Example: `  $ reearth vis story unpublish 01hq...`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if err := f.Confirm(fmt.Sprintf("Unpublish the story of project %s? Its published URL stops working.", id)); err != nil {
				return err
			}
			return publishStory(cmd, f, []string{id}, "private", "")
		},
	}
}

func publishStory(cmd *cobra.Command, f *core.Factory, args []string, status, alias string) error {
	id, err := resolveProject(f, args)
	if err != nil {
		return err
	}
	c, err := newClient(cmd.Context(), f)
	if err != nil {
		return err
	}
	body := map[string]any{"status": status}
	if alias != "" {
		body["alias"] = alias
	}
	if status != "private" {
		f.IO.StartProgress("Publishing the story…")
	}
	var res storyPublishResult
	err = c.doJSON(cmd.Context(), http.MethodPost, "/projects/"+url.PathEscape(id)+"/story/publish", nil, body, &res)
	f.IO.StopProgress()
	if err != nil {
		return err
	}
	p, err := f.Printer()
	if err != nil {
		return err
	}
	return p.Print(res, func() error {
		if status == "private" {
			f.IO.Success("Unpublished story %s", res.Story.Title)
			return nil
		}
		f.IO.Success("Published story %s (%s)", res.Story.Title, status)
		if res.URL == "" {
			f.IO.Warn("the server did not return a URL (no published host configured); the story's alias is %s", res.Story.PublishedAlias)
			return nil
		}
		_, err := fmt.Fprintln(f.IO.Out, res.URL)
		return err
	})
}
