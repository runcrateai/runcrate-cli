package commands

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/runcrate/cli/internal/api"
	"github.com/spf13/cobra"
)

// billingDashboardURL is where cards are added and top-ups confirmed: the API
// never takes card details.
const billingDashboardURL = "https://runcrate.ai/dashboard/billing"

// Auto-recharge amounts sent when turning it off before it was ever set (the
// server needs both even to turn it off).
const (
	defaultRechargeThreshold = 20
	defaultRechargeTarget    = 50
)

var billingCmd = &cobra.Command{
	Use:   "billing",
	Short: "View billing and usage, top up, and manage auto-recharge",
}

var billingBalanceCmd = &cobra.Command{
	Use:   "balance",
	Short: "Show current credit balance, burn per hour and runway",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		balance, err := client.GetBalance()
		if err != nil {
			return fmt.Errorf("fetching balance: %w", err)
		}
		if jsonOutput {
			return printJSON(balance)
		}
		renderBalance(os.Stdout, balance)
		return nil
	},
}

// renderBalance prints the balance, and what running machines burn and how
// long the balance lasts when the server reports them.
func renderBalance(w io.Writer, b *api.Balance) {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	bold := lipgloss.NewStyle().Bold(true)

	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s  %s\n", dim.Render("Credits     "), bold.Render(fmt.Sprintf("$%.2f", b.Credits)))
	if b.ActiveUsageCost > 0 {
		fmt.Fprintf(w, "  %s  $%.2f\n", dim.Render("Active usage"), b.ActiveUsageCost)
	}
	if b.FloatingBalanceUsd != nil {
		fmt.Fprintf(w, "  %s  $%.2f %s\n", dim.Render("Floating    "), *b.FloatingBalanceUsd, dim.Render("(net of running machines)"))
	}
	if b.BurnUsdPerHour != nil {
		fmt.Fprintf(w, "  %s  $%.2f/hr\n", dim.Render("Burn        "), *b.BurnUsdPerHour)
		switch {
		case b.RunwayHours != nil:
			fmt.Fprintf(w, "  %s  %s\n", dim.Render("Runway      "), formatRunway(*b.RunwayHours))
		case *b.BurnUsdPerHour == 0:
			fmt.Fprintf(w, "  %s  %s\n", dim.Render("Runway      "), dim.Render("nothing running"))
		}
	}
	fmt.Fprintln(w)
}

// formatRunway writes hours of runway as minutes, hours or days.
func formatRunway(hours float64) string {
	switch {
	case hours < 1:
		return fmt.Sprintf("%d min", int(math.Round(hours*60)))
	case hours < 48:
		return fmt.Sprintf("%.1f hours", hours)
	default:
		return fmt.Sprintf("%.1f days", hours/24)
	}
}

// parsePeriodFrom converts a period like "7d", "24h", or "2w" into an RFC3339
// lower-bound timestamp plus a human label. Returns ("", "") for empty/invalid
// input (the server then reports all-time usage).
func parsePeriodFrom(period string) (from, label string) {
	period = strings.TrimSpace(period)
	if period == "" {
		return "", ""
	}
	n, err := strconv.Atoi(period[:len(period)-1])
	if err != nil || n <= 0 {
		return "", ""
	}
	var d time.Duration
	switch period[len(period)-1] {
	case 'h':
		d = time.Duration(n) * time.Hour
	case 'd':
		d = time.Duration(n) * 24 * time.Hour
	case 'w':
		d = time.Duration(n) * 7 * 24 * time.Hour
	default:
		return "", ""
	}
	return time.Now().Add(-d).UTC().Format(time.RFC3339), period
}

var billingUsageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Show inference usage summary",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		period, _ := cmd.Flags().GetString("period")
		from, label := parsePeriodFrom(period)

		usage, err := client.GetUsage(from)
		if err != nil {
			return fmt.Errorf("fetching usage: %w", err)
		}
		if jsonOutput {
			return printJSON(usage)
		}

		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
		bold := lipgloss.NewStyle().Bold(true)

		scope := "all time"
		if label != "" {
			scope = "last " + label
		}

		fmt.Println()
		fmt.Printf("  %s %s\n", bold.Render("Inference usage"), dim.Render("("+scope+")"))
		fmt.Printf("    %s  %d\n", dim.Render("Requests"), usage.TotalRequests)
		fmt.Printf("    %s  %d %s\n", dim.Render("Tokens  "), usage.TotalTokens,
			dim.Render(fmt.Sprintf("(%d prompt / %d completion)", usage.TotalPromptTokens, usage.TotalCompletionTokens)))
		fmt.Printf("    %s  %s\n", dim.Render("Cost    "), bold.Render(fmt.Sprintf("$%.4f", usage.TotalCost)))

		// Account context so this isn't only inference numbers. Compute/instance
		// charges show up against the credit balance + active usage.
		if balance, err := client.GetBalance(); err == nil {
			fmt.Println()
			fmt.Printf("  %s  %s\n", dim.Render("Credits     "), bold.Render(fmt.Sprintf("$%.2f", balance.Credits)))
			if balance.ActiveUsageCost > 0 {
				fmt.Printf("  %s  $%.2f\n", dim.Render("Active usage"), balance.ActiveUsageCost)
			}
		}
		fmt.Println()

		return nil
	},
}

var billingTopUpCmd = &cobra.Command{
	Use:     "topup <amount>",
	Aliases: []string{"top-up"},
	Short:   "Add credit in USD, charged to the workspace's default card",
	Long: "Adds credit to the workspace balance, charged to its default card. Needs the billing\n" +
		"permission on your API key or sign-in, turned on by a workspace owner or manager.\n\n" +
		"Each top-up has an idempotency key (a new one unless you pass --idempotency-key). Sending\n" +
		"the same key again answers that top-up's current status instead of charging again: that is\n" +
		"how a processing top-up, or one waiting for confirmation, is checked.",
	Example: "  runcrate billing topup 50\n" +
		"  runcrate billing topup 50 --yes --json\n" +
		"  runcrate billing topup 50 --idempotency-key <key> --yes   # check an earlier top-up",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		amount, err := parseUSD(args[0])
		if err != nil {
			return err
		}
		cmd.SilenceUsage = true
		client, err := loadClient()
		if err != nil {
			return err
		}

		key, _ := cmd.Flags().GetString("idempotency-key")
		if key == "" {
			key = api.NewIdempotencyKey()
		}
		if yes, _ := cmd.Flags().GetBool("yes"); !yes {
			ok, err := confirm(os.Stdin, os.Stderr, fmt.Sprintf(
				"Charge $%.2f to the workspace's default card and add it to the balance? [y/N] ", amount))
			if err != nil {
				return errors.New("no answer to the prompt; pass --yes to top up without asking")
			}
			if !ok {
				fmt.Println("Nothing charged.")
				return nil
			}
		}

		t, err := client.TopUp(amount, key)
		if err != nil {
			return topUpError(err, key)
		}
		if jsonOutput {
			if err := printJSON(t); err != nil {
				return err
			}
			if t.Status == api.TopUpDeclined {
				return declinedError(t)
			}
			return nil
		}
		return renderTopUp(os.Stdout, t)
	},
}

// parseUSD reads a positive dollar amount such as 50, 49.99 or $50. The
// server checks the range.
func parseUSD(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimPrefix(strings.TrimSpace(s), "$"), 64)
	if err != nil || v <= 0 || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, fmt.Errorf("amount %q is not a dollar amount such as 50 or 49.99", s)
	}
	return v, nil
}

// renderTopUp prints what came of a top-up and how to check it again. A
// declined top-up is an error, so the command exits non-zero.
func renderTopUp(w io.Writer, t *api.TopUp) error {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	amount := fmt.Sprintf("$%.2f", t.AmountUsd)
	again := fmt.Sprintf("runcrate billing topup %s --idempotency-key %s --yes",
		strconv.FormatFloat(t.AmountUsd, 'f', -1, 64), t.IdempotencyKey)

	if t.Replayed {
		fmt.Fprintln(w, "This key was sent before, so nothing new was charged. Its top-up now:")
	}
	switch t.Status {
	case api.TopUpSucceeded:
		fmt.Fprintf(w, "Added %s to the balance.", amount)
		if t.BalanceUsd != nil {
			fmt.Fprintf(w, " Balance: $%.2f.", *t.BalanceUsd)
		}
		if t.InvoiceNumber != nil && *t.InvoiceNumber != "" {
			fmt.Fprintf(w, " Invoice %s.", *t.InvoiceNumber)
		}
		fmt.Fprintln(w)
	case api.TopUpProcessing:
		fmt.Fprintf(w, "The top-up of %s is processing. Check it with the same key:\n  %s\n", amount, again)
	case api.TopUpRequiresAction:
		fmt.Fprintf(w, "The top-up of %s needs confirming. Nothing is charged until then.\n", amount)
		fmt.Fprintf(w, "A workspace owner or manager must open this link, signed in to Runcrate:\n  %s\n", t.ConfirmURL)
		if t.ExpiresAt != nil && *t.ExpiresAt != "" {
			fmt.Fprintf(w, "The link expires at %s.\n", *t.ExpiresAt)
		}
		fmt.Fprintf(w, "Then check it with the same key:\n  %s\n", again)
	case api.TopUpDeclined:
		return declinedError(t)
	default:
		fmt.Fprintf(w, "The top-up of %s is %s. Check it with the same key:\n  %s\n", amount, t.Status, again)
	}
	fmt.Fprintf(w, "%s\n", dim.Render("Idempotency key: "+t.IdempotencyKey))
	return nil
}

// declinedError is a declined top-up, with the server's sentence.
func declinedError(t *api.TopUp) error {
	why := t.Message
	if why == "" {
		why = t.Reason
	}
	return fmt.Errorf("top-up of $%.2f declined: %s", t.AmountUsd, why)
}

// topUpError adds the key to send again when the top-up may have been made:
// no answer came, or it is being charged right now.
func topUpError(err error, key string) error {
	var ne *api.SubmitNetworkError
	if errors.As(err, &ne) {
		return fmt.Errorf("topping up: %w\n  The top-up may have been made. Run the same command with --idempotency-key %s: "+
			"it answers that top-up instead of charging again", err, ne.Key)
	}
	var ae *api.APIError
	if errors.As(err, &ae) && ae.Status == http.StatusConflict && ae.Code == "conflict" {
		return fmt.Errorf("topping up: %w\n  Check it in a moment with --idempotency-key %s", err, key)
	}
	return fmt.Errorf("topping up: %w", err)
}

var billingAutoRechargeCmd = &cobra.Command{
	Use:     "auto-recharge",
	Aliases: []string{"autorecharge"},
	Short:   "Show auto-recharge: a top-up from the default card when the balance runs low",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		a, err := client.GetAutoRecharge()
		if err != nil {
			return fmt.Errorf("fetching auto-recharge: %w", err)
		}
		return showAutoRecharge(a)
	},
}

var billingAutoRechargeSetCmd = &cobra.Command{
	Use:   "set --threshold X --target Y",
	Short: "Turn auto-recharge on: below the threshold, top up to the target (USD)",
	Long: "Turns auto-recharge on. When the balance falls below --threshold, the default card is\n" +
		"charged to bring it back to --target. Needs the billing permission, and the person behind the\n" +
		"key or sign-in must be a workspace owner or manager.",
	Example: "  runcrate billing auto-recharge set --threshold 20 --target 100",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		threshold, _ := cmd.Flags().GetFloat64("threshold")
		target, _ := cmd.Flags().GetFloat64("target")
		cmd.SilenceUsage = true
		client, err := loadClient()
		if err != nil {
			return err
		}
		a, err := client.SetAutoRecharge(true, threshold, target)
		if err != nil {
			return fmt.Errorf("turning on auto-recharge: %w", err)
		}
		return showAutoRecharge(a)
	},
}

var billingAutoRechargeOffCmd = &cobra.Command{
	Use:   "off",
	Short: "Turn auto-recharge off, keeping its amounts",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		client, err := loadClient()
		if err != nil {
			return err
		}
		a, err := autoRechargeOff(client)
		if err != nil {
			return err
		}
		return showAutoRecharge(a)
	},
}

// autoRechargeOff turns auto-recharge off. The server needs the amounts even
// then, so it sends back the current ones (or the defaults if none were set).
func autoRechargeOff(client *api.Client) (*api.AutoRecharge, error) {
	cur, err := client.GetAutoRecharge()
	if err != nil {
		return nil, fmt.Errorf("fetching auto-recharge: %w", err)
	}
	threshold, target := float64(defaultRechargeThreshold), float64(defaultRechargeTarget)
	if cur.ThresholdUsd != nil {
		threshold = *cur.ThresholdUsd
	}
	if cur.TargetUsd != nil {
		target = *cur.TargetUsd
	}
	a, err := client.SetAutoRecharge(false, threshold, target)
	if err != nil {
		return nil, fmt.Errorf("turning off auto-recharge: %w", err)
	}
	return a, nil
}

func showAutoRecharge(a *api.AutoRecharge) error {
	if jsonOutput {
		return printJSON(a)
	}
	renderAutoRecharge(os.Stdout, a)
	return nil
}

func renderAutoRecharge(w io.Writer, a *api.AutoRecharge) {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	bold := lipgloss.NewStyle().Bold(true)

	state := "off"
	if a.Enabled {
		state = "on"
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s  %s\n", dim.Render("Auto-recharge"), bold.Render(state))
	if a.ThresholdUsd != nil {
		fmt.Fprintf(w, "  %s  $%.2f\n", dim.Render("Below        "), *a.ThresholdUsd)
	}
	if a.TargetUsd != nil {
		fmt.Fprintf(w, "  %s  $%.2f\n", dim.Render("Top up to    "), *a.TargetUsd)
	}
	if a.LastFailureReason != nil && *a.LastFailureReason != "" {
		when := ""
		if a.LastFailureAt != nil {
			when = " " + dim.Render("("+*a.LastFailureAt+")")
		}
		fmt.Fprintf(w, "  %s  %s%s\n", dim.Render("Last failure "), *a.LastFailureReason, when)
	}
	if !a.Enabled {
		threshold, target := float64(defaultRechargeThreshold), float64(defaultRechargeTarget)
		if a.ThresholdUsd != nil && a.TargetUsd != nil {
			threshold, target = *a.ThresholdUsd, *a.TargetUsd
		}
		fmt.Fprintf(w, "\n  %s\n", dim.Render(fmt.Sprintf("Turn it on: runcrate billing auto-recharge set --threshold %s --target %s",
			strconv.FormatFloat(threshold, 'f', -1, 64), strconv.FormatFloat(target, 'f', -1, 64))))
	}
	fmt.Fprintln(w)
}

var billingPaymentMethodsCmd = &cobra.Command{
	Use:     "payment-methods",
	Aliases: []string{"cards"},
	Short:   "List the workspace's cards",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		list, err := client.ListPaymentMethods()
		if err != nil {
			return fmt.Errorf("fetching payment methods: %w", err)
		}
		if jsonOutput {
			return printJSON(list)
		}
		return renderPaymentMethods(os.Stdout, list)
	},
}

func renderPaymentMethods(w io.Writer, list []api.PaymentMethod) error {
	if len(list) == 0 {
		fmt.Fprintf(w, "No card on file. A workspace owner or manager adds one in the dashboard: %s\n", billingDashboardURL)
		return nil
	}
	t := newTableTo(w)
	fmt.Fprintf(t, "BRAND\tLAST4\tEXPIRES\tDEFAULT\n")
	for _, pm := range list {
		brand := pm.Type
		if pm.Brand != nil && *pm.Brand != "" {
			brand = *pm.Brand
		}
		last4, expires, def := "-", "-", "no"
		if pm.Last4 != nil && *pm.Last4 != "" {
			last4 = *pm.Last4
		}
		if pm.ExpMonth != nil && pm.ExpYear != nil {
			expires = fmt.Sprintf("%02d/%d", *pm.ExpMonth, *pm.ExpYear)
		}
		if pm.Default {
			def = "yes"
		}
		fmt.Fprintf(t, "%s\t%s\t%s\t%s\n", brand, last4, expires, def)
	}
	return t.Flush()
}

func init() {
	billingCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	billingUsageCmd.Flags().String("period", "", "Usage period (e.g., 24h, 7d, 2w)")

	billingTopUpCmd.Flags().String("idempotency-key", "", "Top up at most once per key; send it again to check that top-up (default: a new key)")
	billingTopUpCmd.Flags().BoolP("yes", "y", false, "Top up without asking")

	billingAutoRechargeSetCmd.Flags().Float64("threshold", 0, "Top up when the balance falls below this many USD (required)")
	billingAutoRechargeSetCmd.Flags().Float64("target", 0, "Bring the balance back to this many USD (required)")
	for _, name := range []string{"threshold", "target"} {
		_ = billingAutoRechargeSetCmd.MarkFlagRequired(name)
	}
	billingAutoRechargeCmd.AddCommand(billingAutoRechargeSetCmd, billingAutoRechargeOffCmd)

	billingCmd.AddCommand(billingBalanceCmd, billingUsageCmd, billingTopUpCmd, billingAutoRechargeCmd, billingPaymentMethodsCmd)
	rootCmd.AddCommand(billingCmd)
}
