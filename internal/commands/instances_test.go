package commands

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseGPUFlag(t *testing.T) {
	tests := []struct {
		name            string
		gpuFlag         string
		gpuCountFlag    int
		gpuCountChanged bool
		wantType        string
		wantCount       int
		wantErr         bool
	}{
		{
			name:      "colon syntax H100:2",
			gpuFlag:   "H100:2",
			wantType:  "H100",
			wantCount: 2,
		},
		{
			name:      "colon syntax A100:4",
			gpuFlag:   "A100:4",
			wantType:  "A100",
			wantCount: 4,
		},
		{
			name:      "colon syntax single GPU",
			gpuFlag:   "L4:1",
			wantType:  "L4",
			wantCount: 1,
		},
		{
			name:         "plain type with default count",
			gpuFlag:      "H100",
			gpuCountFlag: 1,
			wantType:     "H100",
			wantCount:    1,
		},
		{
			name:            "plain type with explicit gpu-count",
			gpuFlag:         "H100",
			gpuCountFlag:    4,
			gpuCountChanged: true,
			wantType:        "H100",
			wantCount:       4,
		},
		{
			name:            "colon syntax conflicts with gpu-count",
			gpuFlag:         "H100:2",
			gpuCountFlag:    4,
			gpuCountChanged: true,
			wantErr:         true,
		},
		{
			name:    "colon with invalid count",
			gpuFlag: "H100:abc",
			wantErr: true,
		},
		{
			name:    "colon with zero count",
			gpuFlag: "H100:0",
			wantErr: true,
		},
		{
			name:    "colon with negative count",
			gpuFlag: "H100:-1",
			wantErr: true,
		},
		{
			name:         "empty gpu flag",
			gpuFlag:      "",
			wantType:     "",
			wantCount:    1,
			gpuCountFlag: 1,
		},
		{
			name:         "empty gpu flag with types default",
			gpuFlag:      "",
			gpuCountFlag: 0,
			wantType:     "",
			wantCount:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotCount, err := parseGPUFlag(tt.gpuFlag, tt.gpuCountFlag, tt.gpuCountChanged)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotType != tt.wantType {
				t.Errorf("type = %q, want %q", gotType, tt.wantType)
			}
			if gotCount != tt.wantCount {
				t.Errorf("count = %d, want %d", gotCount, tt.wantCount)
			}
		})
	}
}

func TestParseMinutesFlag(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		maxMinutes int
		want       int
		wantNil    bool
		wantErr    bool
	}{
		{name: "unset", value: "", maxMinutes: maxRuntimeMaxMinutes, wantNil: true},
		{name: "below minimum", value: "4m", maxMinutes: maxRuntimeMaxMinutes, wantErr: true},
		{name: "minimum", value: "5m", maxMinutes: maxRuntimeMaxMinutes, want: 5},
		{name: "hours and minutes", value: "1h30m", maxMinutes: maxRuntimeMaxMinutes, want: 90},
		{name: "minutes over an hour", value: "90m", maxMinutes: maxRuntimeMaxMinutes, want: 90},
		{name: "max runtime ceiling", value: "168h", maxMinutes: maxRuntimeMaxMinutes, want: 10080},
		{name: "above max runtime ceiling", value: "169h", maxMinutes: maxRuntimeMaxMinutes, wantErr: true},
		{name: "seconds", value: "30s", maxMinutes: maxRuntimeMaxMinutes, wantErr: true},
		{name: "not whole minutes", value: "5m30s", maxMinutes: maxRuntimeMaxMinutes, wantErr: true},
		{name: "days are not a Go duration", value: "2d", maxMinutes: maxRuntimeMaxMinutes, wantErr: true},
		{name: "bare number", value: "30", maxMinutes: maxRuntimeMaxMinutes, wantErr: true},
		{name: "negative", value: "-30m", maxMinutes: maxRuntimeMaxMinutes, wantErr: true},
		{name: "idle ceiling", value: "24h", maxMinutes: idleStopMaxMinutes, want: 1440},
		{name: "above idle ceiling", value: "25h", maxMinutes: idleStopMaxMinutes, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMinutesFlag("max-runtime", tt.value, tt.maxMinutes)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got none", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantNil {
				if got != nil {
					t.Fatalf("got %d, want nil", *got)
				}
				return
			}
			if got == nil || *got != tt.want {
				t.Fatalf("got %v, want %d", got, tt.want)
			}
		})
	}
}

func TestFormatMinutes(t *testing.T) {
	for minutes, want := range map[int]string{5: "5m", 60: "1h", 90: "1h30m", 1440: "24h", 10080: "168h"} {
		if got := formatMinutes(minutes); got != want {
			t.Errorf("formatMinutes(%d) = %q, want %q", minutes, got, want)
		}
	}
}

func TestParseAutoStopFlag(t *testing.T) {
	if v, err := parseAutoStopFlag("max-runtime", "off", maxRuntimeMaxMinutes); err != nil || v != nil {
		t.Errorf("off = %v, %v; want nil, nil", v, err)
	}
	if v, err := parseAutoStopFlag("max-runtime", "OFF", maxRuntimeMaxMinutes); err != nil || v != nil {
		t.Errorf("OFF = %v, %v; want nil, nil", v, err)
	}
	if v, err := parseAutoStopFlag("max-runtime", "8h", maxRuntimeMaxMinutes); err != nil || v == nil || *v != 480 {
		t.Errorf("8h = %v, %v; want 480", v, err)
	}
	if _, err := parseAutoStopFlag("idle-stop", "25h", idleStopMaxMinutes); err == nil {
		t.Error("25h idle stop accepted, want out of range")
	}
	// An explicit empty value must not read as off.
	if _, err := parseAutoStopFlag("max-runtime", "", maxRuntimeMaxMinutes); err == nil {
		t.Error("empty value accepted, want an error")
	}
}

// resetCreateFlags puts the flags of `instances create` back to their defaults after a test sets some.
func resetCreateFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(putCreateFlagsBack)
}

func putCreateFlagsBack() {
	for _, name := range []string{"name", "gpu", "gpu-count", "type-id", "volume", "volume-mode"} {
		f := instancesCreateCmd.Flags().Lookup(name)
		f.Value.Set(f.DefValue)
		f.Changed = false
	}
}

// --gpu-count defaults to 1, and the server refuses a gpu_count beside a type id that is not the type's
// (a CPU type has 0 GPUs, a big type 8): with --type-id a count is sent only when the user gave one.
func TestBuildInstanceRequestGPUCountBesideTypeID(t *testing.T) {
	tests := []struct {
		name  string
		flags map[string]string
		want  int
	}{
		{"a type id alone sends no count", map[string]string{"type-id": "cpu-box"}, 0},
		{"a type id with --gpu-count sends it", map[string]string{"type-id": "h100x8", "gpu-count": "8"}, 8},
		{"a type id with the colon form sends its count", map[string]string{"type-id": "h100x2", "gpu": "H100:2"}, 2},
		{"auto-deploy without a count is one GPU", map[string]string{"gpu": "H100"}, 1},
		{"auto-deploy with the colon form", map[string]string{"gpu": "A100:4"}, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetCreateFlags(t)
			instancesCreateCmd.Flags().Set("name", "box")
			for name, value := range tt.flags {
				if err := instancesCreateCmd.Flags().Set(name, value); err != nil {
					t.Fatalf("set --%s: %v", name, err)
				}
			}
			req, err := buildInstanceRequest(instancesCreateCmd)
			if err != nil {
				t.Fatalf("buildInstanceRequest: %v", err)
			}
			if req.GPUCount != tt.want {
				t.Errorf("GPUCount = %d, want %d", req.GPUCount, tt.want)
			}
			if body, _ := json.Marshal(req); tt.want == 0 && strings.Contains(string(body), "gpu_count") {
				t.Errorf("body %s carries gpu_count", body)
			}
		})
	}
}

// --volume-mode is sent as volume_mode only when the user gave it, in the API's spelling whatever the case.
func TestBuildInstanceRequestVolumeMode(t *testing.T) {
	tests := []struct {
		name     string
		flags    map[string]string
		wantMode string
		wantSent bool
	}{
		{"no flag sends no volume_mode", map[string]string{"volume": "vol-1"}, "", false},
		{"mount_ro", map[string]string{"volume": "vol-1", "volume-mode": "mount_ro"}, "mount_ro", true},
		{"copy", map[string]string{"volume": "vol-1", "volume-mode": "copy"}, "copy", true},
		{"any case", map[string]string{"volume": "vol-1", "volume-mode": "Mount_RO"}, "mount_ro", true},
		{"spaces around it", map[string]string{"volume": "vol-1", "volume-mode": " COPY "}, "copy", true},
		{"an empty value is no flag", map[string]string{"volume": "vol-1", "volume-mode": ""}, "", false},
		{"no volume and no flag", map[string]string{}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetCreateFlags(t)
			instancesCreateCmd.Flags().Set("name", "box")
			instancesCreateCmd.Flags().Set("gpu", "H100")
			for name, value := range tt.flags {
				if err := instancesCreateCmd.Flags().Set(name, value); err != nil {
					t.Fatalf("set --%s: %v", name, err)
				}
			}
			req, err := buildInstanceRequest(instancesCreateCmd)
			if err != nil {
				t.Fatalf("buildInstanceRequest: %v", err)
			}
			if req.VolumeMode != tt.wantMode {
				t.Errorf("VolumeMode = %q, want %q", req.VolumeMode, tt.wantMode)
			}
			body, _ := json.Marshal(req)
			var sent map[string]any
			json.Unmarshal(body, &sent)
			if got, ok := sent["volume_mode"]; ok != tt.wantSent || (ok && got != tt.wantMode) {
				t.Errorf("body %s: volume_mode = %v, sent %v, want %q sent %v", body, got, ok, tt.wantMode, tt.wantSent)
			}
		})
	}
}

func TestBuildInstanceRequestVolumeModeRefusals(t *testing.T) {
	tests := []struct {
		name    string
		flags   map[string]string
		wantErr string
	}{
		{"an unknown mode", map[string]string{"volume": "vol-1", "volume-mode": "rw"}, "copy or mount_ro"},
		{"a mode nobody knows without a volume", map[string]string{"volume-mode": "rw"}, "copy or mount_ro"},
		{"a mode without a volume", map[string]string{"volume-mode": "mount_ro"}, "--volume-mode needs --volume"},
		{"copy without a volume", map[string]string{"volume-mode": "copy"}, "--volume-mode needs --volume"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetCreateFlags(t)
			instancesCreateCmd.Flags().Set("name", "box")
			instancesCreateCmd.Flags().Set("gpu", "H100")
			for name, value := range tt.flags {
				instancesCreateCmd.Flags().Set(name, value)
			}
			_, err := buildInstanceRequest(instancesCreateCmd)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// A refused --volume-mode never reaches the network: the whole command runs against a server that counts
// requests, and a good one sends volume_mode in the create body.
func TestInstancesCreateVolumeModeBeforeAnyCall(t *testing.T) {
	var calls atomic.Int32
	var created map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/instances":
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &created)
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"data":{"id":"inst-1","name":"box","gpu_type":"H100","gpu_count":1}}`))
		default:
			w.Write([]byte(`{"data":[]}`))
		}
	}))
	defer server.Close()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("RUNCRATE_API_KEY", "rc_live_test")
	t.Setenv("RUNCRATE_API_URL", server.URL)

	t.Cleanup(putCreateFlagsBack)
	run := func(flags map[string]string) error {
		putCreateFlagsBack()
		instancesCreateCmd.Flags().Set("name", "box")
		instancesCreateCmd.Flags().Set("gpu", "H100")
		for name, value := range flags {
			instancesCreateCmd.Flags().Set(name, value)
		}
		stdout := os.Stdout
		devnull, _ := os.Open(os.DevNull)
		os.Stdout = devnull
		defer func() { os.Stdout = stdout; devnull.Close() }()
		return instancesCreateCmd.RunE(instancesCreateCmd, nil)
	}

	for _, flags := range []map[string]string{
		{"volume": "vol-1", "volume-mode": "rw"},
		{"volume-mode": "mount_ro"},
	} {
		if err := run(flags); err == nil {
			t.Errorf("flags %v: expected an error", flags)
		}
		if n := calls.Load(); n != 0 {
			t.Fatalf("flags %v: %d requests were made before the refusal", flags, n)
		}
	}

	if err := run(map[string]string{"volume": "vol-1", "volume-mode": "mount_ro"}); err != nil {
		t.Fatalf("create with mount_ro: %v", err)
	}
	if created["storage_id"] != "vol-1" || created["volume_mode"] != "mount_ro" {
		t.Errorf("create body = %v, want storage_id vol-1 and volume_mode mount_ro", created)
	}
}

func TestVolumeLine(t *testing.T) {
	if got, want := volumeLine("vol-1", ""), "  Volume:      vol-1, at /workspace"; got != want {
		t.Errorf("without a mode = %q, want %q", got, want)
	}
	if got, want := volumeLine("vol-1", "mount_ro"), "  Volume:      vol-1, at /workspace (mount_ro)"; got != want {
		t.Errorf("with a mode = %q, want %q", got, want)
	}
}
