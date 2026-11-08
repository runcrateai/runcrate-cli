package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The body GET /api/v1/storage answers: camelCase keys, the region as its id (the API's StorageVolume).
const storageListBody = `{"data":[{"id":"vol-1","name":"datasets","sizeGb":1000,"status":"active","region":"582",` +
	`"providerType":"wasabi","deploymentId":null,"projectId":"p1","environmentId":null,` +
	`"createdAt":"2026-11-01T10:00:00.000Z","updatedAt":"2026-11-02T10:00:00.000Z"}]}`

func TestListStorageReadsTheServersShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/storage" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(storageListBody))
	}))
	defer server.Close()

	volumes, err := NewClient("rc_live_test", server.URL).ListStorage()
	if err != nil {
		t.Fatalf("ListStorage: %v", err)
	}
	if len(volumes) != 1 {
		t.Fatalf("got %d volumes, want 1", len(volumes))
	}
	v := volumes[0]
	if v.SizeGB != 1000 {
		t.Errorf("SizeGB = %d, want 1000 (the API sends sizeGb)", v.SizeGB)
	}
	if v.Region != "582" || v.Status != "active" || v.CreatedAt != "2026-11-01T10:00:00.000Z" {
		t.Errorf("volume = %+v", v)
	}
}
