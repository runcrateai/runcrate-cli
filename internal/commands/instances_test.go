package commands

import "testing"

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

func TestKindText(t *testing.T) {
	for in, want := range map[string]string{"vm": "VM", "baremetal": "Bare metal", "container": "Container", "": "-", "gpu-pod": "gpu-pod"} {
		if got := kindText(in); got != want {
			t.Errorf("kindText(%q) = %q, want %q", in, got, want)
		}
	}
}
