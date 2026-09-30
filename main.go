package main

import (
	"context"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/nitra/terraform-provider-dremio/dremio"
)

// This provider used to be muxed with a legacy terraform-plugin-sdk/v2
// provider while resources were migrated to terraform-plugin-framework one
// at a time (see the migration plan and CHANGELOG for the history). That
// migration is now complete - every resource and the one data source
// (dremio_summary) are on terraform-plugin-framework - so the mux and the
// SDKv2 provider it carried are gone; this just serves the Framework
// provider directly.
func main() {
	err := providerserver.Serve(context.Background(), dremio.NewFrameworkProvider, providerserver.ServeOpts{
		Address: "registry.opentofu.org/nitra/dremio",
	})
	if err != nil {
		log.Fatal(err)
	}
}
