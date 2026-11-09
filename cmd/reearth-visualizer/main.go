// Command reearth-visualizer is the standalone build of the visualizer
// product. `reearth visualizer` is the supported entry point.
package main

import (
	"github.com/reearth/cli/products/visualizer"
	"github.com/reearth/cli/sdk/app"
)

func main() {
	app.Main(visualizer.Product{})
}
