// Command reearth-cms is the standalone build of the cms product.
// `reearth cms` is the supported entry point.
package main

import (
	"github.com/reearth/cli/products/cms"
	"github.com/reearth/cli/sdk/app"
)

func main() {
	app.Main(cms.Product{})
}
