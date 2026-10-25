package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
)

// ClusterInterconnect is how a cluster's nodes talk to each other.
type ClusterInterconnect struct {
	Kind     string   `json:"kind"` // infiniband, roce or ethernet
	RateGbps *float64 `json:"rate_gbps,omitempty"`
	Label    string   `json:"label"`
}

// ClusterNodeSpec is one node of an offer.
type ClusterNodeSpec struct {
	VCPUs     int      `json:"vcpus"`
	MemoryGB  float64  `json:"memory_gb"`
	StorageGB float64  `json:"storage_gb"`
	VRAMGB    *float64 `json:"vram_gb,omitempty"`
}

// ClusterOffer is somewhere a cluster of a shape can be created now, from
// GET /api/v1/clusters/offers. HourlyRate is for the whole cluster at NodeCount.
type ClusterOffer struct {
	OfferID           string              `json:"offer_id"`
	GPUType           string              `json:"gpu_type"`
	GPUsPerNode       int                 `json:"gpus_per_node"`
	NodeCount         int                 `json:"node_count"`
	MinNodes          int                 `json:"min_nodes"`
	MaxNodes          int                 `json:"max_nodes"`
	NodesAvailable    *int                `json:"nodes_available"`
	DeploymentType    string              `json:"deployment_type"`
	HostTier          string              `json:"host_tier"`
	Location          string              `json:"location"`
	Interconnect      ClusterInterconnect `json:"interconnect"`
	HourlyRatePerNode float64             `json:"hourly_rate_per_node"`
	HourlyRate        float64             `json:"hourly_rate"`
	Stacks            []string            `json:"stacks"`
	Resize            bool                `json:"resize"`
	BootEstimateSec   *int                `json:"boot_estimate_sec,omitempty"`
	Node              ClusterNodeSpec     `json:"node"`
}

// ClusterOfferFilters are the query of GET /api/v1/clusters/offers; zero values are not sent.
type ClusterOfferFilters struct {
	GPUType          string
	GPUsPerNode      int
	Nodes            int
	Region           string
	Stack            string
	IncludeCommunity bool
}

// CreateClusterRequest is the body of POST /api/v1/clusters.
type CreateClusterRequest struct {
	Name             string   `json:"name"`
	OfferID          string   `json:"offer_id"`
	NodeCount        int      `json:"node_count"`
	Stack            string   `json:"stack"`
	SSHKeyIDs        []string `json:"ssh_key_ids,omitempty"`
	StorageID        string   `json:"storage_id,omitempty"`
	IncludeCommunity bool     `json:"include_community,omitempty"`
}

// Cluster is one row of GET /api/v1/clusters, and the top of the detail.
type Cluster struct {
	ID                string              `json:"id"`
	Name              string              `json:"name"`
	Status            string              `json:"status"`
	Stack             string              `json:"stack"`
	StackStatus       string              `json:"stack_status"`
	StackDetail       *string             `json:"stack_detail"`
	NodeCount         int                 `json:"node_count"`
	GPUType           string              `json:"gpu_type"`
	GPUsPerNode       int                 `json:"gpus_per_node"`
	DeploymentType    string              `json:"deployment_type"`
	HostTier          string              `json:"host_tier"`
	Location          string              `json:"location"`
	Interconnect      ClusterInterconnect `json:"interconnect"`
	HourlyRatePerNode float64             `json:"hourly_rate_per_node"`
	HourlyRate        float64             `json:"hourly_rate"`
	CreatedAt         string              `json:"created_at"`
	UpAt              *string             `json:"up_at"`
	ReadyAt           *string             `json:"ready_at"`
	TerminatedAt      *string             `json:"terminated_at"`
	FailureReason     *string             `json:"failure_reason,omitempty"`
	FailureDetail     *string             `json:"failure_detail,omitempty"`
}

// ClusterNode is one machine of a cluster. InstanceID works with every
// /api/v1/instances/{id} route (ssh-certificate, exec, upload, download).
type ClusterNode struct {
	Rank           int     `json:"rank"`
	Role           string  `json:"role"` // head (rank 0) or worker
	InstanceID     string  `json:"instance_id"`
	Name           string  `json:"name"`
	Status         string  `json:"status"`
	Stage          string  `json:"stage"`
	OverlayAddress *string `json:"overlay_address"`
	GPUs           int     `json:"gpus"`
	IP             *string `json:"ip"`
	SSHCommand     *string `json:"ssh_command"`
}

// ClusterConnection is how to use a cluster: SSH to the head, the hostfile,
// the launcher environment and ready-made commands per stack.
type ClusterConnection struct {
	HeadSSH  *string           `json:"head_ssh"`
	Hostfile string            `json:"hostfile"`
	Env      map[string]string `json:"env"`
	Snippets map[string]string `json:"snippets"`
}

// ClusterShared is the /shared folder every node mounts.
type ClusterShared struct {
	Path      string `json:"path"`
	Available bool   `json:"available"`
	Detail    string `json:"detail,omitempty"`
	Volume    *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"volume,omitempty"`
}

// ClusterEvent is one line of a cluster's history.
type ClusterEvent struct {
	At      string `json:"at"`
	Kind    string `json:"kind"`
	Rank    *int   `json:"rank"`
	Message string `json:"message"`
}

// NCCLSize is the all-reduce result at one message size.
type NCCLSize struct {
	Bytes     int64   `json:"bytes"`
	AlgbwGbps float64 `json:"algbw_gbps"`
	BusbwGbps float64 `json:"busbw_gbps"`
	TimeUs    float64 `json:"time_us"`
}

// ClusterTest is an NCCL all-reduce test across every GPU of a cluster.
type ClusterTest struct {
	ID         string             `json:"id"`
	Kind       string             `json:"kind"`
	Status     string             `json:"status"` // running, passed or failed
	StartedAt  string             `json:"started_at"`
	FinishedAt *string            `json:"finished_at"`
	Result     *ClusterTestResult `json:"result"`
}

// ClusterTestResult is a finished test: bandwidth per message size, or Error.
type ClusterTestResult struct {
	Sizes         []NCCLSize `json:"sizes"`
	PeakBusbwGbps float64    `json:"peak_busbw_gbps"`
	Nodes         int        `json:"nodes"`
	GPUs          int        `json:"gpus"`
	Error         string     `json:"error,omitempty"`
}

// ClusterDetail is GET /api/v1/clusters/{id}.
type ClusterDetail struct {
	Cluster
	Nodes      []ClusterNode     `json:"nodes"`
	Connection ClusterConnection `json:"connection"`
	Shared     ClusterShared     `json:"shared"`
	Events     []ClusterEvent    `json:"events"`
	LastTest   *ClusterTest      `json:"last_test"`
	Billing    struct {
		BilledSince *string `json:"billed_since"`
		NodesBilled int     `json:"nodes_billed"`
	} `json:"billing"`
}

// Kubeconfig is a Kubernetes cluster's kubeconfig and the SSH tunnel it needs:
// its server is https://127.0.0.1:6443.
type Kubeconfig struct {
	YAML   string
	Tunnel string
}

// ListClusters returns the environment's clusters, newest first: active ones,
// or every one with all.
func (c *Client) ListClusters(all bool) ([]Cluster, error) {
	path := "/api/v1/clusters"
	if all {
		path += "?status=all"
	}
	list, err := callJSON[[]Cluster](c, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	return *list, nil
}

// ListClusterOffers returns where a cluster of this shape can be created now.
func (c *Client) ListClusterOffers(f ClusterOfferFilters) ([]ClusterOffer, error) {
	q := url.Values{}
	if f.GPUType != "" {
		q.Set("gpu_type", f.GPUType)
	}
	if f.GPUsPerNode > 0 {
		q.Set("gpus_per_node", strconv.Itoa(f.GPUsPerNode))
	}
	if f.Nodes > 0 {
		q.Set("nodes", strconv.Itoa(f.Nodes))
	}
	if f.Region != "" {
		q.Set("region", f.Region)
	}
	if f.Stack != "" {
		q.Set("stack", f.Stack)
	}
	if f.IncludeCommunity {
		q.Set("include_community", "true")
	}
	path := "/api/v1/clusters/offers"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	list, err := callJSON[[]ClusterOffer](c, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	return *list, nil
}

// CreateCluster creates a cluster, all or nothing. The same idempotencyKey
// answers the cluster it created; "" sends a fresh one. A gone offer is a 409
// OFFER_GONE whose details carry the current offers (see OffersOf).
func (c *Client) CreateCluster(req CreateClusterRequest, idempotencyKey string) (*ClusterDetail, error) {
	if idempotencyKey == "" {
		idempotencyKey = NewIdempotencyKey()
	}
	return callJSONHeaders[ClusterDetail](c, "POST", "/api/v1/clusters", req, map[string]string{"Idempotency-Key": idempotencyKey})
}

// GetCluster returns one cluster with its nodes and how to connect.
func (c *Client) GetCluster(id string) (*ClusterDetail, error) {
	return callJSON[ClusterDetail](c, "GET", clusterPath(id), nil)
}

// DeleteCluster terminates every node of a cluster together.
func (c *Client) DeleteCluster(id string) (*ClusterDetail, error) {
	return callJSON[ClusterDetail](c, "DELETE", clusterPath(id), nil)
}

// StartClusterTest starts an NCCL all-reduce test; a running one is a 409 TEST_RUNNING.
func (c *Client) StartClusterTest(id string) (*ClusterTest, error) {
	return callJSON[ClusterTest](c, "POST", clusterPath(id)+"/tests", nil)
}

// GetClusterTest returns one NCCL test.
func (c *Client) GetClusterTest(id, testID string) (*ClusterTest, error) {
	return callJSON[ClusterTest](c, "GET", clusterPath(id)+"/tests/"+url.PathEscape(testID), nil)
}

// GetClusterKubeconfig returns a Kubernetes cluster's kubeconfig (text/yaml,
// not the JSON envelope) and the tunnel command from X-Runcrate-Tunnel.
func (c *Client) GetClusterKubeconfig(id string) (*Kubeconfig, error) {
	resp, err := c.doCtxHeaders(context.Background(), "GET", clusterPath(id)+"/kubeconfig", nil, map[string]string{"Accept": "text/yaml"})
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()
	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}
	yaml, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading kubeconfig: %w", err)
	}
	return &Kubeconfig{YAML: string(yaml), Tunnel: resp.Header.Get("X-Runcrate-Tunnel")}, nil
}

// OffersOf returns the current offers a 409 OFFER_GONE carries in details.offers.
func OffersOf(apiErr *APIError) []ClusterOffer {
	raw, err := json.Marshal(apiErr.Details["offers"])
	if err != nil {
		return nil
	}
	var offers []ClusterOffer
	if json.Unmarshal(raw, &offers) != nil {
		return nil
	}
	return offers
}

func clusterPath(id string) string {
	return "/api/v1/clusters/" + url.PathEscape(id)
}
