package provider

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Acceptance tests create REAL resources. They run only with TF_ACC=1 and
// CLOUDMINATOR_ENDPOINT / CLOUDMINATOR_API_KEY / CLOUDMINATOR_ORG pointing at a
// disposable organization on a non-production platform — never a customer org.

func testAccPreCheck(t *testing.T) {
	t.Helper()
	for _, env := range []string{EnvEndpoint, EnvAPIKey, EnvOrg} {
		if os.Getenv(env) == "" {
			t.Fatalf("%s must be set for acceptance tests", env)
		}
	}
}

func TestAccDataSources(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: `
data "cloudminator_regions" "all" {}
data "cloudminator_flavors" "all" {}
data "cloudminator_images" "all" {}
data "cloudminator_plans" "all" {}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("data.cloudminator_regions.all", "regions.#"),
				resource.TestCheckResourceAttrSet("data.cloudminator_flavors.all", "flavors.#"),
				resource.TestCheckResourceAttrSet("data.cloudminator_images.all", "images.#"),
				resource.TestCheckResourceAttrSet("data.cloudminator_plans.all", "plans.#"),
			),
		}},
	})
}

func TestAccBucket(t *testing.T) {
	name := fmt.Sprintf("tfacc-%d", time.Now().UnixNano())
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "cloudminator_bucket" "b" {
  name = %q
  tags = { purpose = "tfacc" }
}
resource "cloudminator_bucket_key" "k" {
  bucket = cloudminator_bucket.b.id
}
`, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("cloudminator_bucket.b", "id", name),
					resource.TestCheckResourceAttr("cloudminator_bucket.b", "phase", "Running"),
					resource.TestCheckResourceAttrSet("cloudminator_bucket_key.k", "access_key_id"),
					resource.TestCheckResourceAttrSet("cloudminator_bucket_key.k", "secret_access_key"),
				),
			},
			{
				ResourceName:            "cloudminator_bucket.b",
				ImportState:             true,
				ImportStateIdFunc:       importID("cloudminator_bucket.b"),
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"timeouts"},
			},
		},
	})
}

func TestAccNetworkAndSecret(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: `
data "cloudminator_regions" "all" {}

resource "cloudminator_vpc" "v" {
  name   = "tfacc"
  region = data.cloudminator_regions.all.regions[0].id
}
resource "cloudminator_subnet" "s" {
  name   = "tfacc"
  vpc_id = cloudminator_vpc.v.id
  snat   = false
}
resource "cloudminator_firewall" "https" {
  name      = "https"
  subnet_id = cloudminator_subnet.s.id
  protocol  = "tcp"
  port      = "443"
}
resource "cloudminator_secret" "s" {
  name = "tfacc"
  data = { password = "not-a-real-secret" }
}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("cloudminator_vpc.v", "cidr"),
				resource.TestCheckResourceAttr("cloudminator_subnet.s", "snat", "false"),
				resource.TestCheckResourceAttrSet("cloudminator_secret.s", "path"),
			),
		}},
	})
}
