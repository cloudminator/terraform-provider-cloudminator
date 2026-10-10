// Package provider implements the Cloudminator Terraform / OpenTofu provider.
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/cloudminator/terraform-provider-cloudminator/internal/client"
)

// Environment variables read when the matching provider argument is unset.
const (
	EnvEndpoint = "CLOUDMINATOR_ENDPOINT"
	EnvAPIKey   = "CLOUDMINATOR_API_KEY"
	EnvOrg      = "CLOUDMINATOR_ORG"
	EnvProject  = "CLOUDMINATOR_PROJECT"
)

var _ provider.Provider = &cloudminatorProvider{}

type cloudminatorProvider struct {
	version string
}

// New returns a provider factory.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &cloudminatorProvider{version: version}
	}
}

type providerModel struct {
	Endpoint types.String `tfsdk:"endpoint"`
	APIKey   types.String `tfsdk:"api_key"`
	Org      types.String `tfsdk:"org"`
	Project  types.String `tfsdk:"project"`
}

// providerData is handed to every resource and data source.
type providerData struct {
	client  *client.Client
	org     string
	project string
}

func (p *cloudminatorProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "cloudminator"
	resp.Version = p.version
}

func (p *cloudminatorProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage resources on the [Cloudminator](https://cloudminator.com) sovereign cloud platform. " +
			"Works with Terraform and OpenTofu.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "Base URL of the Cloudminator platform API, e.g. `https://platform.example.com`. " +
					"May also be set with the `" + EnvEndpoint + "` environment variable.",
				Optional: true,
			},
			"api_key": schema.StringAttribute{
				MarkdownDescription: "API key (`cm_…`) used as a bearer token. Create one in the console under " +
					"*Organization → API keys*. May also be set with `" + EnvAPIKey + "`.",
				Optional:  true,
				Sensitive: true,
			},
			"org": schema.StringAttribute{
				MarkdownDescription: "Default organization slug for resources that do not set `org`. " +
					"May also be set with `" + EnvOrg + "`.",
				Optional: true,
			},
			"project": schema.StringAttribute{
				MarkdownDescription: "Default project slug for project-scoped resources that do not set `project`. " +
					"May also be set with `" + EnvProject + "`. Defaults to `default`.",
				Optional: true,
			},
		},
	}
}

func valueOrEnv(v types.String, env string) string {
	if !v.IsNull() && !v.IsUnknown() && v.ValueString() != "" {
		return v.ValueString()
	}
	return os.Getenv(env)
}

func (p *cloudminatorProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.Endpoint.IsUnknown() || cfg.APIKey.IsUnknown() {
		// Values come from not-yet-applied resources; defer until they are known.
		return
	}
	endpoint := valueOrEnv(cfg.Endpoint, EnvEndpoint)
	apiKey := valueOrEnv(cfg.APIKey, EnvAPIKey)
	if endpoint == "" {
		resp.Diagnostics.AddAttributeError(path.Root("endpoint"), "Missing Cloudminator API endpoint",
			"Set the provider's endpoint argument or the "+EnvEndpoint+" environment variable.")
	}
	if apiKey == "" {
		resp.Diagnostics.AddAttributeWarning(path.Root("api_key"), "No Cloudminator API key",
			"Neither api_key nor "+EnvAPIKey+" is set. Only public catalog data sources (regions, flavors, images, plans) will work.")
	}
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := client.New(endpoint, apiKey, p.version)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("endpoint"), "Invalid Cloudminator API endpoint", err.Error())
		return
	}
	project := valueOrEnv(cfg.Project, EnvProject)
	if project == "" {
		project = "default"
	}
	data := &providerData{client: c, org: valueOrEnv(cfg.Org, EnvOrg), project: project}
	resp.ResourceData = data
	resp.DataSourceData = data
}

func (p *cloudminatorProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewProjectResource,
		NewSSHKeyResource,
		NewBucketResource,
		NewBucketKeyResource,
		NewVPCResource,
		NewSubnetResource,
		NewFirewallResource,
		NewVMResource,
		NewSecretResource,
	}
}

func (p *cloudminatorProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewRegionsDataSource,
		NewFlavorsDataSource,
		NewImagesDataSource,
		NewPlansDataSource,
	}
}
