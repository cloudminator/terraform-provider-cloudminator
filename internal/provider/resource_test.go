package provider

import (
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/cloudminator/terraform-provider-cloudminator/internal/client"
)

var testProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"cloudminator": providerserver.NewProtocol6WithError(New("test")()),
}

func providerBlock(endpoint string) string {
	return fmt.Sprintf(`
provider "cloudminator" {
  endpoint = %q
  api_key  = "cm_test"
  org      = "acme"
  project  = "prod"
}
`, endpoint)
}

func init() { client.PollInterval = 10 * time.Millisecond }

// These run the real Terraform CLI against an in-memory fake platform, so they
// need a terraform (or tofu via TF_ACC_TERRAFORM_PATH) binary but no credentials.

func TestBucketLifecycle(t *testing.T) {
	srv := newFakePlatform(t)
	cfg := func(quota int, env string) string {
		return providerBlock(srv.URL) + fmt.Sprintf(`
resource "cloudminator_bucket" "b" {
  name        = "backups"
  quota_bytes = %d
  tags        = { env = %q }
}
`, quota, env)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg(1000, "prod"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("cloudminator_bucket.b", "id", "backups"),
					resource.TestCheckResourceAttr("cloudminator_bucket.b", "org", "acme"),
					resource.TestCheckResourceAttr("cloudminator_bucket.b", "project", "prod"),
					resource.TestCheckResourceAttr("cloudminator_bucket.b", "region", "eu-central-1"),
					resource.TestCheckResourceAttr("cloudminator_bucket.b", "phase", "Running"),
					resource.TestCheckResourceAttr("cloudminator_bucket.b", "s3_name", "acme-prod-backups"),
					resource.TestCheckResourceAttr("cloudminator_bucket.b", "simulated", "true"),
				),
			},
			{
				// quota and tags change in place via :update.
				Config: cfg(2000, "staging"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("cloudminator_bucket.b", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("cloudminator_bucket.b", "quota_bytes", "2000"),
					resource.TestCheckResourceAttr("cloudminator_bucket.b", "tags.env", "staging"),
				),
			},
			{
				ResourceName:      "cloudminator_bucket.b",
				ImportState:       true,
				ImportStateId:     "acme/prod/backups",
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"timeouts"},
			},
			{
				// Changing the region replaces the bucket.
				Config: providerBlock(srv.URL) + `
resource "cloudminator_bucket" "b" {
  name        = "backups"
  region      = "eu-north-1"
  quota_bytes = 2000
  tags        = { env = "staging" }
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("cloudminator_bucket.b", plancheck.ResourceActionDestroyBeforeCreate)},
				},
			},
		},
	})
}

func TestVMLifecycle(t *testing.T) {
	srv := newFakePlatform(t)
	cfg := func(name, size string, disk int, running bool) string {
		return providerBlock(srv.URL) + fmt.Sprintf(`
resource "cloudminator_vm" "web" {
  name    = %q
  image   = "debian-13"
  size    = %q
  disk_gb = %d
  running = %t
  ip      = "10.0.0.7"
}
`, name, size, disk, running)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg("web", "2c-4g", 20, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("cloudminator_vm.web", "id", regexp.MustCompile(`^vm-[0-9a-f]{24}$`)),
					resource.TestCheckResourceAttr("cloudminator_vm.web", "phase", "Running"),
					resource.TestCheckResourceAttr("cloudminator_vm.web", "address", "10.0.0.5"),
					// The platform stores "10.0.0.7/24"; the configured bare address is kept.
					resource.TestCheckResourceAttr("cloudminator_vm.web", "ip", "10.0.0.7"),
				),
			},
			{
				// Rename, resize, grow the disk and power off: all in place.
				Config: cfg("web-1", "4c-8g", 40, false),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("cloudminator_vm.web", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("cloudminator_vm.web", "name", "web-1"),
					resource.TestCheckResourceAttr("cloudminator_vm.web", "size", "4c-8g"),
					resource.TestCheckResourceAttr("cloudminator_vm.web", "disk_gb", "40"),
					resource.TestCheckResourceAttr("cloudminator_vm.web", "running", "false"),
					resource.TestCheckResourceAttr("cloudminator_vm.web", "phase", "Stopped"),
				),
			},
			{
				// Shrinking the disk replaces the VM.
				Config: cfg("web-1", "4c-8g", 30, false),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("cloudminator_vm.web", plancheck.ResourceActionDestroyBeforeCreate)},
				},
			},
			{
				ResourceName:            "cloudminator_vm.web",
				ImportState:             true,
				ImportStateIdFunc:       importID("cloudminator_vm.web"),
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"timeouts", "ip"},
			},
		},
	})
}

func importID(name string) resource.ImportStateIdFunc {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return "", fmt.Errorf("%s not in state", name)
		}
		a := rs.Primary.Attributes
		return a["org"] + "/" + a["project"] + "/" + a["id"], nil
	}
}
