package provider

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

// TestSchemasMatchContract checks every envelope resource's Terraform schema
// against the platform's public contract (internal/contract/openapi.public.json,
// refreshed with `make contract`):
//
//   - every non-readOnly contract field is an attribute (unless listed as
//     deliberately omitted because the platform does not act on it, ADR 0058);
//   - Required ⇔ contract `required`;
//   - Computed ⇔ `readOnly` / `x-cm-server-default` (or a provider-side default);
//   - in place ⇔ `x-cm-updatable` / present in <Kind>Update and not `x-cm-fixed`
//     (or changed through a dedicated action); everything else RequiresReplace;
//   - Sensitive ⇔ `x-cm-sensitive`.
//
// When the platform makes a field updatable, this test fails until the field is
// added to the resource's `updatable` set — that is the one-line flip.
func TestSchemasMatchContract(t *testing.T) {
	doc := loadContract(t)
	for _, c := range contractCases {
		t.Run(c.kind, func(t *testing.T) {
			attrs := resourceAttrs(t, c.factory())
			spec := doc.schema(t, c.kind+"Spec")
			create := doc.schema(t, c.kind+"Create")
			update := doc.schema(t, c.kind+"Update")

			check := func(tfName string, field jsonSchema, required, updatable bool) {
				t.Helper()
				a, ok := attrs[tfName]
				if !ok {
					t.Errorf("contract field %q has no Terraform attribute", tfName)
					return
				}
				if a.IsRequired() != required {
					t.Errorf("%s: Required=%v, contract required=%v", tfName, a.IsRequired(), required)
				}
				wantComputed := field.ReadOnly || field.ServerDefault || c.providerDefault[tfName]
				if a.IsComputed() != wantComputed {
					t.Errorf("%s: Computed=%v, contract readOnly/server-default=%v", tfName, a.IsComputed(), wantComputed)
				}
				if field.ReadOnly {
					return
				}
				inPlace := updatable || c.viaAction[tfName]
				if rr := requiresReplace(a); rr == inPlace {
					t.Errorf("%s: RequiresReplace=%v, but contract in-place=%v", tfName, rr, inPlace)
				}
				if a.IsSensitive() != field.Sensitive {
					t.Errorf("%s: Sensitive=%v, contract x-cm-sensitive=%v", tfName, a.IsSensitive(), field.Sensitive)
				}
			}

			// Top-level create fields (name, tags, data).
			for jsonName, field := range create.Properties {
				if jsonName == "spec" {
					continue
				}
				u, inUpdate := update.Properties[jsonName]
				check(tfAttrName(jsonName), field, create.isRequired(jsonName), inUpdate && !u.Fixed)
			}
			// Spec fields.
			specUpdate := update.Properties["spec"]
			for jsonName, field := range spec.Properties {
				tfName := tfAttrName(jsonName)
				if c.omitted[jsonName] {
					if _, ok := attrs[tfName]; ok {
						t.Errorf("%s is listed as omitted but the schema has it", tfName)
					}
					continue
				}
				if field.ReadOnly {
					if _, ok := attrs[tfName]; !ok {
						continue // server-owned fields may be left out
					}
				}
				u, inUpdate := specUpdate.Properties[jsonName]
				updatable := field.Updatable || (inUpdate && !u.Fixed)
				check(tfName, field, spec.isRequired(jsonName), updatable)
			}
		})
	}
}

type contractCase struct {
	kind    string
	factory func() resource.Resource
	// omitted: contract fields the provider deliberately does not offer because
	// the platform stores but never acts on them (ADR 0058).
	omitted map[string]bool
	// viaAction: attributes changed in place through a dedicated action rather
	// than :update.
	viaAction map[string]bool
	// providerDefault: Optional+Computed only because the provider sets a Default.
	providerDefault map[string]bool
}

var contractCases = []contractCase{
	{kind: "Bucket", factory: NewBucketResource, omitted: map[string]bool{"versioning": true, "websiteHost": true}},
	{kind: "Firewall", factory: NewFirewallResource},
	{kind: "Secret", factory: NewSecretResource, omitted: map[string]bool{"engine": true}},
	{kind: "Subnet", factory: NewSubnetResource, omitted: map[string]bool{"zone": true}},
	{kind: "Vm", factory: NewVMResource,
		viaAction: map[string]bool{"size": true, "disk_gb": true}},
	{kind: "Vpc", factory: NewVPCResource, omitted: map[string]bool{"zoneType": true}},
}

// --- contract loading ---

type jsonSchema struct {
	Properties    map[string]jsonSchema `json:"properties"`
	Required      []string              `json:"required"`
	ReadOnly      bool                  `json:"readOnly"`
	ServerDefault bool                  `json:"x-cm-server-default"`
	Updatable     bool                  `json:"x-cm-updatable"`
	Sensitive     bool                  `json:"x-cm-sensitive"`
	Fixed         bool                  `json:"x-cm-fixed"`
	Ref           string                `json:"$ref"`
}

func (s jsonSchema) isRequired(name string) bool {
	for _, r := range s.Required {
		if r == name {
			return true
		}
	}
	return false
}

type contractDoc struct {
	Components struct {
		Schemas map[string]jsonSchema `json:"schemas"`
	} `json:"components"`
}

func (d contractDoc) schema(t *testing.T, name string) jsonSchema {
	t.Helper()
	s, ok := d.Components.Schemas[name]
	if !ok {
		t.Fatalf("contract has no components.schemas.%s", name)
	}
	return s
}

func loadContract(t *testing.T) contractDoc {
	t.Helper()
	raw, err := os.ReadFile("../contract/openapi.public.json")
	if err != nil {
		t.Fatalf("read contract: %v", err)
	}
	var d contractDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("parse contract: %v", err)
	}
	return d
}

// --- schema introspection ---

func resourceAttrs(t *testing.T, r resource.Resource) map[string]schema.Attribute {
	t.Helper()
	var resp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", resp.Diagnostics)
	}
	return resp.Schema.Attributes
}

type describer interface {
	Description(context.Context) string
}

// requiresReplace reports whether an attribute carries an unconditional
// RequiresReplace plan modifier.
func requiresReplace(a schema.Attribute) bool {
	var mods []describer
	switch v := a.(type) {
	case schema.StringAttribute:
		for _, m := range v.PlanModifiers {
			mods = append(mods, m)
		}
	case schema.BoolAttribute:
		for _, m := range v.PlanModifiers {
			mods = append(mods, m)
		}
	case schema.Int64Attribute:
		for _, m := range v.PlanModifiers {
			mods = append(mods, m)
		}
	case schema.MapAttribute:
		for _, m := range v.PlanModifiers {
			mods = append(mods, m)
		}
	case schema.ListAttribute:
		for _, m := range v.PlanModifiers {
			mods = append(mods, m)
		}
	}
	for _, m := range mods {
		if strings.Contains(m.Description(context.Background()), "destroy and recreate") {
			return true
		}
	}
	return false
}

// tfAttrName maps a camelCase contract name to the snake_case attribute name.
func tfAttrName(jsonName string) string {
	switch jsonName {
	case "diskGB":
		return "disk_gb"
	case "vpcId":
		return "vpc_id"
	case "subnetId":
		return "subnet_id"
	case "subnetCidr":
		return "subnet_cidr"
	case "sshKeys":
		return "ssh_keys"
	case "userData":
		return "user_data"
	case "cloneFromId":
		return "clone_from_id"
	}
	var b strings.Builder
	for i, r := range jsonName {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestContractCasesCoverEnvelopeKinds guards against a new kind appearing in the
// contract without a provider decision.
func TestContractCasesCoverEnvelopeKinds(t *testing.T) {
	doc := loadContract(t)
	have := map[string]bool{}
	for _, c := range contractCases {
		have[c.kind] = true
	}
	var missing []string
	for name := range doc.Components.Schemas {
		if k, ok := strings.CutSuffix(name, "Spec"); ok && !have[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("contract kinds without a contract case (add a resource or an explicit exclusion): %v", missing)
	}
}
