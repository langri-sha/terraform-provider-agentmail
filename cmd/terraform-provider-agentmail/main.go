// Command terraform-provider-agentmail is the Terraform provider entrypoint
// for AgentMail. The provider itself lives in internal/provider.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/langri-sha/terraform-provider-agentmail/internal/provider"
)

// Set at build time through -ldflags by GoReleaser.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/langri-sha/agentmail",
		Debug:   debug,
	}

	if err := providerserver.Serve(context.Background(), provider.New(version, commit), opts); err != nil {
		log.Fatal(err.Error())
	}
}
