package commands

import (
	"fmt"
	"os"

	"github.com/runcrate/cli/internal/config"
	"github.com/runcrate/cli/internal/update"
	"github.com/spf13/cobra"
)

var (
	versionStr string
	commitStr  string
	buildDate  string
)

func SetVersionInfo(version, commit, date string) {
	versionStr = version
	commitStr = commit
	buildDate = date
	update.SetCurrentVersion(version)
}

func printShort() {
	v := versionStr
	if v == "" {
		v = "dev"
	}

	fmt.Println()
	fmt.Printf("  runcrate %s\n", v)

	if cfg, err := config.Load(); err == nil && cfg.IsOAuth() {
		env := cfg.Environment
		if env == "" {
			env = "main"
		}
		fmt.Printf("  %s · %s\n", cfg.ProjectName, env)
	}

	fmt.Println()
	fmt.Println("  Run \"runcrate --help\" for commands.")
	fmt.Println()
}

func printHelp() {
	v := versionStr
	if v == "" {
		v = "dev"
	}

	fmt.Println()
	fmt.Printf("  runcrate %s — GPU cloud from your terminal.\n", v)
	fmt.Println()
	fmt.Println("  Get started:")
	fmt.Println("    login                            Sign in via browser")
	fmt.Println("    instances types --gpu A100        See available GPUs and pricing")
	fmt.Println("    instances create --name dev --gpu A100")
	fmt.Println("                                     Deploy a GPU instance")
	fmt.Println("    ssh dev                          SSH into your instance")
	fmt.Println("    ssh dev -- nvidia-smi            Run a command remotely")
	fmt.Println()
	fmt.Println("  Instances:")
	fmt.Println("    ps, instances list [-q]          List instances (-q for IDs only)")
	fmt.Println("    instances types [--gpu X]        Available GPUs + pricing")
	fmt.Println("    instances create --name X --gpu A100 [--template Y]")
	fmt.Println("    instances info <name-or-id>      Details (IP, GPU, cost)")
	fmt.Println("    instances status <name-or-id>    Live status from provider")
	fmt.Println("    instances delete <name-or-id>    Terminate and stop billing")
	fmt.Println()
	fmt.Println("  Jobs:                              A command on its own machine, stopped when it ends")
	fmt.Println("    run --gpu A4000 -- <cmd>         Run and stream output [--volume X] [--max-runtime 2h]")
	fmt.Println("    run --gpu A4000 -d -- <cmd>      Start and return the job ID")
	fmt.Println("    jobs list [--status running]     List jobs")
	fmt.Println("    jobs status <id>                 Details (exit code, cost, error)")
	fmt.Println("    jobs logs <id> [-f]              Print output (-f to follow)")
	fmt.Println("    jobs cancel <id>                 Cancel and stop the machine")
	fmt.Println()
	fmt.Println("  Clusters:                          Several GPU machines on one network, all or nothing")
	fmt.Println("    clusters offers --gpu H100:8 --nodes 4")
	fmt.Println("                                     Where a cluster can start, with its price per hour")
	fmt.Println("    clusters create --name X --offer Y --nodes 4 [--stack slurm|kubernetes|ray]")
	fmt.Println("    clusters list | get <name>       Nodes, how to connect, last test")
	fmt.Println("    clusters ssh <name> [--node N]   SSH to the head or a node")
	fmt.Println("    clusters kubeconfig <name> [-o file]  The kubeconfig of a Kubernetes cluster")
	fmt.Println("    clusters tunnel <name>           Kubernetes API (6443) or Ray dashboard (8265) on this computer")
	fmt.Println("    clusters test <name>             NCCL all-reduce bandwidth across every GPU")
	fmt.Println("    clusters delete <name>           Terminate every node and stop billing")
	fmt.Println()
	fmt.Println("  Access:")
	fmt.Println("    ssh <instance>                   Interactive shell (keyless)")
	fmt.Println("    ssh <instance> -- <cmd>          Run command and exit")
	fmt.Println("    cp ./local instance:/remote      Upload files")
	fmt.Println("    cp instance:/remote ./local      Download files")
	fmt.Println()
	fmt.Println("  Volumes:                           Persistent storage across instances")
	fmt.Println("    volumes list                     List volumes")
	fmt.Println("    volumes create --name X --size 100")
	fmt.Println("    volumes resize <id> --size 200")
	fmt.Println("    volumes delete <id>")
	fmt.Println("    volumes regions                  Where volumes can be created")
	fmt.Println()
	fmt.Println("  SSH Keys:                          For non-keyless SSH access")
	fmt.Println("    ssh-keys list")
	fmt.Println("    ssh-keys add --name X --file ~/.ssh/id_ed25519.pub")
	fmt.Println("    ssh-keys delete <id>")
	fmt.Println()
	fmt.Println("  Billing:")
	fmt.Println("    billing balance                  Current credit balance")
	fmt.Println("    billing usage                    Spending breakdown")
	fmt.Println()
	fmt.Println("  Workspaces:                        Separate projects, teams, billing")
	fmt.Println("    workspaces                       List all your workspaces")
	fmt.Println("    workspaces switch                Switch active workspace")
	fmt.Println()
	fmt.Println("  Environments:                      Isolate resources within a workspace")
	fmt.Println("    envs                             List environments")
	fmt.Println("    envs switch <name>               Switch active environment")
	fmt.Println("    envs create <name>               Create new environment")
	fmt.Println("    envs delete <name>               Delete environment")
	fmt.Println()
	fmt.Println("  Secrets:                           Variables every machine in the environment gets")
	fmt.Println("    secrets list [-q]                Names only; values are set in the dashboard")
	fmt.Println()
	fmt.Println("  AI clients:                        Use Runcrate from your AI tools over MCP")
	fmt.Println("    mcp install [--client cursor]    Connect Cursor, Claude Code, VS Code, Codex, Windsurf, Gemini")
	fmt.Println("    mcp url                          Print the MCP server URL")
	fmt.Println()
	fmt.Println("  Other:")
	fmt.Println("    templates                        OS templates for instances")
	fmt.Println("    config show                      Current CLI configuration")
	fmt.Println("    update                           Update to latest version")
	fmt.Println("    logout                           Clear credentials")
	fmt.Println()
	fmt.Println("  Tips:")
	fmt.Println("    Most list commands support --json and aliases: ls, rm, -q")
	fmt.Println("    Run \"runcrate <command> --help\" for detailed usage.")
	fmt.Println()
}

var psCmd = &cobra.Command{
	Use:    "ps",
	Short:  "List instances (shortcut for instances list)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return instancesListCmd.RunE(cmd, args)
	},
}

var rootCmd = &cobra.Command{
	Use:   "runcrate",
	Short: "Runcrate CLI — GPU cloud from your terminal",
	Run: func(cmd *cobra.Command, args []string) {
		printShort()
	},
}

func Execute() error {
	defaultHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd == rootCmd {
			printHelp()
		} else {
			defaultHelp(cmd, args)
		}
	})

	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		update.NotifyFromCache(os.Stderr, versionStr)
		go update.RefreshCache()
	}

	psCmd.Flags().String("search", "", "Filter instances by name")
	psCmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	rootCmd.AddCommand(psCmd)

	return rootCmd.Execute()
}
