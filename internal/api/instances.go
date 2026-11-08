package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Instance represents an instance from the v1 API.
type Instance struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	GPUType     string  `json:"gpu_type"`
	GPUCount    int     `json:"gpu_count"`
	CPUCores    int     `json:"cpu_cores"`
	Memory      int     `json:"memory"`
	Storage     int     `json:"storage"`
	Region      string  `json:"region"`
	IP          string  `json:"ip"`
	OSImage     string  `json:"os_image"`
	CostPerHour float64 `json:"cost_per_hour"`
	DeployedAt  string  `json:"deployed_at"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`

	// Auto stop guardrails. nil means off (no limit) or, for AutoStopAt and
	// StopReason, not applicable yet.
	MaxRuntimeMinutes   *int    `json:"max_runtime_minutes"`
	IdleShutdownMinutes *int    `json:"idle_shutdown_minutes"`
	AutoStopAt          *string `json:"auto_stop_at"`
	StopReason          *string `json:"stop_reason"`
}

// InstanceType represents an available GPU instance type.
type InstanceType struct {
	ID             string  `json:"id"`
	GPUType        string  `json:"gpu_type"`
	GPUCount       int     `json:"gpu_count"`
	CPUCores       int     `json:"cpu_cores"`
	MemoryGB       int     `json:"memory_gb"`
	StorageGB      int     `json:"storage_gb"`
	Region         string  `json:"region"`
	DeploymentType string  `json:"deployment_type"`
	HourlyRate     float64 `json:"hourly_rate"`
}

// InstanceStatus represents the live status of an instance.
type InstanceStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	IP     string `json:"ip"`
}

// CreateInstanceRequest is the payload for creating an instance.
type CreateInstanceRequest struct {
	Name           string   `json:"name"`
	GPUType        string   `json:"gpu_type,omitempty"`
	GPUCount       int      `json:"gpu_count,omitempty"`
	Region         string   `json:"region,omitempty"`
	SSHKeyID       string   `json:"ssh_key_id,omitempty"`
	Template       string   `json:"template,omitempty"`
	InstanceTypeID string   `json:"instance_type_id,omitempty"`
	StorageID      string   `json:"storage_id,omitempty"`
	EnvVars        []EnvVar `json:"env_vars,omitempty"`
	StartupCmds    []string `json:"startup_commands,omitempty"`

	// Optional auto stop guardrails, in whole minutes. Omitted when nil.
	MaxRuntimeMinutes   *int `json:"max_runtime_minutes,omitempty"`
	IdleShutdownMinutes *int `json:"idle_shutdown_minutes,omitempty"`
}

// EnvVar is a key-value pair for instance environment variables.
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// apiEnvelope wraps the standard { data: T } response.
type apiEnvelope[T any] struct {
	Data T `json:"data"`
}

// SSHCertificateResponse is the response from the SSH certificate endpoint.
type SSHCertificateResponse struct {
	Certificate string `json:"certificate"`
	IP          string `json:"ip"`
	User        string `json:"user"`
	Port        int    `json:"port"`
}

// GetSSHCertificate requests a signed SSH certificate for an instance.
func (c *Client) GetSSHCertificate(instanceID, publicKey string) (*SSHCertificateResponse, error) {
	payload := fmt.Sprintf(`{"public_key":%q}`, publicKey)
	resp, err := c.do("POST", "/api/v1/instances/"+instanceID+"/ssh-certificate", strings.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[SSHCertificateResponse]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result.Data, nil
}

// checkHTTPStatus returns an *APIError for non-2xx responses.
func checkHTTPStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	var envelope errorEnvelope
	_ = json.NewDecoder(resp.Body).Decode(&envelope)
	apiErr := envelope.Error
	apiErr.Status = resp.StatusCode
	return &apiErr
}

// ListInstances returns all instances for the authenticated user.
func (c *Client) ListInstances(search string) ([]Instance, error) {
	path := "/api/v1/instances"
	if search != "" {
		path += "?search=" + search
	}

	resp, err := c.do("GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[[]Instance]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return result.Data, nil
}

// ListInstanceTypes returns available GPU instance types with optional filters.
func (c *Client) ListInstanceTypes(gpuType, region string, gpuCount int) ([]InstanceType, error) {
	var params []string
	if gpuType != "" {
		params = append(params, "gpu_type="+gpuType)
	}
	if region != "" {
		params = append(params, "region="+region)
	}
	if gpuCount > 0 {
		params = append(params, fmt.Sprintf("gpu_count=%d", gpuCount))
	}

	path := "/api/v1/instances/types"
	if len(params) > 0 {
		path += "?" + strings.Join(params, "&")
	}

	resp, err := c.do("GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[[]InstanceType]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return result.Data, nil
}

// GetInstance returns details for a single instance.
func (c *Client) GetInstance(id string) (*Instance, error) {
	resp, err := c.do("GET", "/api/v1/instances/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[Instance]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result.Data, nil
}

// GetInstanceStatus returns the live status of an instance.
func (c *Client) GetInstanceStatus(id string) (*InstanceStatus, error) {
	resp, err := c.do("GET", "/api/v1/instances/"+id+"/status", nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[InstanceStatus]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result.Data, nil
}

// CreateInstance creates and provisions a new instance.
func (c *Client) CreateInstance(req CreateInstanceRequest) (*Instance, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}

	resp, err := c.do("POST", "/api/v1/instances", strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[Instance]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result.Data, nil
}

// UpdateInstanceAutoStop changes a live instance's auto stop. Only the keys present
// in fields are sent ("max_runtime_minutes", "idle_shutdown_minutes"); a nil value
// sends null, which turns that limit off.
func (c *Client) UpdateInstanceAutoStop(id string, fields map[string]*int) (*Instance, error) {
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}

	resp, err := c.do("PATCH", "/api/v1/instances/"+id, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[Instance]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result.Data, nil
}

// DeleteInstance terminates an instance.
func (c *Client) DeleteInstance(id string) error {
	resp, err := c.do("DELETE", "/api/v1/instances/"+id, nil)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 204 {
		return nil
	}
	return checkHTTPStatus(resp)
}
