package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testOffer = `{"offer_id":"off_1","gpu_type":"H100","gpus_per_node":8,"node_count":2,"min_nodes":2,"max_nodes":8,` +
	`"nodes_available":null,"deployment_type":"vm","host_tier":"datacenter","location":"Dallas, TX, United States",` +
	`"interconnect":{"kind":"ethernet","label":"Ethernet, private overlay"},"hourly_rate_per_node":20,"hourly_rate":40,` +
	`"stacks":["none","slurm"],"resize":true,"node":{"vcpus":96,"memory_gb":1024,"storage_gb":2000}}`

func TestCreateClusterSendsBodyAndIdempotencyKey(t *testing.T) {
	var keys []string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/clusters" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"data":{"id":"cl-1","name":"train","status":"provisioning","stack":"slurm","node_count":2,` +
			`"hourly_rate":40,"nodes":[{"rank":0,"role":"head","instance_id":"dep-0","ip":null}]}}`))
	}))
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	req := CreateClusterRequest{Name: "train", OfferID: "off_1", NodeCount: 2, Stack: "slurm"}
	d, err := client.CreateCluster(req, "")
	if err != nil {
		t.Fatalf("CreateCluster() error: %v", err)
	}
	if d.ID != "cl-1" || d.Status != "provisioning" || len(d.Nodes) != 1 || d.Nodes[0].InstanceID != "dep-0" || d.Nodes[0].IP != nil {
		t.Errorf("cluster = %+v", d)
	}
	if body["name"] != "train" || body["offer_id"] != "off_1" || body["node_count"] != float64(2) || body["stack"] != "slurm" {
		t.Errorf("body = %v", body)
	}
	for _, unset := range []string{"ssh_key_ids", "storage_id", "include_community"} {
		if _, ok := body[unset]; ok {
			t.Errorf("unset %q was sent: %v", unset, body[unset])
		}
	}

	if _, err := client.CreateCluster(req, "my-key"); err != nil {
		t.Fatal(err)
	}
	if len(keys[0]) != 36 || keys[1] != "my-key" {
		t.Errorf("Idempotency-Keys = %q, want a fresh UUID then the given key", keys)
	}
}

func TestCreateClusterOfferGoneCarriesTheCurrentOffers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":{"code":"OFFER_GONE","message":"That offer is no longer available.","details":{"offers":[` + testOffer + `]}}}`))
	}))
	defer server.Close()

	_, err := NewClient("rc_live_test", server.URL).CreateCluster(CreateClusterRequest{Name: "a", OfferID: "old", NodeCount: 2, Stack: "none"}, "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Code != "OFFER_GONE" {
		t.Fatalf("err = %v, want a 409 OFFER_GONE", err)
	}
	offers := OffersOf(apiErr)
	if len(offers) != 1 || offers[0].OfferID != "off_1" || offers[0].HourlyRate != 40 || offers[0].NodesAvailable != nil {
		t.Errorf("offers = %+v", offers)
	}
}

func TestListClusterOffersSendsOnlyTheFiltersGiven(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Path+"?"+r.URL.RawQuery)
		w.Write([]byte(`{"data":[` + testOffer + `]}`))
	}))
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	offers, err := client.ListClusterOffers(ClusterOfferFilters{GPUType: "H100", GPUsPerNode: 8, Nodes: 4, IncludeCommunity: true, Volume: true})
	if err != nil || len(offers) != 1 || offers[0].Interconnect.Kind != "ethernet" {
		t.Fatalf("offers = %+v, err = %v", offers, err)
	}
	if _, err := client.ListClusterOffers(ClusterOfferFilters{}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/api/v1/clusters/offers?gpu_type=H100&gpus_per_node=8&include_community=true&nodes=4&volume=true",
		"/api/v1/clusters/offers?",
	}
	if len(queries) != 2 || queries[0] != want[0] || queries[1] != want[1] {
		t.Errorf("requests = %q, want %q", queries, want)
	}
}

func TestGetClusterKubeconfigReadsYAMLAndTunnel(t *testing.T) {
	const yaml = "apiVersion: v1\nclusters:\n- cluster:\n    server: https://127.0.0.1:6443\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/clusters/k8s/kubeconfig":
			w.Header().Set("Content-Type", "text/yaml")
			w.Header().Set("X-Runcrate-Tunnel", "ssh -N -L 6443:127.0.0.1:6443 root@203.0.113.5")
			w.Write([]byte(yaml))
		default:
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"error":{"code":"conflict","message":"This cluster does not run Kubernetes."}}`))
		}
	}))
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	kc, err := client.GetClusterKubeconfig("k8s")
	if err != nil {
		t.Fatalf("GetClusterKubeconfig() error: %v", err)
	}
	if kc.YAML != yaml || kc.Tunnel != "ssh -N -L 6443:127.0.0.1:6443 root@203.0.113.5" {
		t.Errorf("kubeconfig = %+v", kc)
	}

	_, err = client.GetClusterKubeconfig("slurm")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Message != "This cluster does not run Kubernetes." {
		t.Errorf("err = %v, want the 409 message", err)
	}
}
