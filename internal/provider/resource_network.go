package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/cloudminator/terraform-provider-cloudminator/internal/client"
)

// ============================== VPC ==============================

// vpcUpdatable: name+tags via :update.
// TODO(ADR 0062): flip further fields here as the platform's :update grows.
var vpcUpdatable = updatable{"name": true, "tags": true}

var (
	_ resource.Resource                = &vpcResource{}
	_ resource.ResourceWithConfigure   = &vpcResource{}
	_ resource.ResourceWithImportState = &vpcResource{}
)

// NewVPCResource is the cloudminator_vpc factory.
func NewVPCResource() resource.Resource { return &vpcResource{} }

type vpcResource struct{ base }

type vpcModel struct {
	ID       types.String   `tfsdk:"id"`
	Org      types.String   `tfsdk:"org"`
	Project  types.String   `tfsdk:"project"`
	Name     types.String   `tfsdk:"name"`
	Tags     types.Map      `tfsdk:"tags"`
	CIDR     types.String   `tfsdk:"cidr"`
	Region   types.String   `tfsdk:"region"`
	Phase    types.String   `tfsdk:"phase"`
	Timeouts timeouts.Value `tfsdk:"timeouts"`
}

func (r *vpcResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vpc"
}

func (r *vpcResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.configure(req.ProviderData, &resp.Diagnostics)
}

func (r *vpcResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	u := vpcUpdatable
	resp.Schema = schema.Schema{
		MarkdownDescription: "A virtual private cloud: an isolated address space that subnets are carved from. " +
			"`name` and `tags` change in place.",
		Attributes: map[string]schema.Attribute{
			"id":      idAttr("Server-assigned VPC id (`vpc-…`)."),
			"org":     orgAttr(),
			"project": projectAttr(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name. Not unique.",
				Required:            true,
				PlanModifiers:       u.str("name"),
			},
			"tags": tagsAttr(u),
			"cidr": optionalComputedString(u, "cidr", "IPv4 range. Omit to have the platform allocate one."),
			"region": schema.StringAttribute{
				MarkdownDescription: "Region id (see `cloudminator_regions`).",
				Required:            true,
				PlanModifiers:       u.str("region"),
			},
			"phase": phaseAttr(),
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *vpcResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vpcModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := plan.Timeouts.Create(ctx, defaultCreateTimeout)
	resp.Diagnostics.Append(d...)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	org := r.resolveOrg(plan.Org, &resp.Diagnostics)
	project := r.resolveProject(plan.Project)
	tags := tagsToAPI(ctx, plan.Tags, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	spec := client.VPCSpec{CIDR: plan.CIDR.ValueString(), Region: plan.Region.ValueString()}
	env, err := client.CreateResource[client.VPCSpec](ctx, r.client(), org, project, client.KindVPCs,
		client.CreateRequest[client.VPCSpec]{Name: plan.Name.ValueString(), Tags: tags, Spec: spec})
	if err != nil {
		resp.Diagnostics.AddError("Error creating VPC", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathID, env.Metadata.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathOrg, org)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathProject, project)...)
	env, err = client.WaitForPhase[client.VPCSpec](ctx, r.client(), org, project, client.KindVPCs, env.Metadata.ID)
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for VPC to become ready", err.Error())
		return
	}
	vpcToState(env, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vpcResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vpcModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	env, err := client.GetResource[client.VPCSpec](ctx, r.client(), state.Org.ValueString(), state.Project.ValueString(), client.KindVPCs, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading VPC", err.Error())
		return
	}
	vpcToState(env, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *vpcResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state vpcModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"name": plan.Name.ValueString(), "tags": tagsToUpdate(ctx, plan.Tags, &resp.Diagnostics)}
	if resp.Diagnostics.HasError() {
		return
	}
	env, err := client.UpdateResource[client.VPCSpec](ctx, r.client(), state.Org.ValueString(), state.Project.ValueString(), client.KindVPCs, state.ID.ValueString(), body)
	if err != nil {
		resp.Diagnostics.AddError("Error updating VPC", err.Error())
		return
	}
	vpcToState(env, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vpcResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vpcModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	deleteAndWait[client.VPCSpec](ctx, r.client(), state.Timeouts, state.Org, state.Project, state.ID, client.KindVPCs, "VPC", resp)
}

func (r *vpcResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importScoped(ctx, req, resp)
}

func vpcToState(env client.Envelope[client.VPCSpec], m *vpcModel) {
	m.ID = types.StringValue(env.Metadata.ID)
	m.Org = types.StringValue(env.Metadata.Org)
	m.Project = types.StringValue(env.Metadata.Project)
	m.Name = types.StringValue(env.Metadata.Name)
	m.Tags = tagsFromAPI(env.Metadata.Tags, m.Tags)
	m.CIDR = types.StringValue(env.Spec.CIDR)
	m.Region = types.StringValue(env.Spec.Region)
	m.Phase = types.StringValue(env.Status.Phase)
}

// ============================== Subnet ==============================

// subnetUpdatable: name, tags, snat, dhcp via :update.
// TODO(ADR 0062): flip further fields here as the platform's :update grows.
var subnetUpdatable = updatable{"name": true, "tags": true, "snat": true, "dhcp": true}

var (
	_ resource.Resource                = &subnetResource{}
	_ resource.ResourceWithConfigure   = &subnetResource{}
	_ resource.ResourceWithImportState = &subnetResource{}
)

// NewSubnetResource is the cloudminator_subnet factory.
func NewSubnetResource() resource.Resource { return &subnetResource{} }

type subnetResource struct{ base }

type subnetModel struct {
	ID       types.String   `tfsdk:"id"`
	Org      types.String   `tfsdk:"org"`
	Project  types.String   `tfsdk:"project"`
	Name     types.String   `tfsdk:"name"`
	Tags     types.Map      `tfsdk:"tags"`
	VPCID    types.String   `tfsdk:"vpc_id"`
	CIDR     types.String   `tfsdk:"cidr"`
	Gateway  types.String   `tfsdk:"gateway"`
	SNAT     types.Bool     `tfsdk:"snat"`
	DHCP     types.Bool     `tfsdk:"dhcp"`
	Phase    types.String   `tfsdk:"phase"`
	Timeouts timeouts.Value `tfsdk:"timeouts"`
}

func (r *subnetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_subnet"
}

func (r *subnetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.configure(req.ProviderData, &resp.Diagnostics)
}

func (r *subnetResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	u := subnetUpdatable
	resp.Schema = schema.Schema{
		MarkdownDescription: "A subnet inside a VPC. `name`, `tags`, `snat` and `dhcp` change in place.",
		Attributes: map[string]schema.Attribute{
			"id":      idAttr("Server-assigned subnet id (`subnet-…`)."),
			"org":     orgAttr(),
			"project": projectAttr(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name. Not unique.",
				Required:            true,
				PlanModifiers:       u.str("name"),
			},
			"tags": tagsAttr(u),
			"vpc_id": schema.StringAttribute{
				MarkdownDescription: "Parent VPC id.",
				Required:            true,
				PlanModifiers:       u.str("vpc_id"),
			},
			"cidr":    optionalComputedString(u, "cidr", "IPv4 range within the VPC. Omit to allocate a /24."),
			"gateway": optionalComputedString(u, "gateway", "Gateway address. Defaults to the first host of the range."),
			"snat": schema.BoolAttribute{
				MarkdownDescription: "Source-NAT outbound traffic. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				PlanModifiers:       u.boolean("snat"),
			},
			"dhcp": schema.BoolAttribute{
				MarkdownDescription: "Serve DHCP on the subnet. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				PlanModifiers:       u.boolean("dhcp"),
			},
			"phase": phaseAttr(),
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *subnetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan subnetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := plan.Timeouts.Create(ctx, defaultCreateTimeout)
	resp.Diagnostics.Append(d...)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	org := r.resolveOrg(plan.Org, &resp.Diagnostics)
	project := r.resolveProject(plan.Project)
	tags := tagsToAPI(ctx, plan.Tags, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	spec := client.SubnetSpec{
		VPCID: plan.VPCID.ValueString(), CIDR: plan.CIDR.ValueString(),
		Gateway: plan.Gateway.ValueString(), SNAT: plan.SNAT.ValueBool(), DHCP: plan.DHCP.ValueBool(),
	}
	env, err := client.CreateResource[client.SubnetSpec](ctx, r.client(), org, project, client.KindSubnets,
		client.CreateRequest[client.SubnetSpec]{Name: plan.Name.ValueString(), Tags: tags, Spec: spec})
	if err != nil {
		resp.Diagnostics.AddError("Error creating subnet", err.Error())
		return
	}
	id := env.Metadata.ID
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathID, id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathOrg, org)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathProject, project)...)
	env, err = client.WaitForPhase[client.SubnetSpec](ctx, r.client(), org, project, client.KindSubnets, id)
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for subnet to become ready", err.Error())
		return
	}
	// Workaround (ADR 0062 follow-up): platform SubnetService.Create ignores the
	// requested snat/dhcp and always sets both true, so apply a requested false
	// with :update once the subnet is Running, before saving state.
	if env.Spec.SNAT != spec.SNAT || env.Spec.DHCP != spec.DHCP {
		env, err = client.UpdateResource[client.SubnetSpec](ctx, r.client(), org, project, client.KindSubnets, id,
			map[string]any{"snat": spec.SNAT, "dhcp": spec.DHCP})
		if err != nil {
			resp.Diagnostics.AddError("Error setting subnet snat/dhcp", err.Error())
			return
		}
	}
	subnetToState(env, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *subnetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state subnetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	env, err := client.GetResource[client.SubnetSpec](ctx, r.client(), state.Org.ValueString(), state.Project.ValueString(), client.KindSubnets, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading subnet", err.Error())
		return
	}
	subnetToState(env, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *subnetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state subnetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{
		"name": plan.Name.ValueString(),
		"tags": tagsToUpdate(ctx, plan.Tags, &resp.Diagnostics),
		"snat": plan.SNAT.ValueBool(),
		"dhcp": plan.DHCP.ValueBool(),
	}
	if resp.Diagnostics.HasError() {
		return
	}
	env, err := client.UpdateResource[client.SubnetSpec](ctx, r.client(), state.Org.ValueString(), state.Project.ValueString(), client.KindSubnets, state.ID.ValueString(), body)
	if err != nil {
		resp.Diagnostics.AddError("Error updating subnet", err.Error())
		return
	}
	subnetToState(env, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *subnetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state subnetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	deleteAndWait[client.SubnetSpec](ctx, r.client(), state.Timeouts, state.Org, state.Project, state.ID, client.KindSubnets, "subnet", resp)
}

func (r *subnetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importScoped(ctx, req, resp)
}

func subnetToState(env client.Envelope[client.SubnetSpec], m *subnetModel) {
	s := env.Spec
	m.ID = types.StringValue(env.Metadata.ID)
	m.Org = types.StringValue(env.Metadata.Org)
	m.Project = types.StringValue(env.Metadata.Project)
	m.Name = types.StringValue(env.Metadata.Name)
	m.Tags = tagsFromAPI(env.Metadata.Tags, m.Tags)
	m.VPCID = types.StringValue(s.VPCID)
	m.CIDR = types.StringValue(s.CIDR)
	m.Gateway = types.StringValue(s.Gateway)
	m.SNAT = types.BoolValue(s.SNAT)
	m.DHCP = types.BoolValue(s.DHCP)
	m.Phase = types.StringValue(env.Status.Phase)
}

// ============================== Firewall rule ==============================

// firewallUpdatable: everything but subnet_id via :update.
// TODO(ADR 0062): flip further fields here as the platform's :update grows.
var firewallUpdatable = updatable{"name": true, "tags": true, "protocol": true, "port": true, "source": true, "comment": true}

var (
	_ resource.Resource                = &firewallResource{}
	_ resource.ResourceWithConfigure   = &firewallResource{}
	_ resource.ResourceWithImportState = &firewallResource{}
)

// NewFirewallResource is the cloudminator_firewall factory.
func NewFirewallResource() resource.Resource { return &firewallResource{} }

type firewallResource struct{ base }

type firewallModel struct {
	ID       types.String `tfsdk:"id"`
	Org      types.String `tfsdk:"org"`
	Project  types.String `tfsdk:"project"`
	Name     types.String `tfsdk:"name"`
	Tags     types.Map    `tfsdk:"tags"`
	SubnetID types.String `tfsdk:"subnet_id"`
	Protocol types.String `tfsdk:"protocol"`
	Port     types.String `tfsdk:"port"`
	Source   types.String `tfsdk:"source"`
	Comment  types.String `tfsdk:"comment"`
	Phase    types.String `tfsdk:"phase"`
}

func (r *firewallResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall"
}

func (r *firewallResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.configure(req.ProviderData, &resp.Diagnostics)
}

func (r *firewallResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	u := firewallUpdatable
	resp.Schema = schema.Schema{
		MarkdownDescription: "An inbound allow rule on a subnet, applied on top of the platform's baseline isolation. " +
			"Changing `subnet_id` replaces the rule; everything else changes in place.",
		Attributes: map[string]schema.Attribute{
			"id":      idAttr("Server-assigned rule id (`fwrule-…`)."),
			"org":     orgAttr(),
			"project": projectAttr(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name.",
				Required:            true,
				PlanModifiers:       u.str("name"),
			},
			"tags": tagsAttr(u),
			"subnet_id": schema.StringAttribute{
				MarkdownDescription: "Subnet the rule applies to.",
				Required:            true,
				PlanModifiers:       u.str("subnet_id"),
			},
			"protocol": schema.StringAttribute{
				MarkdownDescription: "`tcp`, `udp` or `icmp`. Omit for any protocol.",
				Optional:            true,
				PlanModifiers:       u.str("protocol"),
			},
			"port": schema.StringAttribute{
				MarkdownDescription: "Port (`443`) or range (`8000:8100`). Omit for any port.",
				Optional:            true,
				PlanModifiers:       u.str("port"),
			},
			"source": schema.StringAttribute{
				MarkdownDescription: "Source CIDR. Omit for any source.",
				Optional:            true,
				PlanModifiers:       u.str("source"),
			},
			"comment": schema.StringAttribute{
				MarkdownDescription: "Free-form comment.",
				Optional:            true,
				PlanModifiers:       u.str("comment"),
			},
			"phase": phaseAttr(),
		},
	}
}

func (r *firewallResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan firewallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	org := r.resolveOrg(plan.Org, &resp.Diagnostics)
	project := r.resolveProject(plan.Project)
	tags := tagsToAPI(ctx, plan.Tags, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	spec := client.FirewallSpec{
		SubnetID: plan.SubnetID.ValueString(), Protocol: plan.Protocol.ValueString(),
		Port: plan.Port.ValueString(), Source: plan.Source.ValueString(), Comment: plan.Comment.ValueString(),
	}
	env, err := client.CreateResource[client.FirewallSpec](ctx, r.client(), org, project, client.KindFirewalls,
		client.CreateRequest[client.FirewallSpec]{Name: plan.Name.ValueString(), Tags: tags, Spec: spec})
	if err != nil {
		resp.Diagnostics.AddError("Error creating firewall rule", err.Error())
		return
	}
	firewallToState(env, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *firewallResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state firewallModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	env, err := client.GetResource[client.FirewallSpec](ctx, r.client(), state.Org.ValueString(), state.Project.ValueString(), client.KindFirewalls, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading firewall rule", err.Error())
		return
	}
	firewallToState(env, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *firewallResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state firewallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Firewall :update nests spec changes under "spec"; "" clears a field.
	body := map[string]any{
		"name": plan.Name.ValueString(),
		"tags": tagsToUpdate(ctx, plan.Tags, &resp.Diagnostics),
		"spec": map[string]any{
			"protocol": plan.Protocol.ValueString(),
			"port":     plan.Port.ValueString(),
			"source":   plan.Source.ValueString(),
			"comment":  plan.Comment.ValueString(),
		},
	}
	if resp.Diagnostics.HasError() {
		return
	}
	env, err := client.UpdateResource[client.FirewallSpec](ctx, r.client(), state.Org.ValueString(), state.Project.ValueString(), client.KindFirewalls, state.ID.ValueString(), body)
	if err != nil {
		resp.Diagnostics.AddError("Error updating firewall rule", err.Error())
		return
	}
	firewallToState(env, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *firewallResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state firewallModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := client.DeleteResource(ctx, r.client(), state.Org.ValueString(), state.Project.ValueString(), client.KindFirewalls, state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting firewall rule", err.Error())
	}
}

func (r *firewallResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importScoped(ctx, req, resp)
}

func firewallToState(env client.Envelope[client.FirewallSpec], m *firewallModel) {
	s := env.Spec
	m.ID = types.StringValue(env.Metadata.ID)
	m.Org = types.StringValue(env.Metadata.Org)
	m.Project = types.StringValue(env.Metadata.Project)
	m.Name = types.StringValue(env.Metadata.Name)
	m.Tags = tagsFromAPI(env.Metadata.Tags, m.Tags)
	m.SubnetID = types.StringValue(s.SubnetID)
	m.Protocol = optString(s.Protocol)
	m.Port = optString(s.Port)
	m.Source = optString(s.Source)
	m.Comment = optString(s.Comment)
	m.Phase = types.StringValue(env.Status.Phase)
}

// ============================== shared ==============================

// deleteAndWait issues an async DELETE and waits for the record to disappear.
func deleteAndWait[S any](ctx context.Context, c *client.Client, t timeouts.Value, org, project, id types.String, kind, label string, resp *resource.DeleteResponse) {
	timeout, d := t.Delete(ctx, defaultDeleteTimeout)
	resp.Diagnostics.Append(d...)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	o, p, i := org.ValueString(), project.ValueString(), id.ValueString()
	if err := client.DeleteResource(ctx, c, o, p, kind, i); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting "+label, err.Error())
		return
	}
	if err := client.WaitForDeleted[S](ctx, c, o, p, kind, i); err != nil {
		resp.Diagnostics.AddError("Error waiting for "+label+" deletion", err.Error())
	}
}
