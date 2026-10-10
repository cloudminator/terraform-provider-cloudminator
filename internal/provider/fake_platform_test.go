package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePlatform is an in-memory stand-in for platformd's uniform resource API,
// just faithful enough for the provider's CRUD paths: async create
// (Creating → Running on the next GET), :update / :resize / :start / :stop, and
// async delete (Deleting → 404).
type fakePlatform struct {
	mu        sync.Mutex
	items     map[string]map[string]any // path → envelope
	seq       int
	deleting  map[string]bool
	lastBody  map[string]map[string]any // "<kind>:<action>" → last request body
	createdBy map[string]string         // Idempotency-Key → path
}

func newFakePlatform(t *testing.T) *httptest.Server {
	t.Helper()
	f := &fakePlatform{
		items: map[string]map[string]any{}, deleting: map[string]bool{},
		lastBody: map[string]map[string]any{}, createdBy: map[string]string{},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return srv
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakePlatform) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer cm_test" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no principal"})
		return
	}
	// /v1/orgs/{org}/projects/{project}/{kind}[/{id}[:{action}]]
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/orgs/"), "/")
	if len(parts) < 4 || parts[1] != "projects" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no route"})
		return
	}
	org, project, kind := parts[0], parts[2], parts[3]
	base := fmt.Sprintf("/v1/orgs/%s/projects/%s/%s", org, project, kind)
	var body map[string]any
	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}

	if len(parts) == 4 && r.Method == http.MethodPost {
		if p, ok := f.createdBy[r.Header.Get("Idempotency-Key")]; ok && r.Header.Get("Idempotency-Key") != "" {
			w.Header().Set("Idempotent-Replayed", "true")
			writeJSON(w, http.StatusOK, f.items[p])
			return
		}
		name, _ := body["name"].(string)
		id := name
		if kind != "buckets" {
			f.seq++
			id = fmt.Sprintf("%s-%024x", strings.TrimSuffix(kind, "s"), f.seq)
		}
		p := base + "/" + id
		if _, exists := f.items[p]; exists {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "already exists"})
			return
		}
		spec, _ := body["spec"].(map[string]any)
		if spec == nil {
			spec = map[string]any{}
		}
		observed := map[string]any{"simulated": true}
		switch kind {
		case "buckets":
			if spec["region"] == nil {
				spec["region"] = "eu-central-1"
			}
			observed["endpoint"] = "https://s3.example.test"
			observed["s3Name"] = org + "-" + project + "-" + name
		case "vms":
			if spec["region"] == nil {
				spec["region"] = "eu-central-1"
			}
			if spec["diskGB"] == nil {
				spec["diskGB"] = 20
			}
			if ip, ok := spec["ip"].(string); ok && ip != "" && !strings.Contains(ip, "/") {
				spec["ip"] = ip + "/24"
			}
			observed["address"] = "10.0.0.5"
		}
		env := map[string]any{
			"metadata": map[string]any{
				"id": id, "kind": kind, "org": org, "project": project, "name": name, "tags": body["tags"],
				"createdAt": time.Now().UTC(), "updatedAt": time.Now().UTC(),
			},
			"spec":   spec,
			"status": map[string]any{"phase": "Creating", "observed": observed},
		}
		f.items[p] = env
		if k := r.Header.Get("Idempotency-Key"); k != "" {
			f.createdBy[k] = p
		}
		writeJSON(w, http.StatusAccepted, env)
		return
	}
	if len(parts) != 5 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no route"})
		return
	}
	id, action, _ := strings.Cut(parts[4], ":")
	p := base + "/" + id
	env, ok := f.items[p]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": kind + " not found"})
		return
	}
	status := env["status"].(map[string]any)
	spec := env["spec"].(map[string]any)
	meta := env["metadata"].(map[string]any)

	switch {
	case r.Method == http.MethodGet:
		if f.deleting[p] {
			delete(f.items, p)
			delete(f.deleting, p)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": kind + " not found"})
			return
		}
		if status["phase"] == "Creating" {
			status["phase"] = "Running"
		}
		writeJSON(w, http.StatusOK, env)
	case r.Method == http.MethodDelete:
		status["phase"] = "Deleting"
		f.deleting[p] = true
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "deleting"})
	case r.Method == http.MethodPost && action != "":
		f.lastBody[kind+":"+action] = body
		switch action {
		case "update":
			if n, ok := body["name"]; ok {
				meta["name"] = n
			}
			if t, ok := body["tags"]; ok {
				meta["tags"] = t
			}
			if s, ok := body["spec"].(map[string]any); ok {
				for k, v := range s {
					spec[k] = v
				}
			}
		case "resize":
			if s, ok := body["size"]; ok {
				spec["size"] = s
			}
			if d, ok := body["diskGB"]; ok {
				spec["diskGB"] = d
			}
		case "start":
			status["phase"] = "Running"
		case "stop":
			status["phase"] = "Stopped"
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported action " + action})
			return
		}
		writeJSON(w, http.StatusOK, env)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}
