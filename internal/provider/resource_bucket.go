package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/cloudminator/terraform-provider-cloudminator/internal/client"
)

// bucketUpdatable: tags, quota_bytes and max_objects via :update. The name,
// placement and storage class are fixed for the bucket's life.
// TODO(ADR 0061): flip further fields here as the platform's :update grows;
// Update sends whichever updatable attribute changed.
var bucketUpdatable = updatable{"tags": true, "quota_bytes": true, "max_objects": true}

var (
	_ resource.Resource                = &bucketResource{}
	_ resource.ResourceWithConfigure   = &bucketResource{}
	_ resource.ResourceWithImportState = &bucketResource{}
)

// NewBucketResource is the cloudminator_bucket factory.
func NewBucketResource() resource.Resource { return &bucketResource{} }

type bucketResource struct{ base }

type bucketModel struct {
	ID           types.String   `tfsdk:"id"`
	Org          types.String   `tfsdk:"org"`
	Project      types.String   `tfsdk:"project"`
	Name         types.String   `tfsdk:"name"`
	Tags         types.Map      `tfsdk:"tags"`
	Region       types.String   `tfsdk:"region"`
	Zone         types.String   `tfsdk:"zone"`
	StorageClass types.String   `tfsdk:"storage_class"`
	QuotaBytes   types.Int64    `tfsdk:"quota_bytes"`
	MaxObjects   types.Int64    `tfsdk:"max_objects"`
	Endpoint     types.String   `tfsdk:"endpoint"`
	S3Name       types.String   `tfsdk:"s3_name"`
	Phase        types.String   `tfsdk:"phase"`
	Simulated    types.Bool     `tfsdk:"simulated"`
	Timeouts     timeouts.Value `tfsdk:"timeouts"`
}

func (r *bucketResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket"
}

func (r *bucketResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.configure(req.ProviderData, &resp.Diagnostics)
}

func (r *bucketResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	u := bucketUpdatable
	resp.Schema = schema.Schema{
		MarkdownDescription: "An S3-compatible object-storage bucket. The bucket name is its id. " +
			"`tags`, `quota_bytes` and `max_objects` change in place; everything else replaces the bucket.",
		Attributes: map[string]schema.Attribute{
			"id":      idAttr("Bucket id (equal to `name`)."),
			"org":     orgAttr(),
			"project": projectAttr(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Bucket name; also its id. Use `s3_name` when addressing the bucket from an S3 client.",
				Required:            true,
				PlanModifiers:       u.str("name"),
			},
			"tags":          tagsAttr(u),
			"region":        optionalComputedString(u, "region", "Region id. Defaults to the platform's first storage region."),
			"zone":          optionalComputedString(u, "zone", "Zone id. Empty lets the platform choose."),
			"storage_class": optionalComputedString(u, "storage_class", "Storage class."),
			"quota_bytes": schema.Int64Attribute{
				MarkdownDescription: "Maximum stored bytes (0 = plan default).",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       u.int64("quota_bytes", int64planmodifier.UseStateForUnknown()),
			},
			"max_objects": schema.Int64Attribute{
				MarkdownDescription: "Maximum object count (0 = plan default).",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       u.int64("max_objects", int64planmodifier.UseStateForUnknown()),
			},
			"endpoint":  computedString("S3 endpoint URL."),
			"s3_name":   computedString("The name an S3 client must use to address this bucket (org/project-qualified)."),
			"phase":     phaseAttr(),
			"simulated": simulatedAttr(),
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *bucketResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketModel
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
	spec := client.BucketSpec{
		Region:       plan.Region.ValueString(),
		Zone:         plan.Zone.ValueString(),
		StorageClass: plan.StorageClass.ValueString(),
		QuotaBytes:   plan.QuotaBytes.ValueInt64(),
		MaxObjects:   plan.MaxObjects.ValueInt64(),
	}
	env, err := client.CreateResource[client.BucketSpec](ctx, r.client(), org, project, client.KindBuckets,
		client.CreateRequest[client.BucketSpec]{Name: plan.Name.ValueString(), Tags: tags, Spec: spec})
	if err != nil {
		resp.Diagnostics.AddError("Error creating bucket", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathID, env.Metadata.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathOrg, org)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, pathProject, project)...)

	env, err = client.WaitForPhase[client.BucketSpec](ctx, r.client(), org, project, client.KindBuckets, env.Metadata.ID)
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for bucket to become ready", err.Error())
		return
	}
	bucketToState(env, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	env, err := client.GetResource[client.BucketSpec](ctx, r.client(), state.Org.ValueString(), state.Project.ValueString(), client.KindBuckets, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading bucket", err.Error())
		return
	}
	bucketToState(env, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *bucketResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state bucketModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := plan.Timeouts.Update(ctx, defaultUpdateTimeout)
	resp.Diagnostics.Append(d...)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	org, project, id := state.Org.ValueString(), state.Project.ValueString(), state.ID.ValueString()
	// Bucket :update nests spec changes under "spec"; fixed fields are never sent.
	body, spec := map[string]any{}, map[string]any{}
	u := bucketUpdatable
	if u["tags"] && !plan.Tags.Equal(state.Tags) {
		body["tags"] = tagsToUpdate(ctx, plan.Tags, &resp.Diagnostics)
	}
	if u["quota_bytes"] && !plan.QuotaBytes.IsUnknown() && !plan.QuotaBytes.Equal(state.QuotaBytes) {
		spec["quotaBytes"] = plan.QuotaBytes.ValueInt64()
	}
	if u["max_objects"] && !plan.MaxObjects.IsUnknown() && !plan.MaxObjects.Equal(state.MaxObjects) {
		spec["maxObjects"] = plan.MaxObjects.ValueInt64()
	}
	if len(spec) > 0 {
		body["spec"] = spec
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if len(body) > 0 {
		if _, err := client.UpdateResource[client.BucketSpec](ctx, r.client(), org, project, client.KindBuckets, id, body); err != nil {
			resp.Diagnostics.AddError("Error updating bucket", err.Error())
			return
		}
	}
	env, err := client.GetResource[client.BucketSpec](ctx, r.client(), org, project, client.KindBuckets, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading bucket after update", err.Error())
		return
	}
	bucketToState(env, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state bucketModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := state.Timeouts.Delete(ctx, defaultDeleteTimeout)
	resp.Diagnostics.Append(d...)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	org, project, id := state.Org.ValueString(), state.Project.ValueString(), state.ID.ValueString()
	if err := client.DeleteResource(ctx, r.client(), org, project, client.KindBuckets, id); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting bucket", err.Error())
		return
	}
	if err := client.WaitForDeleted[client.BucketSpec](ctx, r.client(), org, project, client.KindBuckets, id); err != nil {
		resp.Diagnostics.AddError("Error waiting for bucket deletion", err.Error())
	}
}

func (r *bucketResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importScoped(ctx, req, resp)
}

func bucketToState(env client.Envelope[client.BucketSpec], m *bucketModel) {
	s := env.Spec
	m.ID = types.StringValue(env.Metadata.ID)
	m.Org = types.StringValue(env.Metadata.Org)
	m.Project = types.StringValue(env.Metadata.Project)
	m.Name = types.StringValue(env.Metadata.Name)
	m.Tags = tagsFromAPI(env.Metadata.Tags, m.Tags)
	m.Region = types.StringValue(s.Region)
	m.Zone = types.StringValue(s.Zone)
	m.StorageClass = types.StringValue(s.StorageClass)
	m.QuotaBytes = types.Int64Value(s.QuotaBytes)
	m.MaxObjects = types.Int64Value(s.MaxObjects)
	m.Endpoint = types.StringValue(env.Status.ObservedString("endpoint"))
	m.S3Name = types.StringValue(env.Status.ObservedString("s3Name"))
	m.Phase = types.StringValue(env.Status.Phase)
	m.Simulated = types.BoolValue(env.Status.ObservedBool("simulated"))
}
