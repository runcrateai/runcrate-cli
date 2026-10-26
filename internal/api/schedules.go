package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
)

// Schedule starts an instance or a job later: once at RunAt, or on a 5-field
// Cron in Timezone.
type Schedule struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"` // "instance" or "job"
	EnvironmentID string `json:"environment_id"`
	CreatedBy     string `json:"created_by"`
	// Spec is the create body of the kind. Env values are never returned, only
	// their names under env_keys.
	Spec              map[string]any `json:"spec"`
	MaxRuntimeMinutes int            `json:"max_runtime_minutes"`
	RunAt             *string        `json:"run_at"`
	Cron              *string        `json:"cron"`
	Timezone          string         `json:"timezone"`
	Overlap           string         `json:"overlap"`
	Enabled           bool           `json:"enabled"`
	NextRunAt         *string        `json:"next_run_at"`
	LastRunAt         *string        `json:"last_run_at"`
	// LastStatus is launched, failed, skipped_budget, skipped_overlap,
	// no_access or credential_revoked.
	LastStatus *string `json:"last_status"`
	LastError  *string `json:"last_error"`
	// RunAs is the user each run launches as; RunAsCredential is how the
	// schedule was set up: user, api_key or agent.
	RunAs           *string `json:"run_as"`
	RunAsCredential string  `json:"run_as_credential"`
	LastInstanceID  *string `json:"last_instance_id"`
	LastJobID       *string `json:"last_job_id"`
	RunCount        int     `json:"run_count"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

// CreateScheduleRequest is the body of POST /api/v1/schedules. Spec is a
// CreateInstanceRequest or a CreateJobRequest; set exactly one of RunAt and Cron.
type CreateScheduleRequest struct {
	Kind     string `json:"kind"`
	Spec     any    `json:"spec"`
	RunAt    string `json:"run_at,omitempty"`
	Cron     string `json:"cron,omitempty"`
	Timezone string `json:"timezone,omitempty"`
	Overlap  string `json:"overlap,omitempty"`
	Name     string `json:"name,omitempty"`
}

// UpdateScheduleRequest is the body of PATCH /api/v1/schedules/{id}. Nil
// fields are left as they are.
type UpdateScheduleRequest struct {
	Enabled *bool `json:"enabled,omitempty"`
}

// ScheduleRun is what run-now started: Instance or Job, by the schedule's kind.
type ScheduleRun struct {
	Schedule Schedule  `json:"schedule"`
	Instance *Instance `json:"instance,omitempty"`
	Job      *Job      `json:"job,omitempty"`
}

// DeletedSchedule is the response of DELETE /api/v1/schedules/{id}.
type DeletedSchedule struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// CreateSchedule creates a schedule in the current environment.
func (c *Client) CreateSchedule(req CreateScheduleRequest) (*Schedule, error) {
	return callJSON[Schedule](c, "POST", "/api/v1/schedules", req)
}

// ListSchedules returns the environment's schedules, newest first.
func (c *Client) ListSchedules() ([]Schedule, error) {
	list, err := callJSON[[]Schedule](c, "GET", "/api/v1/schedules", nil)
	if err != nil {
		return nil, err
	}
	return *list, nil
}

// GetSchedule returns one schedule.
func (c *Client) GetSchedule(id string) (*Schedule, error) {
	return callJSON[Schedule](c, "GET", schedulePath(id), nil)
}

// UpdateSchedule changes a schedule; enabled false pauses it, true resumes it.
func (c *Client) UpdateSchedule(id string, req UpdateScheduleRequest) (*Schedule, error) {
	return callJSON[Schedule](c, "PATCH", schedulePath(id), req)
}

// DeleteSchedule removes a schedule. What it already started keeps running.
func (c *Client) DeleteSchedule(id string) (*DeletedSchedule, error) {
	return callJSON[DeletedSchedule](c, "DELETE", schedulePath(id), nil)
}

// RunScheduleNow launches the schedule's instance or job immediately, whether
// it is enabled or not, without moving its next run.
func (c *Client) RunScheduleNow(id string) (*ScheduleRun, error) {
	return callJSON[ScheduleRun](c, "POST", schedulePath(id)+"/run-now", nil)
}

func schedulePath(id string) string {
	return "/api/v1/schedules/" + url.PathEscape(id)
}

// callJSON sends body (nil for none) as JSON and decodes the { data: T } reply.
func callJSON[T any](c *Client, method, path string, body any) (*T, error) {
	return callJSONHeaders[T](c, method, path, body, nil)
}

// callJSONHeaders is callJSON with extra request headers (an Idempotency-Key).
func callJSONHeaders[T any](c *Client, method, path string, body any, headers map[string]string) (*T, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	resp, err := c.doCtxHeaders(context.Background(), method, path, reader, headers)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[T]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result.Data, nil
}
