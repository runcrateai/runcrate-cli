package commands

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/runcrate/cli/internal/api"
)

func TestTunnelForwardsByStack(t *testing.T) {
	// k3s listens on the head's private address only, never its loopback.
	k8s, _, err := tunnelForwards("kubernetes", "10.77.0.1")
	if err != nil || strings.Join(k8s, " ") != "-L 6443:10.77.0.1:6443" {
		t.Errorf("kubernetes = %q, %v", k8s, err)
	}
	ray, _, err := tunnelForwards("ray", "10.77.0.1")
	if err != nil || strings.Join(ray, " ") != "-L 8265:127.0.0.1:8265" {
		t.Errorf("ray = %q, %v", ray, err)
	}
	for _, stack := range []string{"none", "slurm"} {
		if _, _, err := tunnelForwards(stack, "10.77.0.1"); err == nil || !strings.Contains(err.Error(), "runcrate clusters ssh") {
			t.Errorf("%s: err = %v, want a pointer to clusters ssh", stack, err)
		}
	}
}

func TestHeadOverlayAddressPrefersTheHeadsOwn(t *testing.T) {
	head, worker := "10.42.0.5", "10.42.0.6"
	d := &api.ClusterDetail{Nodes: []api.ClusterNode{{Rank: 1, OverlayAddress: &worker}, {Rank: 0, OverlayAddress: &head}}}
	if got := headOverlayAddress(d); got != head {
		t.Errorf("headOverlayAddress = %q, want the head's %q", got, head)
	}
	d = &api.ClusterDetail{Connection: api.ClusterConnection{Env: map[string]string{"MASTER_ADDR": "10.77.0.1"}}}
	if got := headOverlayAddress(d); got != "10.77.0.1" {
		t.Errorf("without a head address = %q, want MASTER_ADDR", got)
	}
}

func TestCheckGPUsPerMachine(t *testing.T) {
	for _, n := range []int{0, 1, 2, 4, 8} {
		if err := checkGPUsPerMachine(n); err != nil {
			t.Errorf("%d: %v", n, err)
		}
	}
	for _, n := range []int{3, 6, 16} {
		if err := checkGPUsPerMachine(n); err == nil {
			t.Errorf("%d GPUs per machine was accepted", n)
		}
	}
}

func TestClusterNodePicksTheRankWithAnAddress(t *testing.T) {
	ip := "203.0.113.7"
	d := &api.ClusterDetail{
		Cluster: api.Cluster{Name: "train", Status: "provisioning"},
		Nodes: []api.ClusterNode{
			{Rank: 0, Role: "head", InstanceID: "dep-0", IP: &ip},
			{Rank: 1, Role: "worker", InstanceID: "dep-1"},
		},
	}
	if n, err := clusterNode(d, 0); err != nil || n.InstanceID != "dep-0" {
		t.Errorf("rank 0 = %+v, %v", n, err)
	}
	if _, err := clusterNode(d, 1); err == nil || !strings.Contains(err.Error(), "no address yet") {
		t.Errorf("rank 1 without an ip: err = %v", err)
	}
	if _, err := clusterNode(d, 5); err == nil || !strings.Contains(err.Error(), "ranks 0 to 1") {
		t.Errorf("rank 5: err = %v", err)
	}
}

func TestResolveClusterByNamePrefersALiveOne(t *testing.T) {
	var gets []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/clusters" && r.URL.Query().Get("status") == "all":
			w.Write([]byte(`{"data":[{"id":"cl-old","name":"train","status":"terminated"},{"id":"cl-new","name":"Train","status":"ready"}]}`))
		case r.URL.Path == "/api/v1/clusters/cl-new":
			gets = append(gets, "cl-new")
			w.Write([]byte(`{"data":{"id":"cl-new","name":"Train","status":"ready","nodes":[]}}`))
		default:
			gets = append(gets, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":{"code":"not_found","message":"Cluster not found"}}`))
		}
	}))
	defer server.Close()
	client := api.NewClient("rc_live_test", server.URL)

	d, err := resolveCluster(client, "train")
	if err != nil || d.ID != "cl-new" {
		t.Fatalf("resolveCluster = %+v, %v", d, err)
	}
	if _, err := resolveCluster(client, "nope"); err == nil || !strings.Contains(err.Error(), `"nope" not found`) {
		t.Errorf("unknown name: err = %v", err)
	}
}

func TestResolveClusterStopsWhenClustersAreOff(t *testing.T) {
	lists := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/clusters" {
			lists++
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"FEATURE_OFF","message":"Self-serve clusters are not on for this workspace."}}`))
	}))
	defer server.Close()

	_, err := resolveCluster(api.NewClient("rc_live_test", server.URL), "train")
	if err == nil || !strings.Contains(err.Error(), "not on for this workspace") || lists != 0 {
		t.Errorf("err = %v, lists = %d; want the FEATURE_OFF message and no list call", err, lists)
	}
}

func TestRenderOfferTableShowsRatesAndCommunity(t *testing.T) {
	free := 3
	offers := []api.ClusterOffer{
		{OfferID: "off_1", GPUType: "H100", GPUsPerNode: 8, NodeCount: 4, MinNodes: 2, MaxNodes: 8, DeploymentType: "vm",
			HostTier: "datacenter", Location: "Dallas, TX, United States",
			Interconnect:      api.ClusterInterconnect{Kind: "ethernet", Label: "Ethernet", Note: "No fast link between nodes."},
			HourlyRatePerNode: 20, HourlyRate: 80, Stacks: []string{"none", "kubernetes"}},
		{OfferID: "off_3", GPUType: "H100", GPUsPerNode: 4, NodeCount: 4, MinNodes: 2, MaxNodes: 8, AllowedNodeCounts: []int{2, 4, 8},
			DeploymentType: "vm", HostTier: "datacenter", Location: "Dallas, TX, United States",
			Interconnect:      api.ClusterInterconnect{Kind: "ethernet", Label: "Ethernet", Note: "No fast link between nodes."},
			HourlyRatePerNode: 10, HourlyRate: 40, Stacks: []string{"none"}},
		{OfferID: "off_2", GPUType: "H100", GPUsPerNode: 8, NodeCount: 4, MinNodes: 2, MaxNodes: 4, NodesAvailable: &free,
			DeploymentType: "container", HostTier: "community", Location: "Oslo, Norway", Interconnect: api.ClusterInterconnect{Label: "InfiniBand 400 Gb/s"},
			HourlyRatePerNode: 18.5, HourlyRate: 74, Stacks: []string{"none", "ray"}},
	}
	var buf bytes.Buffer
	if err := renderOfferTable(&buf, offers); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"off_1", "8x H100", "$20.00", "$80.00 (4 machines)", "None, Kubernetes", "2-8",
		"container, community", "2-4 (3 free)", "$74.00 (4 machines)", "4x H100", "2, 4, 8"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "Ethernet: No fast link between nodes.") != 1 {
		t.Errorf("the ethernet note should show once under the table:\n%s", out)
	}
}

func TestRenderTestResult(t *testing.T) {
	passed := &api.ClusterTest{ID: "t-1", Status: "passed"}
	passed.Result = &api.ClusterTestResult{
		Sizes:        []api.NCCLSize{{Bytes: 1 << 30, AlgbwGBs: 40.1, BusbwGBs: 75.2, TimeUs: 26800}},
		PeakBusbwGBs: 75.2, Nodes: 2, GPUs: 16,
	}
	var buf bytes.Buffer
	if err := renderTestResult(&buf, passed); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"passed across 16 GPUs on 2 nodes", "peak bus bandwidth 75.2 GB/s", "1.00 GB", "75.20"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, buf.String())
		}
	}

	passed.Result.Error = "NCCL timed out"
	buf.Reset()
	renderTestResult(&buf, passed)
	if !strings.Contains(buf.String(), "failed: NCCL timed out") {
		t.Errorf("error result = %q", buf.String())
	}
}
