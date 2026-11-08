package commands

import (
	"strings"
	"testing"

	"github.com/runcrate/cli/internal/api"
)

func TestRegionLabel(t *testing.T) {
	regions := []api.StorageRegion{
		{ID: "137", Name: "us-east-1", Provider: "wasabi"},
		{ID: "582", Name: "eu-west-1", Provider: "wasabi"},
	}
	tests := []struct {
		name    string
		regions []api.StorageRegion
		region  string
		want    string
	}{
		{"an id shows its name", regions, "582", "eu-west-1"},
		{"a region name from an older volume is shown as it is", regions, "us-west-1", "us-west-1"},
		{"an id nobody lists is shown as it is", regions, "999", "999"},
		{"no regions (the lookup failed) shows the id", nil, "582", "582"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := regionLabel(tt.regions, tt.region); got != tt.want {
				t.Errorf("regionLabel(%q) = %q, want %q", tt.region, got, tt.want)
			}
		})
	}
}

// The API refuses a volume without a region ("Missing required fields: name, size_gb, region"), so the
// command says so before it signs in or sends anything.
func TestVolumesCreateNeedsARegion(t *testing.T) {
	flags := storageCreateCmd.Flags()
	t.Cleanup(func() {
		for _, name := range []string{"name", "size", "region"} {
			f := flags.Lookup(name)
			f.Value.Set(f.DefValue)
			f.Changed = false
		}
	})
	flags.Set("name", "datasets")
	flags.Set("size", "100")

	err := storageCreateCmd.RunE(storageCreateCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "--region is required") {
		t.Fatalf("err = %v, want one naming --region", err)
	}
}
