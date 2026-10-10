package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/cloudminator/terraform-provider-cloudminator/internal/client"
)

// vmUpdatable: name+tags via :update, size+disk_gb via :resize, running via
// :start/:stop. Everything else forces replacement.
// TODO(ADR 0061): flip further fields here as the platform's :update grows.
var vmUpdatable = updatable{"name": true, "tags": true, "size": true, "disk_gb": true, "running": true}

var (
	_ resource.Resource                = &vmResource{}
	_ resource.ResourceWithConfigure   = &vmResource{}
	_ resource.ResourceWithImportState = &vmResource{}
)

// NewVMResource is the cloudminator_vm factory.
func NewVMResource() resource.Resource { return &vmResource{} }

type vmResource struct{ base }

type vmModel struct {
	ID        types.String   `tfsdk:"id"`
	Org       types.String   `tfsdk:"org"`
	Project   types.String   `tfsdk:"project"`
	Name      types.String   `tfsdk:"name"`
	Tags      types.Map      `tfsdk:"tags"`
	Image     types.String   `tfsdk:"image"`
	Size      types.String   `tfsdk:"size"`
	DiskGB    types.Int64    `tfsdk:"disk_gb"`
	Region    types.String   `tfsdk:"region"`
	Zone      types.String   `tfsdk:"zone"`
	SSHKeys   types.List     `tfsdk:"ssh_keys"`
	User      types.String   `tfsdk:"user"`
	UserData  types.String   `tfsdk:"user_data"`
	SubnetID  types.String   `tfsdk:"subnet_id"`
	IP        types.String   `tfsdk:"ip"`
	Running   types.Bool     `tfsdk:"running"`
	Address   types.String   `tfsdk:"address"`
	Gateway   types.String   `tfsdk:"gateway"`
	Phase     types.String   `tfsdk:"phase"`
	Simulated types.Bool     `tfsdk:"simulated"`
	Timeouts  timeouts.Value `tfsdk:"timeouts"`
}

func (r *vmResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vm"
}

func (r *vmResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.configure(req.ProviderData, &resp.Diagnostics)
}

func (r *vmResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	u := vmUpdatable
	resp.Schema = schema.Schema{
		MarkdownDescription: "A virtual machine. `name`, `tags`, `size`, a larger `disk_gb` and `running` change in place; " +
			"everything else (including shrinking the disk) replaces the VM.",
		Attributes: map[string]schema.Attribute{
			"id":      idAttr("Server-assigned VM id (`vm-…`)."),
			"org":     orgAttr(),
			"project": projectAttr(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name. Not unique and not part of the VM's identity.",
				Required:            true,
				PlanModifiers:       u.str("name"),
			},
			"tags": tagsAttr(u),
			"image": schema.StringAttribute{
				MarkdownDescription: "OS image id (see the `cloudminator_images` data source).",
				Required:            true,
				PlanModifiers:       u.str("image"),
			},
			"size": schema.StringAttribute{
				MarkdownDescription: "Instance size, e.g. `2c-4g` (see the `size` of `cloudminator_flavors`). " +
					"On a running VM a resize takes effect at its next restart.",
				Required:      true,
				PlanModifiers: u.str("size"),
			},
			"disk_gb": schema.Int64Attribute{
				MarkdownDescription: "Boot disk size in GB. Growing it is in place; shrinking it replaces the VM.",
				Optional:            true,
				Computed:            true,
				PlanModifiers: u.int64("disk_gb",
					int64planmodifier.UseStateForUnknown(),
					int64planmodifier.RequiresReplaceIf(diskShrinks, "Shrinking a disk replaces the VM.", "Shrinking a disk replaces the VM."),
				),
			},
			"region":  optionalComputedString(u, "region", "Region id. Defaults to the platform's first region, or the subnet's region when `subnet_id` is set."),
			"zone":    optionalComputedString(u, "zone", "Zone id within the region. Empty lets the platform choose."),
			"ssh_keys": schema.ListAttribute{
				MarkdownDescription: "SSH public keys (authorized_keys lines) installed by cloud-init.",
				ElementType:         types.StringType,
				Optional:            true,
				PlanModifiers:       u.list("ssh_keys"),
			},
			"user": schema.StringAttribute{
				MarkdownDescription: "Cloud-init login user.",
				Optional:            true,
				PlanModifiers:       u.str("user"),
			},
			"user_data": schema.StringAttribute{
				MarkdownDescription: "Cloud-init user-data. Treated as sensitive because it often carries join tokens.",
				Optional:            true,
				Sensitive:           true,
				PlanModifiers:       u.str("user_data"),
			},
			"subnet_id": schema.StringAttribute{
				MarkdownDescription: "Place the VM on this tenant subnet (`cloudminator_subnet.id`).",
				Optional:            true,
				PlanModifiers:       u.str("subnet_id"),
			},
			"ip": optionalComputedString(u, "ip", "Fixed IPv4 address within `subnet_id`, outside the DHCP pool (e.g. `.2`–`.9`). Omit for DHCP."),
			"running": schema.BoolAttribute{
				MarkdownDescription: "Desired power state. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				PlanModifiers:       u.boolean("running"),
			},
			"address":   computedString("Address the platform reports for the VM."),
			"gateway":   computedString("Subnet gateway resolved for a VM on a subnet."),
			"phase":     phaseAttr(),
			"simulated": simulatedAttr(),
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

// diskShrinks replaces the VM when disk_gb decreases (the backend can only grow a disk).
func diskShrinks(_ context.Context, req planmodifier.Int64Request, resp *int64planmodifier.RequiresReplaceIfFuncResponse) {
	if req.StateValue.IsNull() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	resp.RequiresReplace = req.PlanValue.ValueInt64() < req.StateValue.ValueInt64()
}

func (r *vmResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vmModel
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
	spec := client.VMSpec{
		Image:    plan.Image.ValueString(),
		Size:     plan.Size.ValueString(),
		Region:   plan.Region.ValueString(),
		Zone:     plan.Zone.ValueString(),
		DiskGB:   plan.DiskGB.ValueInt64(),
		User:     plan.User.ValueString(),
		UserData: plan.UserData.ValueString(),
		SubnetID: plan.SubnetID.ValueString(),
		IP:       plan.IP.ValueString(),
	}
	if !plan.SSHKeys.IsNull() && !plan.SSHKeys.IsUnknown() {
		resp.Diagnostics.Append(plan.SSHKeys.ElementsAs(ctx, &spec.SSHKeys, false)...)
	}
	tags := tagsToAPI(ctx, plan.Tags, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	env, err := client.CreateResource[client.VMSpec](ctx, r.client(), org, project, client.KindVMs,
		client.CreateRequest[client.VMSpec]{Name: plan.Name.ValueString(), Tags: tags, Spec: spec})
	if err != nil {
		resp.Diagnostics.AddError("Error creating VM", err.Error())
		return
	}
	plan.ID = types.StringValue(env.Metadata.ID)
	plan.Org = types.StringValue(org)
	plan.Project = types.StringValue(project)
	// Persist the id immediately so a failed wait does not orphan the VM.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathID, plan.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathOrg, plan.Org)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathProject, plan.Project)...)

	env, err = client.WaitForPhase[client.VMSpec](ctx, r.client(), org, project, client.KindVMs, env.Metadata.ID)
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for VM to become ready", err.Error())
		return
	}
	if !plan.Running.ValueBool() && env.Status.Phase != client.PhaseStopped {
		if err := client.Act(ctx, r.client(), org, project, client.KindVMs, env.Metadata.ID, "stop", nil, &env); err != nil {
			resp.Diagnostics.AddError("Error stopping VM", err.Error())
			return
		}
	}
	r.toState(ctx, env, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vmResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vmModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	env, err := client.GetResource[client.VMSpec](ctx, r.client(), state.Org.ValueString(), state.Project.ValueString(), client.KindVMs, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading VM", err.Error())
		return
	}
	r.toState(ctx, env, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *vmResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state vmModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := plan.Timeouts.Update(ctx, defaultUpdateTimeout)
	resp.Diagnostics.Append(d...)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := r.client()
	org, project, id := state.Org.ValueString(), state.Project.ValueString(), state.ID.ValueString()

	if !plan.Name.Equal(state.Name) || !plan.Tags.Equal(state.Tags) {
		body := map[string]any{"name": plan.Name.ValueString(), "tags": tagsToUpdate(ctx, plan.Tags, &resp.Diagnostics)}
		if resp.Diagnostics.HasError() {
			return
		}
		if _, err := client.UpdateResource[client.VMSpec](ctx, c, org, project, client.KindVMs, id, body); err != nil {
			resp.Diagnostics.AddError("Error updating VM", err.Error())
			return
		}
	}
	if !plan.Size.Equal(state.Size) || (!plan.DiskGB.IsUnknown() && !plan.DiskGB.Equal(state.DiskGB)) {
		body := map[string]any{}
		if !plan.Size.Equal(state.Size) {
			body["size"] = plan.Size.ValueString()
		}
		if !plan.DiskGB.IsUnknown() && !plan.DiskGB.Equal(state.DiskGB) {
			body["diskGB"] = plan.DiskGB.ValueInt64()
		}
		var env client.Envelope[client.VMSpec]
		if err := client.Act(ctx, c, org, project, client.KindVMs, id, "resize", body, &env); err != nil {
			resp.Diagnostics.AddError("Error resizing VM", err.Error())
			return
		}
		if _, pending := env.Status.Observed["pendingRestart"]; pending && plan.Running.ValueBool() {
			resp.Diagnostics.AddWarning("VM resize pending restart",
				"The new size is recorded but takes effect the next time the VM restarts.")
		}
	}
	if !plan.Running.Equal(state.Running) {
		action := "stop"
		if plan.Running.ValueBool() {
			action = "start"
		}
		if err := client.Act(ctx, c, org, project, client.KindVMs, id, action, nil, nil); err != nil {
			resp.Diagnostics.AddError("Error changing VM power state", err.Error())
			return
		}
	}

	env, err := client.GetResource[client.VMSpec](ctx, c, org, project, client.KindVMs, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading VM after update", err.Error())
		return
	}
	plan.ID, plan.Org, plan.Project = state.ID, state.Org, state.Project
	r.toState(ctx, env, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vmResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vmModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := state.Timeouts.Delete(ctx, defaultDeleteTimeout)
	resp.Diagnostics.Append(d...)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	org, project, id := state.Org.ValueString(), state.Project.ValueString(), state.ID.ValueString()
	if err := client.DeleteResource(ctx, r.client(), org, project, client.KindVMs, id); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting VM", err.Error())
		return
	}
	if err := client.WaitForDeleted[client.VMSpec](ctx, r.client(), org, project, client.KindVMs, id); err != nil {
		resp.Diagnostics.AddError("Error waiting for VM deletion", err.Error())
	}
}

func (r *vmResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importScoped(ctx, req, resp)
}

// toState copies the server envelope into the model. Write-only / user-shaped
// inputs (user_data, ssh_keys, ip) keep the configured value when the server's
// form is equivalent.
func (r *vmResource) toState(_ context.Context, env client.Envelope[client.VMSpec], m *vmModel) {
	s := env.Spec
	m.ID = types.StringValue(env.Metadata.ID)
	m.Org = types.StringValue(env.Metadata.Org)
	m.Project = types.StringValue(env.Metadata.Project)
	m.Name = types.StringValue(env.Metadata.Name)
	m.Tags = tagsFromAPI(env.Metadata.Tags, m.Tags)
	m.Image = types.StringValue(s.Image)
	m.Size = types.StringValue(s.Size)
	m.DiskGB = types.Int64Value(s.DiskGB)
	m.Region = types.StringValue(s.Region)
	m.Zone = types.StringValue(s.Zone)
	m.User = optString(s.User)
	m.SubnetID = optString(s.SubnetID)
	m.IP = ipFromAPI(s.IP, m.IP)
	if m.SSHKeys.IsNull() || m.SSHKeys.IsUnknown() {
		if len(s.SSHKeys) > 0 {
			m.SSHKeys, _ = types.ListValueFrom(context.Background(), types.StringType, s.SSHKeys)
		} else {
			m.SSHKeys = types.ListNull(types.StringType)
		}
	}
	if m.UserData.IsUnknown() || (m.UserData.IsNull() && s.UserData != "") {
		m.UserData = optString(s.UserData)
	}
	m.Running = types.BoolValue(env.Status.Phase != client.PhaseStopped)
	m.Address = types.StringValue(env.Status.ObservedString("address"))
	m.Gateway = types.StringValue(s.Gateway)
	m.Phase = types.StringValue(env.Status.Phase)
	m.Simulated = types.BoolValue(env.Status.ObservedBool("simulated"))
}

// ipFromAPI keeps a configured bare address when the server stored it in CIDR
// form ("10.0.0.5" vs "10.0.0.5/24").
func ipFromAPI(server string, prior types.String) types.String {
	if server == "" {
		return types.StringValue("")
	}
	if !prior.IsNull() && !prior.IsUnknown() {
		p := prior.ValueString()
		if p == server || (!strings.Contains(p, "/") && strings.HasPrefix(server, p+"/")) {
			return prior
		}
	}
	return types.StringValue(server)
}
