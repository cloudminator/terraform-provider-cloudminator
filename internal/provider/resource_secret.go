package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/cloudminator/terraform-provider-cloudminator/internal/client"
)

// secretUpdatable: name, tags and data via :update (data writes a new version).
// TODO(ADR 0061): flip further fields here as the platform's :update grows.
var secretUpdatable = updatable{"name": true, "tags": true, "data": true}

var (
	_ resource.Resource                = &secretResource{}
	_ resource.ResourceWithConfigure   = &secretResource{}
	_ resource.ResourceWithImportState = &secretResource{}
)

// NewSecretResource is the cloudminator_secret factory.
func NewSecretResource() resource.Resource { return &secretResource{} }

type secretResource struct{ base }

type secretModel struct {
	ID        types.String `tfsdk:"id"`
	Org       types.String `tfsdk:"org"`
	Project   types.String `tfsdk:"project"`
	Name      types.String `tfsdk:"name"`
	Tags      types.Map    `tfsdk:"tags"`
	Region    types.String `tfsdk:"region"`
	Data      types.Map    `tfsdk:"data"`
	Path      types.String `tfsdk:"path"`
	Version   types.Int64  `tfsdk:"version"`
	Phase     types.String `tfsdk:"phase"`
	Simulated types.Bool   `tfsdk:"simulated"`
}

func (r *secretResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secret"
}

func (r *secretResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.configure(req.ProviderData, &resp.Diagnostics)
}

func (r *secretResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	u := secretUpdatable
	resp.Schema = schema.Schema{
		MarkdownDescription: "A secret stored in the platform's regional vault. `data` is write-only: the platform never returns it, " +
			"so Terraform only detects changes made through Terraform. Changing `data` writes a new version in place.\n\n" +
			"~> `data` is stored in the Terraform state. Protect your state accordingly.",
		Attributes: map[string]schema.Attribute{
			"id":      idAttr("Server-assigned secret id (`secret-…`)."),
			"org":     orgAttr(),
			"project": projectAttr(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name. Not unique.",
				Required:            true,
				PlanModifiers:       u.str("name"),
			},
			"tags":   tagsAttr(u),
			"region": optionalComputedString(u, "region", "Region of the vault holding the secret. Fixed for life."),
			"data": schema.MapAttribute{
				MarkdownDescription: "Secret key/value material.",
				ElementType:         types.StringType,
				Optional:            true,
				Sensitive:           true,
				PlanModifiers:       u.mapping("data"),
			},
			"path": computedString("Vault path assigned by the platform."),
			"version": schema.Int64Attribute{
				MarkdownDescription: "Current version of the material.",
				Computed:            true,
			},
			"phase":     phaseAttr(),
			"simulated": simulatedAttr(),
		},
	}
}

func (r *secretResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan secretModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	org := r.resolveOrg(plan.Org, &resp.Diagnostics)
	project := r.resolveProject(plan.Project)
	tags := tagsToAPI(ctx, plan.Tags, &resp.Diagnostics)
	data := secretData(ctx, plan.Data, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	env, err := client.CreateResource[client.SecretSpec](ctx, r.client(), org, project, client.KindSecrets,
		client.SecretCreateRequest{
			Name: plan.Name.ValueString(), Tags: tags, Data: data,
			Spec: client.SecretSpec{Region: plan.Region.ValueString()},
		})
	if err != nil {
		resp.Diagnostics.AddError("Error creating secret", err.Error())
		return
	}
	secretToState(env, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *secretResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state secretModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	env, err := client.GetResource[client.SecretSpec](ctx, r.client(), state.Org.ValueString(), state.Project.ValueString(), client.KindSecrets, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading secret", err.Error())
		return
	}
	secretToState(env, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *secretResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state secretModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"name": plan.Name.ValueString(), "tags": tagsToUpdate(ctx, plan.Tags, &resp.Diagnostics)}
	if !plan.Data.Equal(state.Data) {
		body["data"] = secretData(ctx, plan.Data, &resp.Diagnostics)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	env, err := client.UpdateResource[client.SecretSpec](ctx, r.client(), state.Org.ValueString(), state.Project.ValueString(), client.KindSecrets, state.ID.ValueString(), body)
	if err != nil {
		resp.Diagnostics.AddError("Error updating secret", err.Error())
		return
	}
	secretToState(env, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *secretResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state secretModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	org, project, id := state.Org.ValueString(), state.Project.ValueString(), state.ID.ValueString()
	if err := client.DeleteResource(ctx, r.client(), org, project, client.KindSecrets, id); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting secret", err.Error())
		return
	}
	if err := client.WaitForDeleted[client.SecretSpec](ctx, r.client(), org, project, client.KindSecrets, id); err != nil {
		resp.Diagnostics.AddError("Error waiting for secret deletion", err.Error())
	}
}

func (r *secretResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importScoped(ctx, req, resp)
}

// secretData converts the data map to the API's map[string]any.
func secretData(ctx context.Context, m types.Map, diags *diag.Diagnostics) map[string]any {
	if m.IsNull() || m.IsUnknown() {
		return nil
	}
	in := map[string]string{}
	diags.Append(m.ElementsAs(ctx, &in, false)...)
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// secretToState never touches Data: the platform does not return material.
func secretToState(env client.Envelope[client.SecretSpec], m *secretModel) {
	m.ID = types.StringValue(env.Metadata.ID)
	m.Org = types.StringValue(env.Metadata.Org)
	m.Project = types.StringValue(env.Metadata.Project)
	m.Name = types.StringValue(env.Metadata.Name)
	m.Tags = tagsFromAPI(env.Metadata.Tags, m.Tags)
	m.Region = types.StringValue(env.Spec.Region)
	m.Path = types.StringValue(env.Spec.Path)
	m.Version = types.Int64Value(env.Status.ObservedInt("version"))
	m.Phase = types.StringValue(env.Status.Phase)
	m.Simulated = types.BoolValue(env.Status.ObservedBool("simulated"))
	if m.Data.IsUnknown() {
		m.Data = types.MapNull(types.StringType)
	}
}
