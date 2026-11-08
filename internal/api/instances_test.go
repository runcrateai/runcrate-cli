package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mockInstancesServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// List instances
		case r.Method == "GET" && r.URL.Path == "/api/v1/instances":
			json.NewEncoder(w).Encode(map[string]any{
				"data": []Instance{
					{ID: "inst-1", Name: "my-gpu", Status: "running", GPUType: "A100", GPUCount: 1, IP: "10.0.0.1", CostPerHour: 1.42},
					{ID: "inst-2", Name: "train-box", Status: "deploying", GPUType: "H100", GPUCount: 8, CostPerHour: 12.0},
				},
			})

		// List types
		case r.Method == "GET" && r.URL.Path == "/api/v1/instances/types":
			gpu := r.URL.Query().Get("gpu_type")
			types := []InstanceType{
				{ID: "type-a100", GPUType: "A100", GPUCount: 1, CPUCores: 30, MemoryGB: 200, StorageGB: 512, Region: "mumbai-india-1", HourlyRate: 1.42},
				{ID: "type-h100", GPUType: "H100", GPUCount: 1, CPUCores: 32, MemoryGB: 256, StorageGB: 1024, Region: "us-east-1", HourlyRate: 3.50},
			}
			if gpu != "" {
				filtered := []InstanceType{}
				for _, t := range types {
					if t.GPUType == gpu {
						filtered = append(filtered, t)
					}
				}
				types = filtered
			}
			json.NewEncoder(w).Encode(map[string]any{"data": types})

		// Get instance
		case r.Method == "GET" && r.URL.Path == "/api/v1/instances/inst-1":
			json.NewEncoder(w).Encode(map[string]any{
				"data": Instance{ID: "inst-1", Name: "my-gpu", Status: "running", GPUType: "A100", GPUCount: 1, IP: "10.0.0.1", CostPerHour: 1.42, Region: "mumbai"},
			})

		// Get instance status
		case r.Method == "GET" && r.URL.Path == "/api/v1/instances/inst-1/status":
			json.NewEncoder(w).Encode(map[string]any{
				"data": InstanceStatus{ID: "inst-1", Status: "running", IP: "10.0.0.1"},
			})

		// Create instance
		case r.Method == "POST" && r.URL.Path == "/api/v1/instances":
			var req CreateInstanceRequest
			json.NewDecoder(r.Body).Decode(&req)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"data": Instance{ID: "inst-new", Name: req.Name, Status: "creating", GPUType: req.GPUType, GPUCount: 1, CostPerHour: 1.42},
			})

		// Delete instance
		case r.Method == "DELETE" && r.URL.Path == "/api/v1/instances/inst-1":
			w.WriteHeader(http.StatusNoContent)

		// SSH certificate
		case r.Method == "POST" && r.URL.Path == "/api/v1/instances/inst-1/ssh-certificate":
			json.NewEncoder(w).Encode(map[string]any{
				"data": SSHCertificateResponse{Certificate: "ssh-ed25519-cert-v01@openssh.com AAAA...", IP: "10.0.0.1", User: "root", Port: 22},
			})

		// Not found
		case r.Method == "GET" && r.URL.Path == "/api/v1/instances/not-found":
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "NOT_FOUND", "message": "Instance not found"}})

		// Delete not found
		case r.Method == "DELETE" && r.URL.Path == "/api/v1/instances/not-found":
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "NOT_FOUND", "message": "Instance not found"}})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestListInstances(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	instances, err := client.ListInstances("")
	if err != nil {
		t.Fatalf("ListInstances() error: %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("got %d instances, want 2", len(instances))
	}
	if instances[0].Name != "my-gpu" {
		t.Errorf("first instance name = %q, want %q", instances[0].Name, "my-gpu")
	}
	if instances[1].Status != "deploying" {
		t.Errorf("second instance status = %q, want %q", instances[1].Status, "deploying")
	}
}

func TestListInstanceTypes(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	// Unfiltered
	types, err := client.ListInstanceTypes("", "", 0)
	if err != nil {
		t.Fatalf("ListInstanceTypes() error: %v", err)
	}
	if len(types) != 2 {
		t.Fatalf("got %d types, want 2", len(types))
	}

	// Filtered by GPU
	types, err = client.ListInstanceTypes("A100", "", 0)
	if err != nil {
		t.Fatalf("ListInstanceTypes(A100) error: %v", err)
	}
	if len(types) != 1 {
		t.Fatalf("got %d types for A100, want 1", len(types))
	}
	if types[0].GPUType != "A100" {
		t.Errorf("filtered type = %q, want A100", types[0].GPUType)
	}
}

func TestGetInstance(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	inst, err := client.GetInstance("inst-1")
	if err != nil {
		t.Fatalf("GetInstance() error: %v", err)
	}
	if inst.Name != "my-gpu" {
		t.Errorf("name = %q, want %q", inst.Name, "my-gpu")
	}
	if inst.IP != "10.0.0.1" {
		t.Errorf("IP = %q, want %q", inst.IP, "10.0.0.1")
	}
}

func TestGetInstance_NotFound(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	_, err := client.GetInstance("not-found")
	if err == nil {
		t.Fatal("expected error for not-found instance, got nil")
	}
}

func TestGetInstanceStatus(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	status, err := client.GetInstanceStatus("inst-1")
	if err != nil {
		t.Fatalf("GetInstanceStatus() error: %v", err)
	}
	if status.Status != "running" {
		t.Errorf("status = %q, want %q", status.Status, "running")
	}
	if status.IP != "10.0.0.1" {
		t.Errorf("IP = %q, want %q", status.IP, "10.0.0.1")
	}
}

func TestCreateInstance(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	inst, err := client.CreateInstance(CreateInstanceRequest{
		Name:    "new-instance",
		GPUType: "A100",
	})
	if err != nil {
		t.Fatalf("CreateInstance() error: %v", err)
	}
	if inst.ID != "inst-new" {
		t.Errorf("ID = %q, want %q", inst.ID, "inst-new")
	}
	if inst.Name != "new-instance" {
		t.Errorf("name = %q, want %q", inst.Name, "new-instance")
	}
	if inst.Status != "creating" {
		t.Errorf("status = %q, want %q", inst.Status, "creating")
	}
}

// The API attaches a volume only when the body names it as storage_id, and leaves it out otherwise.
func TestCreateInstanceSendsVolumeAsStorageID(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"data": Instance{ID: "inst-new"}})
	}))
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	for _, req := range []CreateInstanceRequest{
		{Name: "with-volume", GPUType: "H100", StorageID: "vol-1"},
		{Name: "without", GPUType: "H100"},
	} {
		if _, err := client.CreateInstance(req); err != nil {
			t.Fatalf("CreateInstance(%s) error: %v", req.Name, err)
		}
	}
	if got := bodies[0]["storage_id"]; got != "vol-1" {
		t.Errorf("storage_id = %v, want vol-1", got)
	}
	if _, ok := bodies[1]["storage_id"]; ok {
		t.Errorf("storage_id sent without a volume: %v", bodies[1])
	}
}

func TestDeleteInstance(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	err := client.DeleteInstance("inst-1")
	if err != nil {
		t.Errorf("DeleteInstance() error: %v", err)
	}
}

func TestDeleteInstance_NotFound(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	err := client.DeleteInstance("not-found")
	if err == nil {
		t.Fatal("expected error for not-found delete, got nil")
	}
}

func TestGetSSHCertificate(t *testing.T) {
	server := mockInstancesServer(t)
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	cert, err := client.GetSSHCertificate("inst-1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("GetSSHCertificate() error: %v", err)
	}
	if cert.IP != "10.0.0.1" {
		t.Errorf("IP = %q, want %q", cert.IP, "10.0.0.1")
	}
	if cert.User != "root" {
		t.Errorf("user = %q, want %q", cert.User, "root")
	}
	if cert.Port != 22 {
		t.Errorf("port = %d, want 22", cert.Port)
	}
	if cert.Certificate == "" {
		t.Error("certificate is empty")
	}
}

func TestCreateInstance_AutoStopFields(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"data":{"id":"inst-new","max_runtime_minutes":240,"idle_shutdown_minutes":null,` +
			`"auto_stop_at":"2026-10-20T16:00:00.000Z","stop_reason":null}}`))
	}))
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	maxRuntime := 240
	inst, err := client.CreateInstance(CreateInstanceRequest{Name: "a", GPUType: "H100", MaxRuntimeMinutes: &maxRuntime})
	if err != nil {
		t.Fatalf("CreateInstance() error: %v", err)
	}
	if _, err := client.CreateInstance(CreateInstanceRequest{Name: "b", GPUType: "H100"}); err != nil {
		t.Fatalf("CreateInstance() error: %v", err)
	}

	if got := bodies[0]["max_runtime_minutes"]; got != float64(240) {
		t.Errorf("max_runtime_minutes sent = %v, want 240", got)
	}
	if _, ok := bodies[0]["idle_shutdown_minutes"]; ok {
		t.Error("idle_shutdown_minutes sent although unset")
	}
	for _, key := range []string{"max_runtime_minutes", "idle_shutdown_minutes"} {
		if _, ok := bodies[1][key]; ok {
			t.Errorf("%s sent although unset", key)
		}
	}

	if inst.MaxRuntimeMinutes == nil || *inst.MaxRuntimeMinutes != 240 {
		t.Errorf("MaxRuntimeMinutes = %v, want 240", inst.MaxRuntimeMinutes)
	}
	if inst.IdleShutdownMinutes != nil {
		t.Errorf("IdleShutdownMinutes = %v, want nil", *inst.IdleShutdownMinutes)
	}
	if inst.AutoStopAt == nil || *inst.AutoStopAt != "2026-10-20T16:00:00.000Z" {
		t.Errorf("AutoStopAt = %v, want 2026-10-20T16:00:00.000Z", inst.AutoStopAt)
	}
	if inst.StopReason != nil {
		t.Errorf("StopReason = %q, want nil", *inst.StopReason)
	}
}

func TestIsBudgetReached(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"budget reached", http.StatusPaymentRequired,
			`{"error":{"code":"BUDGET_REACHED","message":"Environment budget reached"}}`, true},
		{"budget reached lowercase code", http.StatusPaymentRequired,
			`{"error":{"code":"budget_reached","message":"Environment budget reached"}}`, true},
		{"low balance is a different 402", http.StatusPaymentRequired,
			`{"error":{"code":"insufficient_credits","message":"Add credits"}}`, false},
		{"other error", http.StatusBadRequest,
			`{"error":{"code":"bad_request","message":"max_runtime_minutes must be between 5 and 10080"}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer server.Close()

			_, err := NewClient("rc_live_test", server.URL).CreateInstance(CreateInstanceRequest{Name: "a"})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if got := IsBudgetReached(err); got != tt.want {
				t.Errorf("IsBudgetReached(%v) = %v, want %v", err, got, tt.want)
			}
		})
	}
}

func TestUpdateInstanceAutoStop_SendsOnlyGivenFields(t *testing.T) {
	var method, path string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		body = nil
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"data":{"id":"inst-1","max_runtime_minutes":480,"idle_shutdown_minutes":null,` +
			`"auto_stop_at":"2026-10-20T18:00:00.000Z"}}`))
	}))
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	maxRuntime := 480
	inst, err := client.UpdateInstanceAutoStop("inst-1", map[string]*int{
		"max_runtime_minutes":   &maxRuntime,
		"idle_shutdown_minutes": nil,
	})
	if err != nil {
		t.Fatalf("UpdateInstanceAutoStop() error: %v", err)
	}
	if method != "PATCH" || path != "/api/v1/instances/inst-1" {
		t.Errorf("request = %s %s, want PATCH /api/v1/instances/inst-1", method, path)
	}
	if got := body["max_runtime_minutes"]; got != float64(480) {
		t.Errorf("max_runtime_minutes sent = %v, want 480", got)
	}
	if got, ok := body["idle_shutdown_minutes"]; !ok || got != nil {
		t.Errorf("idle_shutdown_minutes sent = %v (present %v), want null", got, ok)
	}
	if inst.AutoStopAt == nil || *inst.AutoStopAt != "2026-10-20T18:00:00.000Z" {
		t.Errorf("AutoStopAt = %v", inst.AutoStopAt)
	}

	if _, err := client.UpdateInstanceAutoStop("inst-1", map[string]*int{"idle_shutdown_minutes": &maxRuntime}); err != nil {
		t.Fatalf("UpdateInstanceAutoStop() error: %v", err)
	}
	if _, ok := body["max_runtime_minutes"]; ok {
		t.Error("max_runtime_minutes sent although not given")
	}
}
