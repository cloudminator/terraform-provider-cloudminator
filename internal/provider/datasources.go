package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/cloudminator/terraform-provider-cloudminator/internal/client"
)

// ============================== regions ==============================

// NewRegionsDataSource is the cloudminator_regions factory.
func NewRegionsDataSource() datasource.DataSource { return &regionsDataSource{} }

type regionsDataSource struct{ data *providerData }

type capabilitiesModel struct {
	Compute types.Bool `tfsdk:"compute"`
	Storage types.Bool `tfsdk:"storage"`
}

type zoneModel struct {
	ID           types.String      `tfsdk:"id"`
	Capabilities capabilitiesModel `tfsdk:"capabilities"`
	Simulated    types.Bool        `tfsdk:"simulated"`
}

type regionModel struct {
	ID           types.String      `tfsdk:"id"`
	Name         types.String      `tfsdk:"name"`
	Location     types.String      `tfsdk:"location"`
	Zones        []zoneModel       `tfsdk:"zones"`
	Capabilities capabilitiesModel `tfsdk:"capabilities"`
	Simulated    types.Bool        `tfsdk:"simulated"`
}

type regionsModel struct {
	Regions []regionModel `tfsdk:"regions"`
}

func capabilitiesSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: "What can be hosted here.",
		Computed:            true,
		Attributes: map[string]schema.Attribute{
			"compute": schema.BoolAttribute{Computed: true, MarkdownDescription: "VMs can be placed here."},
			"storage": schema.BoolAttribute{Computed: true, MarkdownDescription: "Buckets can be placed here."},
		},
	}
}

func (d *regionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_regions"
}

func (d *regionsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureDataSource(req, resp)
}

func (d *regionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Regions and zones currently offered (derived from the registered fleet).",
		Attributes: map[string]schema.Attribute{
			"regions": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":       schema.StringAttribute{Computed: true, MarkdownDescription: "Region id, e.g. `eu-central-1`."},
					"name":     schema.StringAttribute{Computed: true, MarkdownDescription: "Display name."},
					"location": schema.StringAttribute{Computed: true, MarkdownDescription: "Physical location."},
					"zones": schema.ListNestedAttribute{
						Computed: true,
						NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
							"id":           schema.StringAttribute{Computed: true, MarkdownDescription: "Zone id, e.g. `eu-central-1a`."},
							"capabilities": capabilitiesSchema(),
							"simulated":    schema.BoolAttribute{Computed: true, MarkdownDescription: "No real backend behind this zone."},
						}},
					},
					"capabilities": capabilitiesSchema(),
					"simulated":    schema.BoolAttribute{Computed: true, MarkdownDescription: "No real backend behind this region."},
				}},
			},
		},
	}
}

func caps(c client.Capabilities) capabilitiesModel {
	return capabilitiesModel{Compute: types.BoolValue(c.Compute), Storage: types.BoolValue(c.Storage)}
}

func (d *regionsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	regions, err := d.data.client.ListRegions(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing regions", err.Error())
		return
	}
	out := regionsModel{Regions: []regionModel{}}
	for _, r := range regions {
		m := regionModel{
			ID: types.StringValue(r.ID), Name: types.StringValue(r.Name), Location: types.StringValue(r.Location),
			Capabilities: caps(r.Capabilities), Simulated: types.BoolValue(r.Simulated), Zones: []zoneModel{},
		}
		for _, z := range r.Zones {
			m.Zones = append(m.Zones, zoneModel{ID: types.StringValue(z.ID), Capabilities: caps(z.Capabilities), Simulated: types.BoolValue(z.Simulated)})
		}
		out.Regions = append(out.Regions, m)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
}

// ============================== flavors ==============================

// NewFlavorsDataSource is the cloudminator_flavors factory.
func NewFlavorsDataSource() datasource.DataSource { return &flavorsDataSource{} }

type flavorsDataSource struct{ data *providerData }

type flavorModel struct {
	ID             types.String `tfsdk:"id"`
	Label          types.String `tfsdk:"label"`
	VCPUs          types.Int64  `tfsdk:"vcpus"`
	MemMB          types.Int64  `tfsdk:"mem_mb"`
	DiskGB         types.Int64  `tfsdk:"disk_gb"`
	Size           types.String `tfsdk:"size"`
	ComputeUnits   types.Int64  `tfsdk:"compute_units"`
	PriceHourCents types.Int64  `tfsdk:"price_hour_cents"`
}

type flavorsModel struct {
	Flavors []flavorModel `tfsdk:"flavors"`
}

func (d *flavorsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_flavors"
}

func (d *flavorsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureDataSource(req, resp)
}

func (d *flavorsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "VM instance types with their hourly list price.",
		Attributes: map[string]schema.Attribute{
			"flavors": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":               schema.StringAttribute{Computed: true, MarkdownDescription: "Flavor id, e.g. `cm.small`."},
					"label":            schema.StringAttribute{Computed: true, MarkdownDescription: "Display label."},
					"vcpus":            schema.Int64Attribute{Computed: true, MarkdownDescription: "vCPUs."},
					"mem_mb":           schema.Int64Attribute{Computed: true, MarkdownDescription: "Memory in MB."},
					"disk_gb":          schema.Int64Attribute{Computed: true, MarkdownDescription: "Default boot disk in GB."},
					"size":             schema.StringAttribute{Computed: true, MarkdownDescription: "Value for `cloudminator_vm.size`, e.g. `2c-4g`."},
					"compute_units":    schema.Int64Attribute{Computed: true, MarkdownDescription: "Billing weight."},
					"price_hour_cents": schema.Int64Attribute{Computed: true, MarkdownDescription: "Hourly list price in euro cents."},
				}},
			},
		},
	}
}

func (d *flavorsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	flavors, err := d.data.client.ListFlavors(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing flavors", err.Error())
		return
	}
	out := flavorsModel{Flavors: []flavorModel{}}
	for _, f := range flavors {
		out.Flavors = append(out.Flavors, flavorModel{
			ID: types.StringValue(f.ID), Label: types.StringValue(f.Label), VCPUs: types.Int64Value(f.VCPUs),
			MemMB: types.Int64Value(f.MemMB), DiskGB: types.Int64Value(f.DiskGB), Size: types.StringValue(f.Size),
			ComputeUnits: types.Int64Value(f.ComputeUnits), PriceHourCents: types.Int64Value(f.PriceHourCents),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
}

// ============================== images ==============================

// NewImagesDataSource is the cloudminator_images factory.
func NewImagesDataSource() datasource.DataSource { return &imagesDataSource{} }

type imagesDataSource struct{ data *providerData }

type imageModel struct {
	ID    types.String `tfsdk:"id"`
	Label types.String `tfsdk:"label"`
}

type imagesModel struct {
	Region    types.String `tfsdk:"region"`
	Images    []imageModel `tfsdk:"images"`
	Simulated types.Bool   `tfsdk:"simulated"`
}

func (d *imagesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_images"
}

func (d *imagesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureDataSource(req, resp)
}

func (d *imagesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "OS images available for `cloudminator_vm.image`.",
		Attributes: map[string]schema.Attribute{
			"region": schema.StringAttribute{Optional: true, MarkdownDescription: "Only images bootable in this region."},
			"images": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":    schema.StringAttribute{Computed: true, MarkdownDescription: "Image id, e.g. `debian-13`."},
					"label": schema.StringAttribute{Computed: true, MarkdownDescription: "Display label."},
				}},
			},
			"simulated": schema.BoolAttribute{Computed: true, MarkdownDescription: "No real hypervisor behind the list."},
		},
	}
}

func (d *imagesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg imagesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, err := d.data.client.ListImages(ctx, cfg.Region.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error listing images", err.Error())
		return
	}
	cfg.Images = []imageModel{}
	for _, i := range list.Images {
		cfg.Images = append(cfg.Images, imageModel{ID: types.StringValue(i.ID), Label: types.StringValue(i.Label)})
	}
	cfg.Simulated = types.BoolValue(list.Simulated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// ============================== plans ==============================

// NewPlansDataSource is the cloudminator_plans factory.
func NewPlansDataSource() datasource.DataSource { return &plansDataSource{} }

type plansDataSource struct{ data *providerData }

type planModel struct {
	Code      types.String `tfsdk:"code"`
	Name      types.String `tfsdk:"name"`
	Default   types.Bool   `tfsdk:"default"`
	Resources types.Map    `tfsdk:"resource_limits"`
	Aggregate types.Map    `tfsdk:"aggregate_limits"`
	Quotas    types.Map    `tfsdk:"quotas"`
}

type plansModel struct {
	Plans []planModel `tfsdk:"plans"`
}

func (d *plansDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plans"
}

func (d *plansDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureDataSource(req, resp)
}

func (d *plansDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Plan tiers and their entitlements.",
		Attributes: map[string]schema.Attribute{
			"plans": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"code":             schema.StringAttribute{Computed: true, MarkdownDescription: "Plan code."},
					"name":             schema.StringAttribute{Computed: true, MarkdownDescription: "Display name."},
					"default":          schema.BoolAttribute{Computed: true, MarkdownDescription: "The plan new organizations get."},
					"resource_limits":  schema.MapAttribute{Computed: true, ElementType: types.Int64Type, MarkdownDescription: "Max count per resource kind (`vms`, `buckets`, …), organization-wide."},
					"aggregate_limits": schema.MapAttribute{Computed: true, ElementType: types.Int64Type, MarkdownDescription: "Cross-resource caps, e.g. `vms.compute-units`, `vms.disk-gb`."},
					"quotas":           schema.MapAttribute{Computed: true, ElementType: types.Int64Type, MarkdownDescription: "Per-plan quota overrides."},
				}},
			},
		},
	}
}

func int64Map(m map[string]int64) types.Map {
	v, _ := types.MapValueFrom(context.Background(), types.Int64Type, m)
	if m == nil {
		v, _ = types.MapValueFrom(context.Background(), types.Int64Type, map[string]int64{})
	}
	return v
}

func (d *plansDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	plans, err := d.data.client.ListPlans(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing plans", err.Error())
		return
	}
	out := plansModel{Plans: []planModel{}}
	for _, p := range plans {
		out.Plans = append(out.Plans, planModel{
			Code: types.StringValue(p.Code), Name: types.StringValue(p.Name), Default: types.BoolValue(p.Default),
			Resources: int64Map(p.Limits.Resources), Aggregate: int64Map(p.Limits.Aggregate), Quotas: int64Map(p.Limits.Quotas),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
}
