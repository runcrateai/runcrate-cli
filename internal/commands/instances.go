package commands

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/runcrate/cli/internal/api"
	"github.com/spf13/cobra"
)

// Auto stop limits accepted by the API, in minutes.
const (
	minAutoStopMinutes   = 5
	maxRuntimeMaxMinutes = 7 * 24 * 60 // 168h
	idleStopMaxMinutes   = 24 * 60     // 24h
)

const budgetReachedMessage = "This environment has reached its monthly budget. " +
	"An owner or admin can raise it in the dashboard."

// parseMinutesFlag parses a duration flag such as "30m", "4h" or "1h30m" into
// whole minutes within [minAutoStopMinutes, maxMinutes]. An empty value means the
// flag is unset and returns nil.
func parseMinutesFlag(flag, value string, maxMinutes int) (*int, error) {
	if value == "" {
		return nil, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return nil, fmt.Errorf("--%s %q is not a duration. Use a value like 30m, 4h or 1h30m", flag, value)
	}
	if d%time.Minute != 0 {
		return nil, fmt.Errorf("--%s %q must be a whole number of minutes, like 30m or 1h30m", flag, value)
	}
	minutes := int(d / time.Minute)
	if minutes < minAutoStopMinutes || minutes > maxMinutes {
		return nil, fmt.Errorf("--%s must be between %s and %s, got %s",
			flag, formatMinutes(minAutoStopMinutes), formatMinutes(maxMinutes), value)
	}
	return &minutes, nil
}

// parseAutoStopFlag parses an auto-stop change: "off" turns the limit off (nil),
// anything else is a duration as for create.
func parseAutoStopFlag(flag, value string, maxMinutes int) (*int, error) {
	if strings.EqualFold(value, "off") {
		return nil, nil
	}
	if value == "" {
		return nil, fmt.Errorf("--%s needs a duration like 30m or 4h, or off", flag)
	}
	return parseMinutesFlag(flag, value, maxMinutes)
}

// formatMinutes renders minutes the way the flags accept them: 30m, 4h, 1h30m.
func formatMinutes(minutes int) string {
	h, m := minutes/60, minutes%60
	switch {
	case h == 0:
		return fmt.Sprintf("%dm", m)
	case m == 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dh%dm", h, m)
	}
}

// formatOptionalMinutes renders a nullable minute limit, "off" when unset.
func formatOptionalMinutes(minutes *int) string {
	if minutes == nil {
		return "off"
	}
	return formatMinutes(*minutes)
}

// stopReasonText maps the API stop_reason to plain words.
// kindText is how a machine's kind (the API's deployment_type) reads:
// "VM", "Bare metal" or "Container". Anything else is shown as sent, and
// nothing as "-".
func kindText(deploymentType string) string {
	switch deploymentType {
	case "vm":
		return "VM"
	case "baremetal":
		return "Bare metal"
	case "container":
		return "Container"
	case "":
		return "-"
	default:
		return deploymentType
	}
}

func stopReasonText(reason string) string {
	switch reason {
	case "user":
		return "Stopped on request"
	case "max_runtime":
		return "Reached its max runtime"
	case "idle":
		return "Stopped when idle"
	case "budget":
		return "Environment budget reached"
	case "low_balance":
		return "Balance ran out"
	case "failed":
		return "Failed"
	case "system":
		return "Stopped by Runcrate"
	default:
		return reason
	}
}

// parseGPUFlag parses the --gpu flag value which may contain a colon-separated
// count (e.g. "H100:2"). Returns the GPU type and count. If --gpu-count was
// explicitly set alongside colon syntax, returns an error.
func parseGPUFlag(gpuFlag string, gpuCountFlag int, gpuCountChanged bool) (string, int, error) {
	if gpuFlag == "" {
		return "", gpuCountFlag, nil
	}

	parts := strings.SplitN(gpuFlag, ":", 2)
	if len(parts) == 2 {
		// Colon syntax: "H100:2"
		if gpuCountChanged {
			return "", 0, fmt.Errorf("cannot use both --gpu %s (colon syntax) and --gpu-count; pick one", gpuFlag)
		}
		count, err := strconv.Atoi(parts[1])
		if err != nil || count < 1 {
			return "", 0, fmt.Errorf("invalid GPU count in %q — expected a positive integer after the colon", gpuFlag)
		}
		return parts[0], count, nil
	}

	// No colon — use gpuCountFlag as-is (defaults to 1 for create, 0 for types)
	return gpuFlag, gpuCountFlag, nil
}

var jsonOutput bool

var instancesCmd = &cobra.Command{
	Use:     "instances",
	Aliases: []string{"i"},
	Short:   "Manage GPU instances",
}

// ── list ────────────────────────────────────────────────────────────────────

var instancesListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all instances",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		search, _ := cmd.Flags().GetString("search")
		instances, err := client.ListInstances(search)
		if err != nil {
			return fmt.Errorf("fetching instances: %w", err)
		}

		if jsonOutput {
			return printJSON(instances)
		}

		quiet, _ := cmd.Flags().GetBool("quiet")
		if quiet {
			for _, inst := range instances {
				fmt.Println(inst.ID)
			}
			return nil
		}

		if len(instances) == 0 {
			fmt.Println("No instances found. Create one with: runcrate instances create")
			return nil
		}

		w := newTable()
		fmt.Fprintf(w, "NAME\tSTATUS\tGPU\tKIND\tIP\t$/HR\n")
		for _, inst := range instances {
			gpu := inst.GPUType
			if inst.GPUCount > 1 {
				gpu = fmt.Sprintf("%dx %s", inst.GPUCount, inst.GPUType)
			}
			ip := inst.IP
			if ip == "" {
				ip = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t$%.2f\n",
				truncate(inst.Name, 24),
				statusColor(inst.Status),
				gpu, kindText(inst.DeploymentType), ip, inst.CostPerHour,
			)
		}
		return w.Flush()
	},
}

// ── types ───────────────────────────────────────────────────────────────────

var instancesTypesCmd = &cobra.Command{
	Use:   "types",
	Short: "List available GPU types and pricing",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		gpuRaw, _ := cmd.Flags().GetString("gpu")
		region, _ := cmd.Flags().GetString("region")
		gpuCountRaw, _ := cmd.Flags().GetInt("gpu-count")

		gpuType, gpuCount, err := parseGPUFlag(gpuRaw, gpuCountRaw, cmd.Flags().Changed("gpu-count"))
		if err != nil {
			return err
		}

		types, err := client.ListInstanceTypes(gpuType, region, gpuCount)
		if err != nil {
			return fmt.Errorf("fetching instance types: %w", err)
		}

		if jsonOutput {
			return printJSON(types)
		}

		if len(types) == 0 {
			fmt.Println("No GPU types available matching your filters.")
			return nil
		}

		w := newTable()
		fmt.Fprintf(w, "ID\tGPU\tCOUNT\tKIND\tCPU\tRAM\tSTORAGE\tREGION\t$/HR\n")
		for _, t := range types {
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%d cores\t%d GB\t%d GB\t%s\t$%.2f\n",
				t.ID,
				t.GPUType, t.GPUCount, kindText(t.DeploymentType),
				t.CPUCores, t.MemoryGB, t.StorageGB,
				t.Region, t.HourlyRate,
			)
		}
		return w.Flush()
	},
}

// ── create ──────────────────────────────────────────────────────────────────

// addInstanceOnlyFlags registers the create flags that only an instance takes,
// shared by instances create and schedule create.
func addInstanceOnlyFlags(cmd *cobra.Command) {
	cmd.Flags().String("type-id", "", "Instance type ID from 'runcrate instances types'")
	cmd.Flags().String("idle-stop", "",
		"Stop the instance after this long with GPU use under 5% and nobody connected (SSH, web terminal or editor), 5m to 24h (e.g. 30m), GPU instances only. Recommended for unattended work")
}

// buildInstanceRequest turns the create flags (name, gpu, gpu-count, region,
// template, type-id, volume, max-runtime, idle-stop) into the create body.
func buildInstanceRequest(cmd *cobra.Command) (api.CreateInstanceRequest, error) {
	var req api.CreateInstanceRequest
	name, _ := cmd.Flags().GetString("name")
	gpuRaw, _ := cmd.Flags().GetString("gpu")
	gpuCountRaw, _ := cmd.Flags().GetInt("gpu-count")
	region, _ := cmd.Flags().GetString("region")
	template, _ := cmd.Flags().GetString("template")
	typeID, _ := cmd.Flags().GetString("type-id")
	volumeID, _ := cmd.Flags().GetString("volume")
	maxRuntimeRaw, _ := cmd.Flags().GetString("max-runtime")
	idleStopRaw, _ := cmd.Flags().GetString("idle-stop")

	maxRuntime, err := parseMinutesFlag("max-runtime", maxRuntimeRaw, maxRuntimeMaxMinutes)
	if err != nil {
		return req, err
	}
	idleStop, err := parseMinutesFlag("idle-stop", idleStopRaw, idleStopMaxMinutes)
	if err != nil {
		return req, err
	}

	gpuType, gpuCount, err := parseGPUFlag(gpuRaw, gpuCountRaw, cmd.Flags().Changed("gpu-count"))
	if err != nil {
		return req, err
	}

	if name == "" {
		return req, fmt.Errorf("--name is required")
	}
	if gpuType == "" && typeID == "" {
		return req, fmt.Errorf("provide --gpu for auto-deploy or --type-id from 'runcrate instances types'")
	}

	return api.CreateInstanceRequest{
		Name:                name,
		GPUType:             gpuType,
		GPUCount:            gpuCount,
		Region:              region,
		Template:            template,
		InstanceTypeID:      typeID,
		MaxRuntimeMinutes:   maxRuntime,
		IdleShutdownMinutes: idleStop,
		StorageID:           volumeID,
	}, nil
}

var instancesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create and deploy a new instance",
	Long: `Create and deploy a new instance.

For unattended or agent-run work, set --max-runtime and --idle-stop so the instance
does not keep billing after the job ends. Auto stop terminates the instance, so keep
anything you need on a storage volume.`,
	Example: "  runcrate instances create --name train --gpu H100 --max-runtime 4h --idle-stop 30m",
	RunE: func(cmd *cobra.Command, args []string) error {
		req, err := buildInstanceRequest(cmd)
		if err != nil {
			return err
		}
		volumeID := req.StorageID

		client, err := loadClient()
		if err != nil {
			return err
		}

		// Check for duplicate active instance name
		existing, _ := client.ListInstances("")
		for _, inst := range existing {
			if strings.EqualFold(inst.Name, req.Name) && isActiveInstance(inst.Status) {
				return fmt.Errorf("instance %q already exists (ID: %s, status: %s). Use a different name or delete it first", req.Name, inst.ID, inst.Status)
			}
		}

		instance, err := client.CreateInstance(req)
		if api.IsBudgetReached(err) {
			return errors.New(budgetReachedMessage)
		}
		if err != nil {
			return fmt.Errorf("creating instance: %w", err)
		}

		if jsonOutput {
			return printJSON(instance)
		}

		gpu := instance.GPUType
		if instance.GPUCount > 1 {
			gpu = fmt.Sprintf("%dx %s", instance.GPUCount, instance.GPUType)
		}

		fmt.Println()
		fmt.Printf("  Instance queued: %s (%s)\n", instance.Name, gpu)
		fmt.Printf("  ID:          %s\n", instance.ID)
		fmt.Printf("  Cost:        $%.2f/hr\n", instance.CostPerHour)
		fmt.Printf("  Max runtime: %s\n", formatOptionalMinutes(instance.MaxRuntimeMinutes))
		fmt.Printf("  Idle stop:   %s\n", formatOptionalMinutes(instance.IdleShutdownMinutes))
		if volumeID != "" {
			fmt.Printf("  Volume:      %s, at /workspace\n", volumeID)
		}
		fmt.Println()
		fmt.Println("  Provisioning in the background. Track with:")
		fmt.Printf("    runcrate instances status %s\n", instance.Name)
		fmt.Printf("    runcrate instances status %s\n", instance.ID)
		fmt.Println()
		fmt.Println("  Once running, connect with:")
		fmt.Printf("    runcrate ssh %s\n", instance.Name)
		fmt.Println()
		return nil
	},
}

// ── info ────────────────────────────────────────────────────────────────────

var instancesInfoCmd = &cobra.Command{
	Use:   "info <name-or-id>",
	Short: "Show instance details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		id, err := resolveInstanceID(client, args[0])
		if err != nil {
			return err
		}

		instance, err := client.GetInstance(id)
		if err != nil {
			return fmt.Errorf("fetching instance: %w", err)
		}

		if jsonOutput {
			return printJSON(instance)
		}

		fmt.Println()
		fmt.Printf("  Name:         %s\n", instance.Name)
		fmt.Printf("  ID:           %s\n", instance.ID)
		fmt.Printf("  Status:       %s\n", statusColor(instance.Status))
		fmt.Printf("  GPU:          %dx %s\n", instance.GPUCount, instance.GPUType)
		fmt.Printf("  Kind:         %s\n", kindText(instance.DeploymentType))
		fmt.Printf("  CPU:          %d cores\n", instance.CPUCores)
		fmt.Printf("  Memory:       %d GB\n", instance.Memory)
		fmt.Printf("  Storage:      %d GB\n", instance.Storage)
		fmt.Printf("  Region:       %s\n", instance.Region)
		if instance.IP != "" {
			fmt.Printf("  IP:           %s\n", instance.IP)
		}
		fmt.Printf("  Image:        %s\n", instance.OSImage)
		fmt.Printf("  Cost:         $%.2f/hr\n", instance.CostPerHour)
		fmt.Printf("  Max runtime:  %s\n", formatOptionalMinutes(instance.MaxRuntimeMinutes))
		fmt.Printf("  Idle stop:    %s\n", formatOptionalMinutes(instance.IdleShutdownMinutes))
		if instance.AutoStopAt != nil {
			fmt.Printf("  Auto stop at: %s\n", *instance.AutoStopAt)
		}
		if instance.StopReason != nil {
			fmt.Printf("  Stop reason:  %s\n", stopReasonText(*instance.StopReason))
		}
		if instance.DeployedAt != "" {
			fmt.Printf("  Deployed:     %s\n", instance.DeployedAt)
		}
		fmt.Printf("  Created:      %s\n", instance.CreatedAt)
		fmt.Println()
		return nil
	},
}

// ── auto-stop ───────────────────────────────────────────────────────────────

var instancesAutoStopCmd = &cobra.Command{
	Use:   "auto-stop <name-or-id>",
	Short: "Change when a running instance stops on its own",
	Long: `Change the max runtime or idle stop of a starting or running instance.

Max runtime counts from when the instance started running, so give the total, not
extra time. A change never stops the instance sooner than 2 minutes from now. Use
off to turn a limit off; a flag you leave out keeps its value. Either limit
terminates the instance.`,
	Example: `  runcrate instances auto-stop training-run --max-runtime 8h
  runcrate instances auto-stop notebook --idle-stop 30m
  runcrate instances auto-stop job --max-runtime off --idle-stop off`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fields := map[string]*int{}
		if cmd.Flags().Changed("max-runtime") {
			raw, _ := cmd.Flags().GetString("max-runtime")
			v, err := parseAutoStopFlag("max-runtime", raw, maxRuntimeMaxMinutes)
			if err != nil {
				return err
			}
			fields["max_runtime_minutes"] = v
		}
		if cmd.Flags().Changed("idle-stop") {
			raw, _ := cmd.Flags().GetString("idle-stop")
			v, err := parseAutoStopFlag("idle-stop", raw, idleStopMaxMinutes)
			if err != nil {
				return err
			}
			fields["idle_shutdown_minutes"] = v
		}
		if len(fields) == 0 {
			return fmt.Errorf("pass --max-runtime, --idle-stop or both (off turns one off)")
		}

		client, err := loadClient()
		if err != nil {
			return err
		}
		id, err := resolveInstanceID(client, args[0])
		if err != nil {
			return err
		}
		instance, err := client.UpdateInstanceAutoStop(id, fields)
		if err != nil {
			return fmt.Errorf("changing auto stop: %w", err)
		}

		if jsonOutput {
			return printJSON(instance)
		}
		fmt.Println()
		fmt.Printf("  Name:         %s\n", instance.Name)
		fmt.Printf("  Max runtime:  %s\n", formatOptionalMinutes(instance.MaxRuntimeMinutes))
		fmt.Printf("  Idle stop:    %s\n", formatOptionalMinutes(instance.IdleShutdownMinutes))
		if instance.AutoStopAt != nil {
			fmt.Printf("  Auto stop at: %s\n", *instance.AutoStopAt)
		}
		fmt.Println()
		return nil
	},
}

// ── status ──────────────────────────────────────────────────────────────────

var instancesStatusCmd = &cobra.Command{
	Use:   "status <name-or-id>",
	Short: "Check live instance status",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		id, err := resolveInstanceID(client, args[0])
		if err != nil {
			return err
		}

		status, err := client.GetInstanceStatus(id)
		if err != nil {
			return fmt.Errorf("fetching status: %w", err)
		}

		if jsonOutput {
			return printJSON(status)
		}

		fmt.Printf("  ID:      %s\n", status.ID)
		fmt.Printf("  Status:  %s\n", statusColor(status.Status))
		if status.IP != "" {
			fmt.Printf("  IP:      %s\n", status.IP)
		}
		return nil
	},
}

// ── delete ──────────────────────────────────────────────────────────────────

var instancesDeleteCmd = &cobra.Command{
	Use:     "delete <name-or-id>",
	Aliases: []string{"rm"},
	Short:   "Terminate an instance",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		id, err := resolveInstanceID(client, args[0])
		if err != nil {
			return err
		}

		fmt.Printf("Terminating instance %s...\n", args[0])
		if err := client.DeleteInstance(id); err != nil {
			return fmt.Errorf("deleting instance: %w", err)
		}

		fmt.Println("Instance terminated.")
		return nil
	},
}

// resolveInstanceID resolves a name or ID to an instance UUID.
func resolveInstanceID(client *api.Client, ref string) (string, error) {
	// Try direct ID lookup first
	inst, err := client.GetInstance(ref)
	if err == nil {
		return inst.ID, nil
	}

	// Fall back to name search
	instances, listErr := client.ListInstances("")
	if listErr != nil {
		return "", fmt.Errorf("fetching instances: %w", listErr)
	}

	var matches []api.Instance
	for _, inst := range instances {
		if strings.EqualFold(inst.Name, ref) {
			matches = append(matches, inst)
		}
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("instance %q not found", ref)
	}
	if len(matches) > 1 {
		fmt.Printf("Multiple instances named %q found:\n", ref)
		for _, m := range matches {
			fmt.Printf("  %s  %s  %s\n", m.ID, statusColor(m.Status), m.GPUType)
		}
		return "", fmt.Errorf("use the instance ID instead of the name")
	}

	return matches[0].ID, nil
}

// isActiveInstance returns true if the instance is not terminated/failed.
func isActiveInstance(status string) bool {
	s := strings.ToLower(status)
	return s != "terminated" && s != "failed"
}

// ── init ────────────────────────────────────────────────────────────────────

func init() {
	// Global --json flag on parent
	instancesCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output as JSON")

	// list
	instancesListCmd.Flags().String("search", "", "Filter instances by name")
	instancesListCmd.Flags().BoolP("quiet", "q", false, "Only show instance IDs")

	// types
	instancesTypesCmd.Flags().String("gpu", "", "Filter by GPU type (e.g., A100 or A100:4)")
	instancesTypesCmd.Flags().String("region", "", "Filter by region (e.g., us-east)")
	instancesTypesCmd.Flags().Int("gpu-count", 0, "Filter by GPU count")

	// create
	instancesCreateCmd.Flags().String("name", "", "Instance name (required)")
	instancesCreateCmd.Flags().String("gpu", "", "GPU type (e.g., H100 or H100:2 for multi-GPU)")
	instancesCreateCmd.Flags().Int("gpu-count", 1, "Number of GPUs (alternative to colon syntax)")
	instancesCreateCmd.Flags().String("region", "", "Preferred region")
	instancesCreateCmd.Flags().String("template", "", "OS template")
	instancesCreateCmd.Flags().String("volume", "", "Storage volume ID to attach, from 'runcrate volumes list'; its files are at /workspace")
	instancesCreateCmd.Flags().String("max-runtime", "",
		"Stop the instance automatically after this long, 5m to 168h (e.g. 4h). Recommended for unattended work")
	addInstanceOnlyFlags(instancesCreateCmd)

	// auto-stop
	instancesAutoStopCmd.Flags().String("max-runtime", "",
		"Total time the instance may run, counted from when it started, 5m to 168h (e.g. 8h), or off")
	instancesAutoStopCmd.Flags().String("idle-stop", "",
		"Stop after this long with GPU use under 5% and nobody connected, 5m to 24h (e.g. 30m), or off")

	// Wire subcommands
	instancesCmd.AddCommand(instancesListCmd)
	instancesCmd.AddCommand(instancesTypesCmd)
	instancesCmd.AddCommand(instancesCreateCmd)
	instancesCmd.AddCommand(instancesInfoCmd)
	instancesCmd.AddCommand(instancesAutoStopCmd)
	instancesCmd.AddCommand(instancesStatusCmd)
	instancesCmd.AddCommand(instancesDeleteCmd)
	rootCmd.AddCommand(instancesCmd)
}
