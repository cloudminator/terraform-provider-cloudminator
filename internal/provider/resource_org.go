package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/cloudminator/terraform-provider-cloudminator/internal/client"
)

// ============================== Project ==============================

// projectUpdatable: the display name (PATCH); the slug is fixed at creation.
var projectUpdatable = updatable{"name": true}

var (
	_ resource.Resource                = &projectResource{}
	_ resource.ResourceWithConfigure   = &projectResource{}
	_ resource.ResourceWithImportState = &projectResource{}
)

// NewProjectResource is the cloudminator_project factory.
func NewProjectResource() resource.Resource { return &projectResource{} }

type projectResource struct{ base }

type projectModel struct {
	ID        types.String `tfsdk:"id"`
	Org       types.String `tfsdk:"org"`
	Name      types.String `tfsdk:"name"`
	Slug      types.String `tfsdk:"slug"`
	CreatedAt types.String `tfsdk:"created_at"`
}

func (r *projectResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (r *projectResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.configure(req.ProviderData, &resp.Diagnostics)
}

func (r *projectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A project inside an organization. The slug is derived from the name at creation and never changes; " +
			"renaming changes only the display name. An organization's last project cannot be deleted.",
		Attributes: map[string]schema.Attribute{
			"id":  idAttr("Project slug."),
			"org": orgAttr(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name. Changing it renames the project in place.",
				Required:            true,
				PlanModifiers:       projectUpdatable.str("name"),
			},
			"slug":       computedString("Project slug, used as `project` on other resources."),
			"created_at": computedString("Creation time (RFC 3339)."),
		},
	}
}

func (r *projectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	org := r.resolveOrg(plan.Org, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client().CreateProject(ctx, org, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error creating project", err.Error())
		return
	}
	plan.Org = types.StringValue(org)
	projectToState(p, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *projectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client().GetProject(ctx, state.Org.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading project", err.Error())
		return
	}
	projectToState(p, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *projectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state projectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client().RenameProject(ctx, state.Org.ValueString(), state.ID.ValueString(), plan.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error renaming project", err.Error())
		return
	}
	plan.ID, plan.Slug, plan.CreatedAt = state.ID, state.Slug, state.CreatedAt
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *projectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state projectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client().DeleteProject(ctx, state.Org.ValueString(), state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting project", err.Error())
	}
}

func (r *projectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importOrgScoped(ctx, req, resp, "org/slug")
}

func projectToState(p client.Project, m *projectModel) {
	m.ID = types.StringValue(p.Slug)
	m.Slug = types.StringValue(p.Slug)
	m.Name = types.StringValue(p.Name)
	m.CreatedAt = types.StringValue(p.CreatedAt)
}

// ============================== SSH key ==============================

var (
	_ resource.Resource                = &sshKeyResource{}
	_ resource.ResourceWithConfigure   = &sshKeyResource{}
	_ resource.ResourceWithImportState = &sshKeyResource{}
)

// NewSSHKeyResource is the cloudminator_ssh_key factory.
func NewSSHKeyResource() resource.Resource { return &sshKeyResource{} }

type sshKeyResource struct{ base }

type sshKeyModel struct {
	ID          types.String `tfsdk:"id"`
	Org         types.String `tfsdk:"org"`
	Name        types.String `tfsdk:"name"`
	PublicKey   types.String `tfsdk:"public_key"`
	KeyType     types.String `tfsdk:"key_type"`
	Fingerprint types.String `tfsdk:"fingerprint"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

func (r *sshKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (r *sshKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.configure(req.ProviderData, &resp.Diagnostics)
}

func (r *sshKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An SSH public key on the organization's key ring. Keys are immutable; any change replaces the key.",
		Attributes: map[string]schema.Attribute{
			"id":  idAttr("Server-assigned key id."),
			"org": orgAttr(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"public_key": schema.StringAttribute{
				MarkdownDescription: "OpenSSH public key line, e.g. `ssh-ed25519 AAAA… user@host`.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"key_type":    computedString("Key algorithm, e.g. `ssh-ed25519`."),
			"fingerprint": computedString("SHA256 fingerprint (OpenSSH format)."),
			"created_at":  computedString("Creation time (RFC 3339)."),
		},
	}
}

func (r *sshKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan sshKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	org := r.resolveOrg(plan.Org, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	k, err := r.client().CreateSSHKey(ctx, org, plan.Name.ValueString(), plan.PublicKey.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error creating SSH key", err.Error())
		return
	}
	plan.Org = types.StringValue(org)
	sshKeyToState(k, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *sshKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state sshKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	k, err := r.client().GetSSHKey(ctx, state.Org.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading SSH key", err.Error())
		return
	}
	sshKeyToState(k, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is unreachable: every attribute forces replacement.
func (r *sshKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan sshKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *sshKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state sshKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client().DeleteSSHKey(ctx, state.Org.ValueString(), state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting SSH key", err.Error())
	}
}

func (r *sshKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importOrgScoped(ctx, req, resp, "org/id")
}

// sshKeyToState keeps the configured public_key: the platform normalizes it
// (whitespace, comment), which would otherwise show as a perpetual diff.
func sshKeyToState(k client.SSHKey, m *sshKeyModel) {
	m.ID = types.StringValue(k.ID)
	m.Name = types.StringValue(k.Name)
	if m.PublicKey.IsNull() || m.PublicKey.IsUnknown() || m.PublicKey.ValueString() == "" {
		m.PublicKey = types.StringValue(k.PublicKey)
	}
	m.KeyType = types.StringValue(k.KeyType)
	m.Fingerprint = types.StringValue(k.Fingerprint)
	m.CreatedAt = types.StringValue(k.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"))
}

// ============================== Bucket key ==============================

var (
	_ resource.Resource                = &bucketKeyResource{}
	_ resource.ResourceWithConfigure   = &bucketKeyResource{}
	_ resource.ResourceWithImportState = &bucketKeyResource{}
)

// NewBucketKeyResource is the cloudminator_bucket_key factory.
func NewBucketKeyResource() resource.Resource { return &bucketKeyResource{} }

type bucketKeyResource struct{ base }

type bucketKeyModel struct {
	ID              types.String `tfsdk:"id"`
	Org             types.String `tfsdk:"org"`
	Project         types.String `tfsdk:"project"`
	Bucket          types.String `tfsdk:"bucket"`
	Name            types.String `tfsdk:"name"`
	AccessKeyID     types.String `tfsdk:"access_key_id"`
	SecretAccessKey types.String `tfsdk:"secret_access_key"`
	Simulated       types.Bool   `tfsdk:"simulated"`
}

func (r *bucketKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_key"
}

func (r *bucketKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.configure(req.ProviderData, &resp.Diagnostics)
}

func (r *bucketKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An S3 access key scoped to one bucket. The secret is returned only when the key is created; " +
			"an imported key has no `secret_access_key`.\n\n~> The secret is stored in the Terraform state.",
		Attributes: map[string]schema.Attribute{
			"id":      idAttr("The access key id."),
			"org":     orgAttr(),
			"project": projectAttr(),
			"bucket": schema.StringAttribute{
				MarkdownDescription: "Bucket name (`cloudminator_bucket.id`).",
				Required:            true,
				PlanModifiers:       replace,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Key name. Defaults to `<bucket>-key`.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
			},
			"access_key_id": computedString("S3 access key id."),
			"secret_access_key": schema.StringAttribute{
				MarkdownDescription: "S3 secret access key (only known for keys created by Terraform).",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"simulated": simulatedAttr(),
		},
	}
}

func (r *bucketKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	org := r.resolveOrg(plan.Org, &resp.Diagnostics)
	project := r.resolveProject(plan.Project)
	if resp.Diagnostics.HasError() {
		return
	}
	k, err := client.CreateBucketKey(ctx, r.client(), org, project, plan.Bucket.ValueString(), plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error creating bucket key", err.Error())
		return
	}
	plan.Org, plan.Project = types.StringValue(org), types.StringValue(project)
	plan.ID = types.StringValue(k.AccessKeyID)
	plan.AccessKeyID = types.StringValue(k.AccessKeyID)
	plan.SecretAccessKey = types.StringValue(k.SecretAccessKey)
	plan.Name = types.StringValue(k.Name)
	plan.Simulated = types.BoolValue(k.Simulated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	keys, err := client.ListBucketKeys(ctx, r.client(), state.Org.ValueString(), state.Project.ValueString(), state.Bucket.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading bucket keys", err.Error())
		return
	}
	for _, k := range keys {
		if k.AccessKeyID == state.ID.ValueString() {
			state.AccessKeyID = types.StringValue(k.AccessKeyID)
			state.Name = types.StringValue(k.Name)
			state.Simulated = types.BoolValue(k.Simulated)
			if state.SecretAccessKey.IsUnknown() {
				state.SecretAccessKey = types.StringNull()
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

// Update is unreachable: every attribute forces replacement.
func (r *bucketKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state bucketKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := client.RevokeBucketKey(ctx, r.client(), state.Org.ValueString(), state.Project.ValueString(), state.Bucket.ValueString(), state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error revoking bucket key", err.Error())
	}
}

func (r *bucketKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := splitImportID(req.ID, 4, "org/project/bucket/access_key_id", &resp.Diagnostics)
	if parts == nil {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathOrg, parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathProject, parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("bucket"), parts[2])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathID, parts[3])...)
}

// importOrgScoped implements `org/<id>` import.
func importOrgScoped(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse, format string) {
	parts := splitImportID(req.ID, 2, format, &resp.Diagnostics)
	if parts == nil {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathOrg, parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathID, parts[1])...)
}
