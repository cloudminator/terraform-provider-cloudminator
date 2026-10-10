package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/cloudminator/terraform-provider-cloudminator/internal/provider"
)

// Run "go generate" to format example terraform files and generate the docs for the registry/website.

//go:generate terraform fmt -recursive ./examples/
//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate -provider-name cloudminator

// version is set by goreleaser via -ldflags.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/cloudminator/cloudminator",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
