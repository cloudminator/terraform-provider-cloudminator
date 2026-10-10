package client

import (
	"context"
	"fmt"
	"time"
)

// Phases of status.phase (platform internal/resource).
const (
	PhasePending  = "Pending"
	PhaseCreating = "Creating"
	PhaseRunning  = "Running"
	PhaseStopped  = "Stopped"
	PhaseUpdating = "Updating"
	PhaseDeleting = "Deleting"
	PhaseDeleted  = "Deleted"
	PhaseError    = "Error"
)

// Metadata is the uniform resource metadata.
type Metadata struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	Org       string            `json:"org"`
	Project   string            `json:"project"`
	Name      string            `json:"name"`
	Tags      map[string]string `json:"tags,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

// Status is the async, observed half of a resource.
type Status struct {
	Phase    string         `json:"phase"`
	Message  string         `json:"message,omitempty"`
	Observed map[string]any `json:"observed,omitempty"`
}

// ObservedString returns status.observed[key] as a string ("" if absent).
func (s Status) ObservedString(key string) string {
	v, _ := s.Observed[key].(string)
	return v
}

// ObservedBool returns status.observed[key] as a bool (false if absent).
func (s Status) ObservedBool(key string) bool {
	v, _ := s.Observed[key].(bool)
	return v
}

// ObservedInt returns status.observed[key] as an int64 (JSON numbers decode as float64).
func (s Status) ObservedInt(key string) int64 {
	switch v := s.Observed[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

// Envelope is the uniform resource object {metadata, spec, status}.
type Envelope[S any] struct {
	Metadata Metadata `json:"metadata"`
	Spec     S        `json:"spec"`
	Status   Status   `json:"status"`
}

// CreateRequest is the generic create body.
type CreateRequest[S any] struct {
	Name string            `json:"name"`
	Tags map[string]string `json:"tags,omitempty"`
	Spec S                 `json:"spec"`
}

// Kind names (URL segments).
const (
	KindBuckets   = "buckets"
	KindVMs       = "vms"
	KindVPCs      = "vpcs"
	KindSubnets   = "subnets"
	KindFirewalls = "firewalls"
	KindSecrets   = "secrets"
)

func kindPath(org, project, kind string) string {
	return fmt.Sprintf("/v1/orgs/%s/projects/%s/%s", esc(org), esc(project), kind)
}

func itemPath(org, project, kind, id string) string {
	return kindPath(org, project, kind) + "/" + esc(id)
}

// CreateResource POSTs a create (with an Idempotency-Key) and returns the envelope.
func CreateResource[S any](ctx context.Context, c *Client, org, project, kind string, body any) (Envelope[S], error) {
	var out Envelope[S]
	err := c.PostIdempotent(ctx, kindPath(org, project, kind), body, &out)
	return out, err
}

// GetResource GETs one resource.
func GetResource[S any](ctx context.Context, c *Client, org, project, kind, id string) (Envelope[S], error) {
	var out Envelope[S]
	err := c.Get(ctx, itemPath(org, project, kind, id), &out)
	return out, err
}

// DeleteResource DELETEs one resource (async: 202, record removed later).
func DeleteResource(ctx context.Context, c *Client, org, project, kind, id string) error {
	return c.Delete(ctx, itemPath(org, project, kind, id))
}

// Act invokes POST …/{id}:{action} and decodes the response into out (may be nil).
func Act(ctx context.Context, c *Client, org, project, kind, id, action string, body, out any) error {
	if body == nil {
		body = map[string]any{}
	}
	return c.Post(ctx, itemPath(org, project, kind, id)+":"+action, body, out)
}

// UpdateResource invokes the :update action and returns the updated envelope.
func UpdateResource[S any](ctx context.Context, c *Client, org, project, kind, id string, body any) (Envelope[S], error) {
	var out Envelope[S]
	err := Act(ctx, c, org, project, kind, id, "update", body, &out)
	return out, err
}

// PollInterval is how often the waiters poll. Tests shorten it.
var PollInterval = 3 * time.Second

// WaitForPhase polls until status.phase is Running or Stopped, failing on Error
// (with status.message) or when ctx expires.
func WaitForPhase[S any](ctx context.Context, c *Client, org, project, kind, id string) (Envelope[S], error) {
	for {
		env, err := GetResource[S](ctx, c, org, project, kind, id)
		if err != nil {
			if ctx.Err() != nil {
				return env, fmt.Errorf("timed out waiting for %s %q to become ready: %w", kind, id, err)
			}
			return env, err
		}
		switch env.Status.Phase {
		case PhaseRunning, PhaseStopped:
			return env, nil
		case PhaseError:
			msg := env.Status.Message
			if msg == "" {
				msg = "no message"
			}
			return env, fmt.Errorf("%s %q entered phase Error: %s", kind, id, msg)
		}
		select {
		case <-ctx.Done():
			return env, fmt.Errorf("timed out waiting for %s %q to become ready (last phase %q): %w", kind, id, env.Status.Phase, ctx.Err())
		case <-time.After(PollInterval):
		}
	}
}

// WaitForDeleted polls until GET returns 404.
func WaitForDeleted[S any](ctx context.Context, c *Client, org, project, kind, id string) error {
	for {
		env, err := GetResource[S](ctx, c, org, project, kind, id)
		if IsNotFound(err) {
			return nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("timed out waiting for %s %q to be deleted: %w", kind, id, err)
			}
			return err
		}
		if env.Status.Phase == PhaseError {
			return fmt.Errorf("%s %q failed to delete: %s", kind, id, env.Status.Message)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for %s %q to be deleted (last phase %q): %w", kind, id, env.Status.Phase, ctx.Err())
		case <-time.After(PollInterval):
		}
	}
}
