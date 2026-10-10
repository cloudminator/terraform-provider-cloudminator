package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/cloudminator/terraform-provider-cloudminator/internal/client"
)

const (
	defaultCreateTimeout = 20 * time.Minute
	defaultUpdateTimeout = 20 * time.Minute
	defaultDeleteTimeout = 20 * time.Minute
)

// base carries the configured provider data into a resource.
type base struct {
	data *providerData
}

func (b *base) configure(pd any, diags *diag.Diagnostics) {
	if pd == nil {
		return
	}
	d, ok := pd.(*providerData)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("expected *providerData, got %T", pd))
		return
	}
	b.data = d
}

func (b *base) client() *client.Client { return b.data.client }

// resolveOrg returns the planned org, else the provider default.
func (b *base) resolveOrg(v types.String, diags *diag.Diagnostics) string {
	if !v.IsNull() && !v.IsUnknown() && v.ValueString() != "" {
		return v.ValueString()
	}
	if b.data != nil && b.data.org != "" {
		return b.data.org
	}
	diags.AddAttributeError(path.Root("org"), "Missing organization",
		"Set org on the resource, or org / "+EnvOrg+" on the provider.")
	return ""
}

// resolveProject returns the planned project, else the provider default.
func (b *base) resolveProject(v types.String) string {
	if !v.IsNull() && !v.IsUnknown() && v.ValueString() != "" {
		return v.ValueString()
	}
	if b.data != nil && b.data.project != "" {
		return b.data.project
	}
	return "default"
}

// configureDataSource is base.configure for data sources.
func configureDataSource(req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) *providerData {
	if req.ProviderData == nil {
		return nil
	}
	d, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *providerData, got %T", req.ProviderData))
		return nil
	}
	return d
}

// --- the in-place vs replace seam (ADR 0062) ---
//
// Each resource declares the set of attributes the platform can change in place
// (via `POST …/{id}:update` or a dedicated action). Every other configurable
// attribute forces replacement. Moving an attribute to in-place is: add it to the
// resource's updatable set, and send it in that resource's Update.

// updatable is a resource's set of in-place attributes.
type updatable map[string]bool

func (u updatable) str(name string, extra ...planmodifier.String) []planmodifier.String {
	if !u[name] {
		extra = append(extra, stringplanmodifier.RequiresReplace())
	}
	return extra
}

func (u updatable) boolean(name string, extra ...planmodifier.Bool) []planmodifier.Bool {
	if !u[name] {
		extra = append(extra, boolplanmodifier.RequiresReplace())
	}
	return extra
}

func (u updatable) int64(name string, extra ...planmodifier.Int64) []planmodifier.Int64 {
	if !u[name] {
		extra = append(extra, int64planmodifier.RequiresReplace())
	}
	return extra
}

func (u updatable) mapping(name string, extra ...planmodifier.Map) []planmodifier.Map {
	if !u[name] {
		extra = append(extra, mapplanmodifier.RequiresReplace())
	}
	return extra
}

func (u updatable) list(name string, extra ...planmodifier.List) []planmodifier.List {
	if !u[name] {
		extra = append(extra, listplanmodifier.RequiresReplace())
	}
	return extra
}

// --- shared schema attributes ---

func idAttr(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

func orgAttr() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Organization slug. Defaults to the provider's `org`. Changing it forces a new resource.",
		Optional:            true,
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
	}
}

func projectAttr() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Project slug. Defaults to the provider's `project` (or `default`). Changing it forces a new resource.",
		Optional:            true,
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
	}
}

func phaseAttr() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Lifecycle phase reported by the platform (`Running`, `Stopped`, …).",
		Computed:            true,
	}
}

func simulatedAttr() schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: "True when the platform served this from a simulated (non-production) backend: nothing real exists behind it.",
		Computed:            true,
		PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
	}
}

func tagsAttr(u updatable) schema.MapAttribute {
	desc := "Key/value tags. Used by attribute-based access control, so a change can be denied if the new tags fall outside the caller's scope."
	if !u["tags"] {
		desc += " Changing tags forces a new resource."
	}
	return schema.MapAttribute{
		MarkdownDescription: desc,
		ElementType:         types.StringType,
		Optional:            true,
		PlanModifiers:       u.mapping("tags"),
	}
}

// optionalComputedString is a configurable attribute the server may fill in.
func optionalComputedString(u updatable, name, desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Optional:            true,
		Computed:            true,
		PlanModifiers:       u.str(name, stringplanmodifier.UseStateForUnknown()),
	}
}

func computedString(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

// --- value conversion helpers ---

// tagsToAPI converts a tags map. Null → nil.
func tagsToAPI(ctx context.Context, m types.Map, diags *diag.Diagnostics) map[string]string {
	if m.IsNull() || m.IsUnknown() {
		return nil
	}
	out := map[string]string{}
	diags.Append(m.ElementsAs(ctx, &out, false)...)
	return out
}

// tagsToUpdate is tagsToAPI for an :update body, where null must clear (non-nil empty map).
func tagsToUpdate(ctx context.Context, m types.Map, diags *diag.Diagnostics) map[string]string {
	if t := tagsToAPI(ctx, m, diags); t != nil {
		return t
	}
	return map[string]string{}
}

// tagsFromAPI converts server tags, keeping null when the user had none.
func tagsFromAPI(t map[string]string, prior types.Map) types.Map {
	if len(t) == 0 && (prior.IsNull() || prior.IsUnknown()) {
		return types.MapNull(types.StringType)
	}
	elems := make(map[string]attr.Value, len(t))
	for k, v := range t {
		elems[k] = types.StringValue(v)
	}
	return types.MapValueMust(types.StringType, elems)
}

// optString converts "" to null for Optional (non-computed) attributes.
func optString(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// splitImportID splits an import ID into exactly n "/"-separated non-empty parts.
func splitImportID(id string, n int, format string, diags *diag.Diagnostics) []string {
	parts := strings.Split(id, "/")
	if len(parts) != n {
		diags.AddError("Invalid import ID", fmt.Sprintf("expected %q, got %q", format, id))
		return nil
	}
	for _, p := range parts {
		if p == "" {
			diags.AddError("Invalid import ID", fmt.Sprintf("expected %q, got %q", format, id))
			return nil
		}
	}
	return parts
}

// importScoped implements `org/project/id` import for envelope resources.
func importScoped(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := splitImportID(req.ID, 3, "org/project/id", &resp.Diagnostics)
	if parts == nil {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("org"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[2])...)
}

var (
	pathID      = path.Root("id")
	pathOrg     = path.Root("org")
	pathProject = path.Root("project")
)
