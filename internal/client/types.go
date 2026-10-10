package client

import (
	"context"
	"net/url"
	"time"
)

// Spec types mirror the platform's Go structs (cloudminator-com-platform
// internal/{buckets,vms,networking,secrets}). Field names are the JSON keys.

// BucketSpec mirrors buckets.Spec.
type BucketSpec struct {
	Region       string `json:"region,omitempty"`
	Zone         string `json:"zone,omitempty"`
	StorageClass string `json:"storageClass,omitempty"`
	QuotaBytes   int64  `json:"quotaBytes,omitempty"`
	MaxObjects   int64  `json:"maxObjects,omitempty"`
	// versioning and websiteHost exist on the platform but are inert (never
	// applied by the storage backend), so the provider does not offer them
	// (ADR 0058).
}

// VMSpec mirrors the customer-settable part of vms.Spec. Bridge/Gateway/
// SubnetCIDR are server-resolved and the clone fields are action-only.
type VMSpec struct {
	Image      string   `json:"image"`
	Size       string   `json:"size"`
	Region     string   `json:"region,omitempty"`
	Zone       string   `json:"zone,omitempty"`
	DiskGB     int64    `json:"diskGB,omitempty"`
	SSHKeys    []string `json:"sshKeys,omitempty"`
	User       string   `json:"user,omitempty"`
	UserData   string   `json:"userData,omitempty"`
	SubnetID   string   `json:"subnetId,omitempty"`
	IP         string   `json:"ip,omitempty"`
	Gateway    string   `json:"gateway,omitempty"`
	SubnetCIDR string   `json:"subnetCidr,omitempty"`
}

// VPCSpec mirrors networking.VPCSpec.
type VPCSpec struct {
	CIDR   string `json:"cidr,omitempty"`
	Region string `json:"region"`
	// zoneType is stored but inert on the platform; not offered (ADR 0058).
}

// SubnetSpec mirrors networking.SubnetSpec.
type SubnetSpec struct {
	VPCID   string `json:"vpcId"`
	CIDR    string `json:"cidr,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	SNAT    bool   `json:"snat"`
	DHCP    bool   `json:"dhcp"`
}

// FirewallSpec mirrors networking.FirewallRuleSpec.
type FirewallSpec struct {
	SubnetID string `json:"subnetId"`
	Protocol string `json:"protocol,omitempty"`
	Port     string `json:"port,omitempty"`
	Source   string `json:"source,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

// SecretSpec mirrors secrets.Spec. Path is server-assigned.
type SecretSpec struct {
	Path   string `json:"path,omitempty"`
	Region string `json:"region,omitempty"`
	// engine is stored but inert on the platform; not offered (ADR 0058).
}

// SecretCreateRequest adds the write-only material to the generic create body.
type SecretCreateRequest struct {
	Name string            `json:"name"`
	Tags map[string]string `json:"tags,omitempty"`
	Spec SecretSpec        `json:"spec"`
	Data map[string]any    `json:"data,omitempty"`
}

// --- bucket keys (actions on a bucket) ---

// BucketKey is the :key.create response (secret shown once).
type BucketKey struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey,omitempty"`
	Name            string `json:"name"`
	Simulated       bool   `json:"simulated,omitempty"`
}

// BucketKeyRecord is one :key.list entry (never the secret).
type BucketKeyRecord struct {
	AccessKeyID string `json:"accessKeyId"`
	Name        string `json:"name"`
	CreatedAt   string `json:"createdAt,omitempty"`
	Simulated   bool   `json:"simulated,omitempty"`
}

// CreateBucketKey issues an access key on a bucket.
func CreateBucketKey(ctx context.Context, c *Client, org, project, bucket, name string) (BucketKey, error) {
	var out BucketKey
	err := Act(ctx, c, org, project, KindBuckets, bucket, "key.create", map[string]string{"name": name}, &out)
	return out, err
}

// ListBucketKeys lists a bucket's keys.
func ListBucketKeys(ctx context.Context, c *Client, org, project, bucket string) ([]BucketKeyRecord, error) {
	var out []BucketKeyRecord
	err := Act(ctx, c, org, project, KindBuckets, bucket, "key.list", nil, &out)
	return out, err
}

// RevokeBucketKey revokes one access key.
func RevokeBucketKey(ctx context.Context, c *Client, org, project, bucket, accessKeyID string) error {
	return Act(ctx, c, org, project, KindBuckets, bucket, "key.revoke", map[string]string{"accessKeyId": accessKeyID}, nil)
}

// --- projects (bespoke routes) ---

// Project is a project view.
type Project struct {
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
}

// CreateProject creates a project; the slug is derived from the name server-side.
func (c *Client) CreateProject(ctx context.Context, org, name string) (Project, error) {
	var out Project
	err := c.Post(ctx, "/v1/orgs/"+esc(org)+"/projects", map[string]string{"name": name}, &out)
	return out, err
}

// GetProject finds a project by slug via the list route (there is no single GET).
func (c *Client) GetProject(ctx context.Context, org, slug string) (Project, error) {
	var out struct {
		Projects []Project `json:"projects"`
	}
	if err := c.Get(ctx, "/v1/orgs/"+esc(org)+"/projects", &out); err != nil {
		return Project{}, err
	}
	for _, p := range out.Projects {
		if p.Slug == slug {
			return p, nil
		}
	}
	return Project{}, &APIError{StatusCode: 404, Method: "GET", Path: "/v1/orgs/" + org + "/projects/" + slug, Message: "project not found"}
}

// RenameProject changes a project's display name (slug is immutable).
func (c *Client) RenameProject(ctx context.Context, org, slug, name string) error {
	return c.Patch(ctx, "/v1/orgs/"+esc(org)+"/projects/"+esc(slug), map[string]string{"name": name}, nil)
}

// DeleteProject deletes a project.
func (c *Client) DeleteProject(ctx context.Context, org, slug string) error {
	return c.Delete(ctx, "/v1/orgs/"+esc(org)+"/projects/"+esc(slug))
}

// --- SSH keys (bespoke, org-scoped) ---

// SSHKey is an org SSH public key record.
type SSHKey struct {
	ID          string    `json:"id"`
	Org         string    `json:"org"`
	Name        string    `json:"name"`
	KeyType     string    `json:"keyType"`
	PublicKey   string    `json:"publicKey"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"createdAt"`
}

// CreateSSHKey adds a key to the org's ring.
func (c *Client) CreateSSHKey(ctx context.Context, org, name, publicKey string) (SSHKey, error) {
	var out SSHKey
	err := c.Post(ctx, "/v1/orgs/"+esc(org)+"/sshkeys", map[string]string{"name": name, "publicKey": publicKey}, &out)
	return out, err
}

// GetSSHKey finds a key by id via the list route (there is no single GET).
func (c *Client) GetSSHKey(ctx context.Context, org, id string) (SSHKey, error) {
	var out struct {
		Items []SSHKey `json:"items"`
	}
	if err := c.Get(ctx, "/v1/orgs/"+esc(org)+"/sshkeys", &out); err != nil {
		return SSHKey{}, err
	}
	for _, k := range out.Items {
		if k.ID == id {
			return k, nil
		}
	}
	return SSHKey{}, &APIError{StatusCode: 404, Method: "GET", Path: "/v1/orgs/" + org + "/sshkeys/" + id, Message: "ssh key not found"}
}

// DeleteSSHKey removes a key.
func (c *Client) DeleteSSHKey(ctx context.Context, org, id string) error {
	return c.Delete(ctx, "/v1/orgs/"+esc(org)+"/sshkeys/"+esc(id))
}

// --- catalogs (public) ---

// Capabilities says what a region/zone can host.
type Capabilities struct {
	Compute bool `json:"compute"`
	Storage bool `json:"storage"`
}

// Zone is one availability zone.
type Zone struct {
	ID           string       `json:"id"`
	Capabilities Capabilities `json:"capabilities"`
	Simulated    bool         `json:"simulated,omitempty"`
}

// Region is one region with its zones.
type Region struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Location     string       `json:"location"`
	Zones        []Zone       `json:"zones"`
	Capabilities Capabilities `json:"capabilities"`
	Simulated    bool         `json:"simulated,omitempty"`
}

// ListRegions returns the fleet-derived region catalog.
func (c *Client) ListRegions(ctx context.Context) ([]Region, error) {
	var out struct {
		Regions []Region `json:"regions"`
	}
	err := c.Get(ctx, "/v1/regions", &out)
	return out.Regions, err
}

// Flavor is a VM instance type.
type Flavor struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	VCPUs          int64  `json:"vcpus"`
	MemMB          int64  `json:"memMB"`
	DiskGB         int64  `json:"diskGB"`
	Size           string `json:"size"`
	ComputeUnits   int64  `json:"computeUnits"`
	PriceHourCents int64  `json:"priceHourCents"`
}

// ListFlavors returns the VM instance types.
func (c *Client) ListFlavors(ctx context.Context) ([]Flavor, error) {
	var out struct {
		Flavors []Flavor `json:"flavors"`
	}
	err := c.Get(ctx, "/v1/flavors", &out)
	return out.Flavors, err
}

// Image is a provisionable OS image.
type Image struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// ImageList is the /v1/images response.
type ImageList struct {
	Images    []Image `json:"images"`
	Region    string  `json:"region,omitempty"`
	Simulated bool    `json:"simulated,omitempty"`
}

// ListImages returns images, optionally scoped to a region.
func (c *Client) ListImages(ctx context.Context, region string) (ImageList, error) {
	path := "/v1/images"
	if region != "" {
		path += "?region=" + url.QueryEscape(region)
	}
	var out ImageList
	err := c.Get(ctx, path, &out)
	return out, err
}

// PlanLimits is pricing.Limits. The platform struct has no json tags, so the
// keys are the Go field names.
type PlanLimits struct {
	Resources map[string]int64 `json:"Resources"`
	Aggregate map[string]int64 `json:"Aggregate"`
	Quotas    map[string]int64 `json:"Quotas"`
}

// Plan is a plan tier.
type Plan struct {
	Code    string     `json:"code"`
	Name    string     `json:"name"`
	Limits  PlanLimits `json:"limits"`
	Default bool       `json:"default,omitempty"`
}

// ListPlans returns the plan tiers.
func (c *Client) ListPlans(ctx context.Context) ([]Plan, error) {
	var out struct {
		Plans []Plan `json:"plans"`
	}
	err := c.Get(ctx, "/v1/plans", &out)
	return out.Plans, err
}
