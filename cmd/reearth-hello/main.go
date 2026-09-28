// Command reearth-hello is the standalone build of the hello product.
// It is not distributed; `reearth hello` is the supported entry point.
package main

import (
	"github.com/reearth/cli/products/hello"
	"github.com/reearth/cli/sdk/app"
)

func main() {
	app.Main(hello.Product{})
}
