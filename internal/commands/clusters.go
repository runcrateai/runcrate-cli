package commands

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/runcrate/cli/internal/api"
	"github.com/spf13/cobra"
)

// ── display ─────────────────────────────────────────────────────────────────

var stackNames = map[string]string{"none": "None", "slurm": "Slurm", "kubernetes": "Kubernetes", "ray": "Ray"}

func stackName(stack string) string {
	if name, ok := stackNames[stack]; ok {
		return name
	}
	return stack
}

// clusterShape reads like the dashboard: "8x H100 x 4 nodes".
func clusterShape(gpusPerNode int, gpuType string, nodes int) string {
	return fmt.Sprintf("%dx %s x %d nodes", gpusPerNode, gpuType, nodes)
}

// sinceISO is the time from an ISO instant to now, or "-".
func sinceISO(iso string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return "-"
	}
	return formatShortDuration(now.Sub(t))
}

func renderClusterTable(w io.Writer, list []api.Cluster, now time.Time) error {
	tw := newTableTo(w)
	fmt.Fprintf(tw, "ID\tNAME\tSTATUS\tGPUS\tINTERCONNECT\tSOFTWARE\t$/HR\tAGE\n")
	for _, c := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t$%.2f\t%s\n",
			c.ID, truncate(c.Name, 24), statusColor(c.Status), clusterShape(c.GPUsPerNode, c.GPUType, c.NodeCount),
			c.Interconnect.Label, stackName(c.Stack), c.HourlyRate, sinceISO(c.CreatedAt, now))
	}
	return tw.Flush()
}

// nodesText is the node range of an offer, and how many can start now when known.
func nodesText(o *api.ClusterOffer) string {
	text := fmt.Sprintf("%d-%d", o.MinNodes, o.MaxNodes)
	if o.NodesAvailable != nil {
		text += fmt.Sprintf(" (%d free)", *o.NodesAvailable)
	}
	return text
}

func renderOfferTable(w io.Writer, offers []api.ClusterOffer) error {
	tw := newTableTo(w)
	fmt.Fprintf(tw, "OFFER\tGPUS/NODE\tLOCATION\tINTERCONNECT\tKIND\tNODES\tSOFTWARE\t$/HR/NODE\t$/HR\n")
	for i := range offers {
		o := &offers[i]
		kind := o.DeploymentType
		if o.HostTier == "community" {
			kind += ", community"
		}
		software := make([]string, 0, len(o.Stacks))
		for _, s := range o.Stacks {
			software = append(software, stackName(s))
		}
		fmt.Fprintf(tw, "%s\t%dx %s\t%s\t%s\t%s\t%s\t%s\t$%.2f\t$%.2f (%d nodes)\n",
			o.OfferID, o.GPUsPerNode, o.GPUType, o.Location, o.Interconnect.Label, kind, nodesText(o),
			strings.Join(software, ", "), o.HourlyRatePerNode, o.HourlyRate, o.NodeCount)
	}
	return tw.Flush()
}

func orDash(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

// renderTestResult writes an NCCL test's outcome and its bandwidth per size.
func renderTestResult(w io.Writer, t *api.ClusterTest) error {
	switch {
	case t.Status == "running":
		fmt.Fprintf(w, "NCCL all-reduce test %s is running (started %s).\n", t.ID, t.StartedAt)
		return nil
	case t.Result == nil:
		fmt.Fprintf(w, "NCCL all-reduce test %s %s with no result.\n", t.ID, t.Status)
		return nil
	case t.Result.Error != "":
		fmt.Fprintf(w, "NCCL all-reduce test %s failed: %s\n", t.ID, t.Result.Error)
		return nil
	}
	r := t.Result
	fmt.Fprintf(w, "NCCL all-reduce test %s across %d GPUs on %d nodes: peak bus bandwidth %.1f GB/s.\n",
		t.Status, r.GPUs, r.Nodes, r.PeakBusbwGBs)
	tw := newTableTo(w)
	fmt.Fprintf(tw, "SIZE\tTIME (us)\tALGBW (GB/s)\tBUSBW (GB/s)\n")
	for _, s := range r.Sizes {
		fmt.Fprintf(tw, "%s\t%.1f\t%.2f\t%.2f\n", formatBytes(s.Bytes), s.TimeUs, s.AlgbwGBs, s.BusbwGBs)
	}
	return tw.Flush()
}

// printClusterDetails writes a cluster's fields, nodes, how to connect, its
// last test and its latest events.
func printClusterDetails(w io.Writer, d *api.ClusterDetail, now time.Time) error {
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  Name:          %s\n", d.Name)
	fmt.Fprintf(w, "  ID:            %s\n", d.ID)
	fmt.Fprintf(w, "  Status:        %s\n", statusColor(d.Status))
	software := stackName(d.Stack)
	if d.Stack != "none" {
		software += " (" + d.StackStatus + ")"
	}
	fmt.Fprintf(w, "  Software:      %s\n", software)
	if d.StackDetail != nil && *d.StackDetail != "" {
		fmt.Fprintf(w, "  Install:       %s\n", *d.StackDetail)
	}
	fmt.Fprintf(w, "  GPUs:          %s\n", clusterShape(d.GPUsPerNode, d.GPUType, d.NodeCount))
	fmt.Fprintf(w, "  Location:      %s\n", d.Location)
	fmt.Fprintf(w, "  Interconnect:  %s\n", d.Interconnect.Label)
	fmt.Fprintf(w, "  Price:         $%.2f/hr for the cluster ($%.2f/hr per node)\n", d.HourlyRate, d.HourlyRatePerNode)
	if d.Billing.BilledSince != nil {
		fmt.Fprintf(w, "  Billed since:  %s (%s, %d nodes)\n", *d.Billing.BilledSince, sinceISO(*d.Billing.BilledSince, now), d.Billing.NodesBilled)
	} else if d.Status == "provisioning" {
		fmt.Fprintf(w, "  Billed since:  not yet: billing starts when every node is up\n")
	}
	switch {
	case !d.Shared.Available:
		fmt.Fprintf(w, "  Shared:        not available: %s\n", d.Shared.Detail)
	case d.Shared.Volume != nil:
		fmt.Fprintf(w, "  Shared:        %s, volume %s at %s\n", d.Shared.Path, d.Shared.Volume.Name, d.Shared.Volume.Path)
	default:
		fmt.Fprintf(w, "  Shared:        %s on every node\n", d.Shared.Path)
	}
	if d.FailureDetail != nil && *d.FailureDetail != "" {
		fmt.Fprintf(w, "  Failure:       %s\n", *d.FailureDetail)
	}

	if len(d.Nodes) > 0 {
		fmt.Fprintln(w)
		tw := newTableTo(w)
		fmt.Fprintf(tw, "  RANK\tROLE\tSTATUS\tSTAGE\tGPUS\tOVERLAY\tIP\n")
		for _, n := range d.Nodes {
			fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\t%d\t%s\t%s\n",
				n.Rank, n.Role, statusColor(n.Status), n.Stage, n.GPUs, orDash(n.OverlayAddress), orDash(n.IP))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	c := d.Connection
	if c.HeadSSH != nil {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  Connect:\n")
		fmt.Fprintf(w, "    runcrate clusters ssh %s              # the head; --node N for another\n", d.Name)
		if d.Stack == "kubernetes" {
			fmt.Fprintf(w, "    runcrate clusters kubeconfig %s -o kubeconfig.yaml\n", d.Name)
		}
		if d.Stack == "kubernetes" || d.Stack == "ray" {
			fmt.Fprintf(w, "    runcrate clusters tunnel %s\n", d.Name)
		}
		keys := make([]string, 0, len(c.Env))
		for k := range c.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "    %s=%s\n", k, c.Env[k])
		}
		for _, name := range []string{"torchrun", "mpirun", "deepspeed", "sbatch"} {
			if snippet := c.Snippets[name]; snippet != "" {
				fmt.Fprintf(w, "\n  %s:\n    %s\n", name, strings.ReplaceAll(strings.TrimSpace(snippet), "\n", "\n    "))
			}
		}
	}

	if d.LastTest != nil {
		fmt.Fprintln(w)
		if err := renderTestResult(w, d.LastTest); err != nil {
			return err
		}
	}

	if len(d.Events) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  Recent events:\n")
		for i, e := range d.Events {
			if i == 5 {
				break
			}
			fmt.Fprintf(w, "    %s  %s\n", e.At, e.Message)
		}
	}
	fmt.Fprintln(w)
	return nil
}

// ── resolving ───────────────────────────────────────────────────────────────

// resolveCluster finds a cluster by ID, else by name (an active one first).
func resolveCluster(client *api.Client, ref string) (*api.ClusterDetail, error) {
	d, err := client.GetCluster(ref)
	var apiErr *api.APIError
	if err == nil || !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound || apiErr.Code == "FEATURE_OFF" {
		return d, err
	}
	list, listErr := client.ListClusters(true)
	if listErr != nil {
		return nil, fmt.Errorf("fetching clusters: %w", listErr)
	}
	ended := map[string]bool{"terminated": true, "failed": true}
	var match *api.Cluster
	for i := range list {
		c := &list[i]
		if !strings.EqualFold(c.Name, ref) {
			continue
		}
		// Prefer a live cluster over an ended one of the same name.
		if match == nil || (ended[match.Status] && !ended[c.Status]) {
			match = c
		}
	}
	if match == nil {
		return nil, fmt.Errorf("cluster %q not found", ref)
	}
	return client.GetCluster(match.ID)
}

// clusterNode is the node of rank, with an address to connect to.
func clusterNode(d *api.ClusterDetail, rank int) (*api.ClusterNode, error) {
	for i := range d.Nodes {
		n := &d.Nodes[i]
		if n.Rank != rank {
			continue
		}
		if n.IP == nil || *n.IP == "" {
			return nil, fmt.Errorf("node %d of %s has no address yet (the cluster is %s)", rank, d.Name, d.Status)
		}
		return n, nil
	}
	return nil, fmt.Errorf("%s has no node %d (ranks 0 to %d)", d.Name, rank, len(d.Nodes)-1)
}

// tunnelForwards are the ssh -L forwards from this computer to the head for a
// stack: the Kubernetes API on 6443 and the Ray dashboard on 8265.
func tunnelForwards(stack string) ([]string, string, error) {
	switch stack {
	case "kubernetes":
		return []string{"-L", "6443:127.0.0.1:6443"}, "the Kubernetes API on https://127.0.0.1:6443", nil
	case "ray":
		return []string{"-L", "8265:127.0.0.1:8265"}, "the Ray dashboard on http://127.0.0.1:8265", nil
	}
	return nil, "", fmt.Errorf("a %s cluster has no API or dashboard to tunnel; connect with: runcrate clusters ssh", stackName(stack))
}

// ── commands ────────────────────────────────────────────────────────────────

var clustersCmd = &cobra.Command{
	Use:     "clusters",
	Aliases: []string{"cluster"},
	Short:   "Create and use multi-node GPU clusters with Slurm, Kubernetes or Ray",
	Long: `A cluster is several GPU machines on one private network, with SSH between
them, /shared on every node, and optionally Slurm, Kubernetes or Ray.

It is all or nothing: every node starts or none does, and a cluster that
could not start costs nothing. Billing starts when every node is up and runs
until you delete the cluster, which stops every node together.

  runcrate clusters offers --gpu H100:8 --nodes 4
  runcrate clusters create --name train --offer <offer> --nodes 4 --stack slurm
  runcrate clusters get train
  runcrate clusters ssh train -- sinfo
  runcrate clusters test train
  runcrate clusters delete train`,
}

var clustersListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List clusters, newest first",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		all, _ := cmd.Flags().GetBool("all")
		list, err := client.ListClusters(all)
		if err != nil {
			return fmt.Errorf("fetching clusters: %w", err)
		}
		if jsonOutput {
			return printJSON(list)
		}
		if quiet, _ := cmd.Flags().GetBool("quiet"); quiet {
			for _, c := range list {
				fmt.Println(c.ID)
			}
			return nil
		}
		if len(list) == 0 {
			fmt.Println("No clusters. Find where one can start with: runcrate clusters offers --gpu H100 --nodes 2")
			return nil
		}
		return renderClusterTable(os.Stdout, list, time.Now())
	},
}

var clustersOffersCmd = &cobra.Command{
	Use:   "offers",
	Short: "Where a cluster can be created now, with its price per hour",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		gpuRaw, _ := cmd.Flags().GetString("gpu")
		perNodeRaw, _ := cmd.Flags().GetInt("gpu-count")
		gpuType, perNode, err := parseGPUFlag(gpuRaw, perNodeRaw, cmd.Flags().Changed("gpu-count"))
		if err != nil {
			return err
		}
		f := api.ClusterOfferFilters{GPUType: gpuType, GPUsPerNode: perNode}
		f.Nodes, _ = cmd.Flags().GetInt("nodes")
		f.Region, _ = cmd.Flags().GetString("region")
		f.Stack, _ = cmd.Flags().GetString("stack")
		f.IncludeCommunity, _ = cmd.Flags().GetBool("include-community")

		offers, err := client.ListClusterOffers(f)
		if err != nil {
			return fmt.Errorf("fetching offers: %w", err)
		}
		if jsonOutput {
			return printJSON(offers)
		}
		if len(offers) == 0 {
			fmt.Println("No cluster offers match right now. Try another GPU, fewer nodes, or any region.")
			return nil
		}
		if err := renderOfferTable(os.Stdout, offers); err != nil {
			return err
		}
		fmt.Printf("\nCreate one with: runcrate clusters create --name <name> --offer <offer> --nodes %d --stack none|slurm|kubernetes|ray\n", offers[0].NodeCount)
		return nil
	},
}

var clustersCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a cluster from an offer (all or nothing; billed from when every node is up)",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		req := api.CreateClusterRequest{}
		req.Name, _ = cmd.Flags().GetString("name")
		req.OfferID, _ = cmd.Flags().GetString("offer")
		req.NodeCount, _ = cmd.Flags().GetInt("nodes")
		req.Stack, _ = cmd.Flags().GetString("stack")
		req.SSHKeyIDs, _ = cmd.Flags().GetStringSlice("ssh-key")
		req.StorageID, _ = cmd.Flags().GetString("volume")
		req.IncludeCommunity, _ = cmd.Flags().GetBool("include-community")
		if _, ok := stackNames[req.Stack]; !ok {
			return fmt.Errorf("--stack must be none, slurm, kubernetes or ray, not %q", req.Stack)
		}
		if req.NodeCount < 2 || req.NodeCount > 64 {
			return fmt.Errorf("--nodes must be 2 to 64")
		}

		d, err := client.CreateCluster(req, "")
		if api.IsBudgetReached(err) {
			return errors.New(budgetReachedMessage)
		}
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.Code == "OFFER_GONE" {
			fmt.Fprintln(os.Stderr, "That offer is no longer available, so nothing was created. Current offers:")
			fmt.Fprintln(os.Stderr)
			if err := renderOfferTable(os.Stderr, api.OffersOf(apiErr)); err != nil {
				return err
			}
			return errors.New("pick another offer and run create again")
		}
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusServiceUnavailable {
			return fmt.Errorf("%s Nothing was created or billed. Try another offer, or again in a minute", apiErr.Message)
		}
		if err != nil {
			return fmt.Errorf("creating cluster: %w", err)
		}
		if jsonOutput {
			return printJSON(d)
		}
		fmt.Printf("Creating cluster %s (%s): %s, %s, %s.\n", d.Name, d.ID,
			clusterShape(d.GPUsPerNode, d.GPUType, d.NodeCount), d.Location, stackName(d.Stack))
		fmt.Printf("Every node starts or none does. Billing starts when every node is up, at $%.2f/hr for the cluster.\n", d.HourlyRate)
		fmt.Printf("Follow it with: runcrate clusters get %s\n", d.Name)
		return nil
	},
}

var clustersGetCmd = &cobra.Command{
	Use:     "get <name-or-id>",
	Aliases: []string{"info", "status"},
	Short:   "Show a cluster: nodes, how to connect, last test and events",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		d, err := resolveCluster(client, args[0])
		if err != nil {
			return fmt.Errorf("fetching cluster: %w", err)
		}
		if jsonOutput {
			return printJSON(d)
		}
		return printClusterDetails(os.Stdout, d, time.Now())
	},
}

var clustersDeleteCmd = &cobra.Command{
	Use:     "delete <name-or-id>",
	Aliases: []string{"rm", "terminate"},
	Short:   "Terminate every node of a cluster and stop its billing",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		d, err := resolveCluster(client, args[0])
		if err != nil {
			return fmt.Errorf("fetching cluster: %w", err)
		}
		if yes, _ := cmd.Flags().GetBool("yes"); !yes {
			ok, err := confirm(os.Stdin, os.Stderr, fmt.Sprintf(
				"Terminate cluster %s and all %d nodes? Files on the nodes and in /shared are deleted (a volume keeps /shared/volume). [y/N] ",
				d.Name, d.NodeCount))
			if err != nil {
				return errors.New("no answer to the prompt; pass --yes to terminate without asking")
			}
			if !ok {
				fmt.Println("Kept the cluster.")
				return nil
			}
		}
		res, err := client.DeleteCluster(d.ID)
		if err != nil {
			return fmt.Errorf("terminating cluster: %w", err)
		}
		if jsonOutput {
			return printJSON(res)
		}
		fmt.Printf("Terminating cluster %s: every node stops and billing ends.\n", d.Name)
		return nil
	},
}

var clustersSSHCmd = &cobra.Command{
	Use:   "ssh <name-or-id> [-- command]",
	Short: "SSH into the head of a cluster, or another node with --node",
	Long: `Opens SSH to the head (rank 0) of a cluster, or to the node of --node.
Uses ephemeral SSH certificates, like runcrate ssh.

  runcrate clusters ssh train                      # shell on the head
  runcrate clusters ssh train --node 2             # shell on node 2
  runcrate clusters ssh train -- sinfo             # run a command on the head
  runcrate clusters ssh train -- cat /shared/hostfile`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		d, err := resolveCluster(client, args[0])
		if err != nil {
			return fmt.Errorf("fetching cluster: %w", err)
		}
		rank, _ := cmd.Flags().GetInt("node")
		node, err := clusterNode(d, rank)
		if err != nil {
			return err
		}
		var remote []string
		if dashIdx := cmd.ArgsLenAtDash(); dashIdx >= 0 && dashIdx < len(args) {
			remote = args[dashIdx:]
		}
		return execSSH(client, node.InstanceID, *node.IP, nil, remote)
	},
}

var clustersKubeconfigCmd = &cobra.Command{
	Use:   "kubeconfig <name-or-id>",
	Short: "Print or save the kubeconfig of a Kubernetes cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		d, err := resolveCluster(client, args[0])
		if err != nil {
			return fmt.Errorf("fetching cluster: %w", err)
		}
		kc, err := client.GetClusterKubeconfig(d.ID)
		if err != nil {
			return fmt.Errorf("fetching kubeconfig: %w", err)
		}
		out, _ := cmd.Flags().GetString("output")
		if out == "" {
			fmt.Print(kc.YAML)
		} else {
			if err := os.WriteFile(out, []byte(kc.YAML), 0o600); err != nil {
				return fmt.Errorf("saving kubeconfig: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Saved the kubeconfig to %s. It grants admin on the cluster: keep it private.\n", out)
		}
		fmt.Fprintf(os.Stderr, "Its server is https://127.0.0.1:6443. In another terminal run: runcrate clusters tunnel %s\n", d.Name)
		if out != "" {
			fmt.Fprintf(os.Stderr, "Then: export KUBECONFIG=%s && kubectl get nodes\n", out)
		}
		return nil
	},
}

var clustersTunnelCmd = &cobra.Command{
	Use:   "tunnel <name-or-id>",
	Short: "Forward the Kubernetes API (6443) or Ray dashboard (8265) from the head to this computer",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		d, err := resolveCluster(client, args[0])
		if err != nil {
			return fmt.Errorf("fetching cluster: %w", err)
		}
		forwards, what, err := tunnelForwards(d.Stack)
		if err != nil {
			return err
		}
		head, err := clusterNode(d, 0)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Forwarding %s from the head of %s. Press Ctrl-C to stop.\n", what, d.Name)
		options := append([]string{"-N", "-o", "ExitOnForwardFailure=yes"}, forwards...)
		return execSSH(client, head.InstanceID, *head.IP, options, nil)
	},
}

// clusterTestPoll is how often test waits between reads of a running test.
const clusterTestPoll = 5 * time.Second

// clusterTestDeadline is how long test waits: the test itself stops at 10 minutes.
const clusterTestDeadline = 12 * time.Minute

var clustersTestCmd = &cobra.Command{
	Use:   "test <name-or-id>",
	Short: "Run an NCCL all-reduce test across every GPU and show its bandwidth",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		d, err := resolveCluster(client, args[0])
		if err != nil {
			return fmt.Errorf("fetching cluster: %w", err)
		}
		t, err := client.StartClusterTest(d.ID)
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.Code == "TEST_RUNNING" && d.LastTest != nil {
			fmt.Fprintf(os.Stderr, "A test is already running on %s; waiting for it.\n", d.Name)
			t, err = d.LastTest, nil
		}
		if err != nil {
			return fmt.Errorf("starting test: %w", err)
		}

		if detach, _ := cmd.Flags().GetBool("detach"); !detach {
			if !jsonOutput {
				fmt.Fprintf(os.Stderr, "Testing all-reduce across every GPU of %s; this takes a few minutes.\n", d.Name)
			}
			deadline := time.Now().Add(clusterTestDeadline)
			for t.Status == "running" && time.Now().Before(deadline) {
				time.Sleep(clusterTestPoll)
				if t, err = client.GetClusterTest(d.ID, t.ID); err != nil {
					return fmt.Errorf("reading test: %w", err)
				}
			}
		}
		if jsonOutput {
			return printJSON(t)
		}
		if err := renderTestResult(os.Stdout, t); err != nil {
			return err
		}
		if t.Status == "running" {
			fmt.Printf("Read it later with: runcrate clusters get %s\n", d.Name)
		}
		return nil
	},
}

func init() {
	clustersCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output as JSON")

	clustersListCmd.Flags().Bool("all", false, "Include terminated and failed clusters")
	clustersListCmd.Flags().BoolP("quiet", "q", false, "Only show cluster IDs")

	clustersOffersCmd.Flags().String("gpu", "", "GPU type, optionally with GPUs per node (e.g. H100 or H100:8)")
	clustersOffersCmd.Flags().Int("gpu-count", 0, "GPUs on each node")
	clustersOffersCmd.Flags().Int("nodes", 2, "Number of nodes the price is for (2 to 64)")
	clustersOffersCmd.Flags().String("region", "", "Only offers in this region, city or country")
	clustersOffersCmd.Flags().String("stack", "", "Only offers that can run none, slurm, kubernetes or ray")
	clustersOffersCmd.Flags().Bool("include-community", false, "Also show community hosts (datacenter only by default)")

	clustersCreateCmd.Flags().String("name", "", "Cluster name; nodes are named <name>-<rank> (required)")
	clustersCreateCmd.Flags().String("offer", "", "Offer ID from 'runcrate clusters offers' (required)")
	clustersCreateCmd.Flags().Int("nodes", 0, "Number of nodes, 2 to 64, within the offer's range (required)")
	clustersCreateCmd.Flags().String("stack", "none", "Software across the nodes: none, slurm, kubernetes or ray")
	clustersCreateCmd.Flags().StringSlice("ssh-key", nil, "SSH key ID from 'runcrate ssh-keys list' to add on every node (repeatable)")
	clustersCreateCmd.Flags().String("volume", "", "Volume ID synced into /shared/volume on the head")
	clustersCreateCmd.Flags().Bool("include-community", false, "Needed when the offer is on community hosts")
	for _, name := range []string{"name", "offer", "nodes"} {
		_ = clustersCreateCmd.MarkFlagRequired(name)
	}

	clustersDeleteCmd.Flags().BoolP("yes", "y", false, "Terminate without asking")
	clustersSSHCmd.Flags().Int("node", 0, "Rank of the node to connect to (0 is the head)")
	clustersKubeconfigCmd.Flags().StringP("output", "o", "", "Save to this file (mode 600) instead of printing")
	clustersTestCmd.Flags().BoolP("detach", "d", false, "Start the test and return without waiting for the result")

	clustersCmd.AddCommand(clustersListCmd, clustersOffersCmd, clustersCreateCmd, clustersGetCmd, clustersDeleteCmd,
		clustersSSHCmd, clustersKubeconfigCmd, clustersTunnelCmd, clustersTestCmd)
	rootCmd.AddCommand(clustersCmd)
}
