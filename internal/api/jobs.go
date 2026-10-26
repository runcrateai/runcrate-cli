package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Job is one shell command run on its own machine (RUN-486, jobs v2).
type Job struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Status              string            `json:"status"`
	Command             string            `json:"command"`
	GPUType             string            `json:"gpu_type"`
	GPUCount            int               `json:"gpu_count"`
	GPUCandidates       []GPUCandidate    `json:"gpu_candidates,omitempty"`
	Template            string            `json:"template"`
	Image               string            `json:"image"`
	EnvKeys             []string          `json:"env_keys"`
	StorageID           string            `json:"storage_id"`
	MaxRuntimeMinutes   int               `json:"max_runtime_minutes"`
	CapacityWaitMinutes int               `json:"capacity_wait_minutes"`
	QueueReason         string            `json:"queue_reason,omitempty"`
	NextAttemptAt       string            `json:"next_attempt_at,omitempty"`
	Attempt             int               `json:"attempt"`
	MaxRetries          int               `json:"max_retries"`
	RetryOn             []string          `json:"retry_on,omitempty"`
	RetryExitCodes      []int             `json:"retry_exit_codes,omitempty"`
	Attempts            []JobAttempt      `json:"attempts,omitempty"`
	ExitCode            *int              `json:"exit_code"`
	Error               string            `json:"error"`
	InstanceID          string            `json:"instance_id"`
	EnvironmentID       string            `json:"environment_id"`
	Tags                map[string]string `json:"tags,omitempty"`
	GroupID             string            `json:"group_id,omitempty"`
	JobIndex            int               `json:"job_index"`
	RerunOf             string            `json:"rerun_of,omitempty"`
	WebhookURL          string            `json:"webhook_url,omitempty"`
	WebhookEvents       []string          `json:"webhook_events,omitempty"`
	// WebhookSecret signs the callbacks; only the answer that created the job has it.
	WebhookSecret     string             `json:"webhook_secret,omitempty"`
	CreatedAt         string             `json:"created_at"`
	StartedAt         string             `json:"started_at"`
	EndedAt           string             `json:"ended_at"`
	CostPerHour       float64            `json:"cost_per_hour"`
	CostUSD           *float64           `json:"cost_usd"`
	LogBytes          int64              `json:"log_bytes"`
	LogsTruncated     bool               `json:"logs_truncated"`
	Code              *JobCodeInfo       `json:"code,omitempty"`
	AllowCPUContainer bool               `json:"allow_cpu_container"`
	ImageRegistryAuth *ImageRegistryAuth `json:"image_registry_auth,omitempty"`
	ArtifactsCount    int                `json:"artifacts_count"`
	GPUUtilAvg        *float64           `json:"gpu_util_avg,omitempty"`
	GPUMemPeakMB      *float64           `json:"gpu_mem_peak_mb,omitempty"`
}

// GPUCandidate is one GPU a job may run on, in the order they are tried.
type GPUCandidate struct {
	GPUType  string `json:"gpu_type"`
	GPUCount int    `json:"gpu_count"`
}

// JobAttempt is one attempt of a job; a retry starts a new one on a new machine.
type JobAttempt struct {
	Attempt      int      `json:"attempt"`
	DeploymentID string   `json:"deployment_id"`
	Status       string   `json:"status"`
	ExitCode     *int     `json:"exit_code"`
	Error        string   `json:"error"`
	StartedAt    string   `json:"started_at"`
	EndedAt      string   `json:"ended_at"`
	CostUSD      *float64 `json:"cost_usd"`
	GPUType      string   `json:"gpu_type"`
	GPUCount     int      `json:"gpu_count"`
	Region       string   `json:"region"`
}

// JobCodeInfo is a job's code as fetched: Ref is the upload id or the git ref.
type JobCodeInfo struct {
	Source string `json:"source"`
	URL    string `json:"url,omitempty"`
	Ref    string `json:"ref,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Commit string `json:"commit,omitempty"`
}

// ImageRegistryAuth names the env variables (or secrets) holding a private
// image's registry login.
type ImageRegistryAuth struct {
	UsernameEnv string `json:"username_env"`
	PasswordEnv string `json:"password_env"`
}

// JobCode is the code a job fetches into /workspace: an upload or a git repo.
type JobCode struct {
	UploadID string  `json:"upload_id,omitempty"`
	Git      *JobGit `json:"git,omitempty"`
}

// JobGit is a git repository over https; TokenEnv names the env variable or
// secret holding a token for a private one.
type JobGit struct {
	URL      string `json:"url"`
	Ref      string `json:"ref,omitempty"`
	TokenEnv string `json:"token_env,omitempty"`
}

// JobGroup is a group of jobs from count or params.
type JobGroup struct {
	ID           string         `json:"id"`
	Count        int            `json:"count"`
	MaxParallel  int            `json:"max_parallel"`
	StatusCounts map[string]int `json:"status_counts"`
	CostUSD      *float64       `json:"cost_usd"`
	CreatedAt    string         `json:"created_at"`
	// Jobs and WebhookSecret are only in the answer that created the group.
	Jobs          []Job  `json:"jobs,omitempty"`
	WebhookSecret string `json:"webhook_secret,omitempty"`
}

// JobSubmission is what a submit or rerun started: one job, or a group.
type JobSubmission struct {
	Job   *Job
	Group *JobGroup
	// Replayed means the Idempotency-Key was seen before: this is the first answer again.
	Replayed bool
}

// LogChunk is a raw fragment of a job's combined stdout and stderr. Seq and
// TS are null on the marker chunk that stands where truncated output was dropped.
type LogChunk struct {
	Seq  *int64  `json:"seq"`
	TS   *string `json:"ts"`
	Text string  `json:"text"`
}

// JobLogs is one page of a job's output. Pass Cursor back as `after`; Done means
// the job has ended and every chunk was read.
type JobLogs struct {
	JobID     string     `json:"job_id"`
	Status    string     `json:"status"`
	Chunks    []LogChunk `json:"chunks"`
	Cursor    int64      `json:"cursor"`
	Truncated bool       `json:"truncated"`
	Done      bool       `json:"done"`
}

// JobList is one page of jobs, newest first.
type JobList struct {
	Jobs    []Job
	Cursor  string
	HasMore bool
}

// JobFilters narrows GET /api/v1/jobs. Empty fields do not filter.
type JobFilters struct {
	// Status is a comma list, e.g. running,queued.
	Status string
	// Tags are key: value pairs a job must all have.
	Tags map[string]string
	// GroupID keeps the jobs of one group.
	GroupID string
	// Query is a substring of the name.
	Query string
	// GPUType is what the job ran (or runs) on.
	GPUType string
	// CreatedAfter is an RFC 3339 time.
	CreatedAfter string
}

// Artifact is a file a job's command wrote to $RUNCRATE_OUTPUT_DIR.
type Artifact struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// JobUpload is where to PUT a job's code: submit with code.upload_id before
// ExpiresAt. URL is empty when a job already uses the upload.
type JobUpload struct {
	UploadID     string `json:"upload_id"`
	URL          string `json:"url"`
	URLExpiresAt string `json:"url_expires_at"`
	ExpiresAt    string `json:"expires_at"`
	MaxBytes     int64  `json:"max_bytes"`
}

// CreateJobRequest is the body of POST /api/v1/jobs. A schedule's job spec is
// the same body with only the first ten fields.
type CreateJobRequest struct {
	Command string `json:"command"`
	// GPUType is a GPU name (string) or GPUs to try in order ([]string); an
	// entry may carry its count, e.g. "A100-80GB:2".
	GPUType           any               `json:"gpu_type"`
	MaxRuntimeMinutes int               `json:"max_runtime_minutes"`
	GPUCount          int               `json:"gpu_count,omitempty"`
	Template          string            `json:"template,omitempty"`
	Image             string            `json:"image,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	StorageID         string            `json:"storage_id,omitempty"`
	Name              string            `json:"name,omitempty"`
	Region            string            `json:"region,omitempty"`

	CapacityWaitMinutes *int                `json:"capacity_wait_minutes,omitempty"`
	MaxRetries          int                 `json:"max_retries,omitempty"`
	RetryOn             []string            `json:"retry_on,omitempty"`
	RetryExitCodes      []int               `json:"retry_exit_codes,omitempty"`
	Count               int                 `json:"count,omitempty"`
	Params              []map[string]string `json:"params,omitempty"`
	MaxParallel         int                 `json:"max_parallel,omitempty"`
	Tags                map[string]string   `json:"tags,omitempty"`
	WebhookURL          string              `json:"webhook_url,omitempty"`
	WebhookEvents       []string            `json:"webhook_events,omitempty"`
	Code                *JobCode            `json:"code,omitempty"`
	ImageRegistryAuth   *ImageRegistryAuth  `json:"image_registry_auth,omitempty"`
	AllowCPUContainer   bool                `json:"allow_cpu_container,omitempty"`
}

const (
	// SubmitTimeout is how long a submit or rerun may take: the route launches
	// for up to 120 s before it answers.
	SubmitTimeout = 135 * time.Second
	// GroupCancelTimeout is how long a group cancel may take: the route stops
	// every machine, each with a final volume copy-up, for up to 300 s.
	GroupCancelTimeout = 315 * time.Second
)

// SubmitNetworkError is a submit (a job or a top-up) whose answer never came:
// it may have been made. Sending the same request with Key answers that one
// instead of making another.
type SubmitNetworkError struct {
	Key string
	Err error
}

func (e *SubmitNetworkError) Error() string { return "network error: " + e.Err.Error() }
func (e *SubmitNetworkError) Unwrap() error { return e.Err }

// withTimeout is c with a request timeout of at least d, for routes that may
// take longer than the default to answer.
func (c *Client) withTimeout(d time.Duration) *Client {
	long := *c
	httpClient := *c.httpClient
	httpClient.Timeout = max(httpClient.Timeout, d)
	long.httpClient = &httpClient
	return &long
}

// LongPollTimeout bounds a follow=true logs request. The server holds it for
// up to 25 s, so this leaves room for the response without hanging on a dead
// connection for the client's default timeout.
const LongPollTimeout = 40 * time.Second

// NewIdempotencyKey is a random key for one submit: sending it again within 24
// hours answers the first submit instead of starting more jobs.
func NewIdempotencyKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// submit POSTs a job body with an Idempotency-Key and reads a job or a group.
// When the job was created but its machine could not be launched, the
// *APIError carries Details["job_id"]; when no answer came, the error is a
// *SubmitNetworkError with the key to send again.
func (c *Client) submit(path string, body any, idempotencyKey string) (*JobSubmission, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}
	if idempotencyKey == "" {
		idempotencyKey = NewIdempotencyKey()
	}
	resp, err := c.withTimeout(SubmitTimeout).doCtxHeaders(context.Background(), "POST", path, bytes.NewReader(raw),
		map[string]string{"Idempotency-Key": idempotencyKey})
	if err != nil {
		return nil, &SubmitNetworkError{Key: idempotencyKey, Err: err}
	}
	defer resp.Body.Close()
	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[json.RawMessage]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	var probe struct {
		Jobs json.RawMessage `json:"jobs"`
	}
	_ = json.Unmarshal(result.Data, &probe)
	sub := &JobSubmission{Replayed: resp.Header.Get("Idempotent-Replayed") == "true"}
	if len(probe.Jobs) > 0 {
		sub.Group = &JobGroup{}
		err = json.Unmarshal(result.Data, sub.Group)
	} else {
		sub.Job = &Job{}
		err = json.Unmarshal(result.Data, sub.Job)
	}
	if err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return sub, nil
}

// CreateJob submits a job, or a group with Count or Params. An empty
// idempotencyKey gets a fresh one.
func (c *Client) CreateJob(req CreateJobRequest, idempotencyKey string) (*JobSubmission, error) {
	return c.submit("/api/v1/jobs", req, idempotencyKey)
}

// RerunJob runs a job's spec again as a new job, with its env values;
// overrides holds the submit fields to change (env is merged over the job's).
func (c *Client) RerunJob(id string, overrides map[string]any, idempotencyKey string) (*JobSubmission, error) {
	if overrides == nil {
		overrides = map[string]any{}
	}
	return c.submit("/api/v1/jobs/"+url.PathEscape(id)+"/rerun", overrides, idempotencyKey)
}

// getJSON GETs path and decodes its { data } into out.
func (c *Client) getJSON(path string, out any) error {
	resp, err := c.do("GET", path, nil)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()
	if err := checkHTTPStatus(resp); err != nil {
		return err
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiEnvelope[any]{Data: out}); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}
	return nil
}

// postJSON POSTs body (nil for none) to path and decodes its { data } into out.
func (c *Client) postJSON(path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	resp, err := c.do("POST", path, reader)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()
	if err := checkHTTPStatus(resp); err != nil {
		return err
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiEnvelope[any]{Data: out}); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}
	return nil
}

// ListJobs returns one page of jobs. limit <= 0 uses the server default;
// cursor is meta.cursor from a previous page.
func (c *Client) ListJobs(f JobFilters, limit int, cursor string) (*JobList, error) {
	q := url.Values{}
	if f.Status != "" {
		q.Set("status", f.Status)
	}
	for k, v := range f.Tags {
		q.Add("tag", k+":"+v)
	}
	if f.GroupID != "" {
		q.Set("group_id", f.GroupID)
	}
	if f.Query != "" {
		q.Set("q", f.Query)
	}
	if f.GPUType != "" {
		q.Set("gpu_type", f.GPUType)
	}
	if f.CreatedAfter != "" {
		q.Set("created_after", f.CreatedAfter)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	path := "/api/v1/jobs"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	resp, err := c.do("GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result struct {
		Data []Job `json:"data"`
		Meta struct {
			Cursor  any  `json:"cursor"`
			HasMore bool `json:"hasMore"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	list := &JobList{Jobs: result.Data, HasMore: result.Meta.HasMore}
	// The cursor is opaque: keep it as the string to send back.
	switch v := result.Meta.Cursor.(type) {
	case string:
		list.Cursor = v
	case float64:
		list.Cursor = strconv.FormatFloat(v, 'f', -1, 64)
	}
	return list, nil
}

// GetJob returns one job.
func (c *Client) GetJob(id string) (*Job, error) {
	var job Job
	if err := c.getJSON("/api/v1/jobs/"+url.PathEscape(id), &job); err != nil {
		return nil, err
	}
	return &job, nil
}

// GetJobLogs returns chunks after the seq cursor `after`. With follow, the
// server waits up to 25 s for new output or the end, and the request is bounded
// by LongPollTimeout. limit <= 0 uses the server default. Cancelling ctx aborts
// the request.
func (c *Client) GetJobLogs(ctx context.Context, id string, after int64, follow bool, limit int) (*JobLogs, error) {
	q := url.Values{}
	q.Set("after", strconv.FormatInt(after, 10))
	if follow {
		q.Set("follow", "true")
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, LongPollTimeout)
		defer cancel()
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}

	resp, err := c.doCtx(ctx, "GET", "/api/v1/jobs/"+url.PathEscape(id)+"/logs?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[JobLogs]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result.Data, nil
}

// CancelJob stops the job's machine; the job becomes cancelled.
func (c *Client) CancelJob(id string) (*Job, error) {
	var job Job
	if err := c.postJSON("/api/v1/jobs/"+url.PathEscape(id)+"/cancel", nil, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

// DeleteJob deletes a job that ended, with its logs and outputs. A live job
// is a 409: cancel it first.
func (c *Client) DeleteJob(id string) error {
	resp, err := c.do("DELETE", "/api/v1/jobs/"+url.PathEscape(id), nil)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()
	return checkHTTPStatus(resp)
}

// GetJobGroup returns a group of jobs.
func (c *Client) GetJobGroup(id string) (*JobGroup, error) {
	var group JobGroup
	if err := c.getJSON("/api/v1/job-groups/"+url.PathEscape(id), &group); err != nil {
		return nil, err
	}
	return &group, nil
}

// CancelJobGroup cancels every job of the group that has not ended.
func (c *Client) CancelJobGroup(id string) (*JobGroup, error) {
	var group JobGroup
	if err := c.withTimeout(GroupCancelTimeout).postJSON("/api/v1/job-groups/"+url.PathEscape(id)+"/cancel", nil, &group); err != nil {
		return nil, err
	}
	return &group, nil
}

// ListArtifacts returns a job's outputs by path.
func (c *Client) ListArtifacts(id string) ([]Artifact, error) {
	var artifacts []Artifact
	if err := c.getJSON("/api/v1/jobs/"+url.PathEscape(id)+"/artifacts", &artifacts); err != nil {
		return nil, err
	}
	return artifacts, nil
}

// ArtifactURL returns the signed URL (valid an hour) that downloads one
// output. The redirect is read, not followed, so the API credentials never go
// to the storage host.
func (c *Client) ArtifactURL(id, path string) (string, error) {
	segments := strings.Split(path, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	req, err := c.newRequest(context.Background(), "GET",
		"/api/v1/jobs/"+url.PathEscape(id)+"/artifacts/"+strings.Join(segments, "/"), nil, nil)
	if err != nil {
		return "", err
	}
	noFollow := *c.httpClient
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noFollow.Do(req)
	if err != nil {
		return "", fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", checkHTTPStatus(resp)
	}
	location, err := resp.Location()
	if err != nil {
		return "", fmt.Errorf("expected a download link, got HTTP %d", resp.StatusCode)
	}
	return location.String(), nil
}

// DownloadSigned copies a signed URL's body to w. No credentials are sent.
func DownloadSigned(ctx context.Context, signedURL string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, "GET", signedURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "runcrate-cli/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("download failed (HTTP %d)", resp.StatusCode)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// CreateUpload asks where to PUT a tar.gz of `size` bytes. The same sha256
// again answers the same upload, so a repeat makes no second one.
func (c *Client) CreateUpload(size int64, sha256 string) (*JobUpload, error) {
	var upload JobUpload
	body := map[string]any{"bytes": size, "sha256": sha256}
	if err := c.postJSON("/api/v1/jobs/uploads", body, &upload); err != nil {
		return nil, err
	}
	return &upload, nil
}

// PutUpload sends the archive to an upload's signed URL. No credentials are sent.
func PutUpload(ctx context.Context, signedURL string, archive []byte) error {
	req, err := http.NewRequestWithContext(ctx, "PUT", signedURL, bytes.NewReader(archive))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(archive))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return errors.New(strings.TrimSpace(fmt.Sprintf("upload failed (HTTP %d) %s", resp.StatusCode, msg)))
	}
	return nil
}
