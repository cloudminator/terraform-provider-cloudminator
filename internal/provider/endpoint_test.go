package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestResolveEndpoint(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv(EnvEndpoint, "")
		if got := resolveEndpoint(types.StringNull()); got != DefaultEndpoint {
			t.Fatalf("got %q, want %q", got, DefaultEndpoint)
		}
	})
	t.Run("env overrides default", func(t *testing.T) {
		t.Setenv(EnvEndpoint, "https://api.cloudminator.nl")
		if got := resolveEndpoint(types.StringNull()); got != "https://api.cloudminator.nl" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("argument overrides env", func(t *testing.T) {
		t.Setenv(EnvEndpoint, "https://api.cloudminator.nl")
		if got := resolveEndpoint(types.StringValue("http://localhost:8080")); got != "http://localhost:8080" {
			t.Fatalf("got %q", got)
		}
	})
}
