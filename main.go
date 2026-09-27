package main

import (
	"context"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6/tf6server"
	"github.com/hashicorp/terraform-plugin-mux/tf5to6server"
	"github.com/hashicorp/terraform-plugin-mux/tf6muxserver"

	"github.com/nitra/terraform-provider-dremio/dremio"
)

// This provider is muxed: the legacy terraform-plugin-sdk/v2 provider (all
// resources except dremio_source) is upgraded from protocol 5 to 6 and
// combined with the terraform-plugin-framework provider (dremio_source).
// See the approved migration plan for why - in short, dremio_source is the
// only resource this provider's users actually have in state, so it's the
// only one worth migrating; the rest stay on SDKv2 under the mux
// indefinitely rather than as a "temporary" bridge.
func main() {
	ctx := context.Background()

	upgradedSdkServer, err := tf5to6server.UpgradeServer(ctx, dremio.Provider().GRPCProvider)
	if err != nil {
		log.Fatal(err)
	}

	providers := []func() tfprotov6.ProviderServer{
		providerserver.NewProtocol6(dremio.NewFrameworkProvider()),
		func() tfprotov6.ProviderServer {
			return upgradedSdkServer
		},
	}

	muxServer, err := tf6muxserver.NewMuxServer(ctx, providers...)
	if err != nil {
		log.Fatal(err)
	}

	err = tf6server.Serve("registry.opentofu.org/nitra/dremio", muxServer.ProviderServer)
	if err != nil {
		log.Fatal(err)
	}
}
