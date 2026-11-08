package commands

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	// Zone data built in, so --tz works where the OS has none (Windows).
	_ "time/tzdata"

	"github.com/runcrate/cli/internal/api"
	"github.com/spf13/cobra"
)

// ── when ────────────────────────────────────────────────────────────────────

// Wall-time layouts for --at, read in --tz.
var atWallLayouts = []string{
	"2006-01-02 15:04",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04",
	"2006-01-02T15:04:05",
}

// Layouts for --at with an explicit offset or Z, used as is.
var atOffsetLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04Z07:00",
	"2006-01-02 15:04Z07:00",
	"2006-01-02 15:04:05Z07:00",
}

const displayTimeLayout = "2006-01-02 15:04 MST"

// parseAt reads --at. A time with an offset or Z is that instant; a wall time
// such as "2026-10-21 02:00" is read in loc. notice is set when the wall time
// does not exist in loc (clocks go forward) and was moved past the gap.
func parseAt(value string, loc *time.Location, now time.Time) (t time.Time, notice string, err error) {
	value = strings.TrimSpace(value)
	for _, layout := range atOffsetLayouts {
		if t, err = time.Parse(layout, value); err == nil {
			return checkFuture(value, t, now)
		}
	}
	for _, layout := range atWallLayouts {
		naive, perr := time.ParseInLocation(layout, value, time.UTC)
		if perr != nil {
			continue
		}
		t, shifted := wallTime(naive, loc)
		if shifted {
			notice = fmt.Sprintf("%s does not exist in %s (the clocks go forward), so it runs at %s.",
				naive.Format("2006-01-02 15:04"), loc, t.In(loc).Format(displayTimeLayout))
		}
		t, _, err = checkFuture(value, t, now)
		return t, notice, err
	}
	return time.Time{}, "", fmt.Errorf("invalid --at %q: use a time like '2026-10-21 02:00' (in --tz) or '2026-10-21T02:00:00+01:00'", value)
}

func checkFuture(value string, t, now time.Time) (time.Time, string, error) {
	if !t.After(now) {
		return time.Time{}, "", fmt.Errorf("--at %s is in the past", value)
	}
	return t, "", nil
}

// wallTime is the instant when loc's clocks read naive's wall clock (naive is
// read as UTC). A time that happens twice (clocks go back) is the first one; a
// time that never happens (clocks go forward) keeps the offset from before the
// change, so it lands the length of the gap later (02:30 becomes 03:30), as the
// server does for cron. shifted reports that case.
func wallTime(naive time.Time, loc *time.Location) (t time.Time, shifted bool) {
	_, before := naive.Add(-24 * time.Hour).In(loc).Zone()
	_, after := naive.Add(24 * time.Hour).In(loc).Zone()
	var first time.Time
	for _, offset := range []int{before, after} {
		c := naive.Add(-time.Duration(offset) * time.Second)
		if !sameWallClock(c.In(loc), naive) {
			continue
		}
		if first.IsZero() || c.Before(first) {
			first = c
		}
	}
	if !first.IsZero() {
		return first, false
	}
	return naive.Add(-time.Duration(before) * time.Second), true
}

func sameWallClock(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd &&
		a.Hour() == b.Hour() && a.Minute() == b.Minute() && a.Second() == b.Second()
}

// localTimezone is this machine's IANA zone: $TZ when it names one, else the
// /etc/localtime link.
func localTimezone() (string, error) {
	if tz, ok := os.LookupEnv("TZ"); ok {
		tz = strings.TrimPrefix(tz, ":")
		if tz == "" {
			return "UTC", nil
		}
		if !filepath.IsAbs(tz) {
			if _, err := time.LoadLocation(tz); err == nil {
				return tz, nil
			}
		}
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(target, "zoneinfo/"); ok {
			if _, err := time.LoadLocation(name); err == nil {
				return name, nil
			}
		}
	}
	return "", errors.New("could not tell this machine's timezone; pass --tz, e.g. --tz Europe/London")
}

// resolveTimezone loads --tz, or this machine's zone when it is empty.
func resolveTimezone(name string) (*time.Location, error) {
	if name == "" {
		local, err := localTimezone()
		if err != nil {
			return nil, err
		}
		name = local
	}
	loc, err := time.LoadLocation(name)
	if err != nil || name == "Local" {
		return nil, fmt.Errorf("unknown --tz %q: use an IANA name such as Europe/London or America/New_York", name)
	}
	return loc, nil
}

// ── create ──────────────────────────────────────────────────────────────────

// Flags that only one kind takes.
var (
	instanceOnlyScheduleFlags = []string{"type-id", "idle-stop"}
	jobOnlyScheduleFlags      = []string{"image", "env"}
)

// buildScheduleRequest turns schedule create's flags and the job command after
// -- into the create body. A command after -- makes it a job schedule, else an
// instance schedule. notice is a note for stderr (a DST-shifted --at).
func buildScheduleRequest(cmd *cobra.Command, args []string, lookupEnv func(string) (string, bool), now time.Time) (req api.CreateScheduleRequest, notice string, err error) {
	flags := cmd.Flags()
	at, _ := flags.GetString("at")
	cron, _ := flags.GetString("cron")
	tz, _ := flags.GetString("tz")
	overlap, _ := flags.GetString("overlap")

	// When.
	switch {
	case at != "" && cron != "":
		return req, "", errors.New("pass only one of --at (one run) and --cron (recurring)")
	case at == "" && cron == "":
		return req, "", errors.New("pass --at '2026-10-21 02:00' for one run or --cron '0 2 * * *' for a recurring one")
	}
	loc, err := resolveTimezone(tz)
	if err != nil {
		return req, "", err
	}
	req.Timezone = loc.String()
	if at != "" {
		if flags.Changed("overlap") {
			return req, "", errors.New("--overlap only applies to --cron schedules")
		}
		t, n, err := parseAt(at, loc, now)
		if err != nil {
			return req, "", err
		}
		req.RunAt = t.UTC().Format(time.RFC3339)
		notice = n
	} else {
		fields := strings.Fields(cron)
		if len(fields) != 5 {
			return req, "", fmt.Errorf("invalid --cron %q: use 5 fields, minute hour day-of-month month day-of-week, e.g. '0 2 * * *'", cron)
		}
		req.Cron = strings.Join(fields, " ")
		if overlap != "skip" && overlap != "allow" {
			return req, "", fmt.Errorf("invalid --overlap %q: use skip or allow", overlap)
		}
		req.Overlap = overlap
	}

	// What.
	dash := cmd.ArgsLenAtDash()
	if dash > 0 || (dash < 0 && len(args) > 0) {
		return req, "", fmt.Errorf("unexpected argument %q: put a job's command after --, e.g. -- python train.py", args[0])
	}
	isJob := dash == 0
	if isJob && len(args) == 0 {
		return req, "", errors.New("give the job's command after --, e.g. -- python train.py")
	}

	maxRuntime, _ := flags.GetString("max-runtime")
	if strings.TrimSpace(maxRuntime) == "" {
		return req, "", errors.New("--max-runtime is required for a schedule, so nothing it starts keeps running (and billing) unattended, e.g. --max-runtime 2h (5m to 168h for instances, 1m to 168h for jobs)")
	}

	req.Name, _ = flags.GetString("name")
	if isJob {
		if err := refuseFlags(flags.Changed, instanceOnlyScheduleFlags, "instance schedules (no command after --)"); err != nil {
			return req, "", err
		}
		spec, err := buildJobRequest(cmd, args, lookupEnv)
		if err != nil {
			return req, "", err
		}
		req.Kind, req.Spec = "job", spec
		return req, notice, nil
	}

	if err := refuseFlags(flags.Changed, jobOnlyScheduleFlags, "job schedules (a command after --)"); err != nil {
		return req, "", err
	}
	spec, err := buildInstanceRequest(cmd)
	if err != nil {
		return req, "", err
	}
	req.Kind, req.Spec = "instance", spec
	return req, notice, nil
}

func refuseFlags(changed func(string) bool, names []string, onlyFor string) error {
	for _, name := range names {
		if changed(name) {
			return fmt.Errorf("--%s only applies to %s", name, onlyFor)
		}
	}
	return nil
}

func newScheduleCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create (--at <time> | --cron <expr>) --max-runtime <d> [flags] [-- <command...>]",
		Short: "Start an instance or a job later, once or on a cron schedule",
		Long: `Start an instance or a job later: once with --at, or on a recurring --cron.

A command after -- makes it a job (like runcrate run); without one it starts an
instance (like runcrate instances create, which needs --name). --max-runtime is
required either way, so nothing a schedule starts keeps running (and billing)
unattended.

--at without an offset is the wall time in --tz, which defaults to this machine's
timezone, as does the --cron zone. Runs start within about 2 minutes of their time,
as you, in the current environment. Recurring runs are at least 15 minutes apart;
--overlap skip (the default) skips a run while the previous one is still going.`,
		Example: `  runcrate schedule create --cron '0 2 * * *' --tz Europe/London --name nightly-train \
    --gpu H100 --volume <volume-id> --max-runtime 2h -- python train.py
  runcrate schedule create --at '2026-10-21 09:00' --name morning-box --gpu A4000 --max-runtime 8h --idle-stop 30m`,
		RunE: runScheduleCreate,
	}
	f := cmd.Flags()
	f.String("at", "", "Run once at this time, e.g. '2026-10-21 02:00' (in --tz) or '2026-10-21T02:00:00+01:00'")
	f.String("cron", "", "Run on this 5-field cron in --tz, e.g. '0 2 * * *' (at least 15 minutes apart)")
	f.String("tz", "", "IANA timezone for --at and --cron, e.g. Europe/London (default this machine's)")
	f.String("overlap", "skip", "With --cron: skip a run while the previous one is still going, or allow it")
	f.String("name", "", "Name for the schedule and what it starts (required for an instance)")
	f.String("gpu", "", "GPU type, e.g. H100 or H100:2")
	f.Int("gpu-count", 1, "Number of GPUs (alternative to colon syntax)")
	f.String("region", "", "Preferred region")
	f.String("template", "", "OS template")
	f.String("volume", "", "Storage volume ID; its files are at /workspace")
	f.String("max-runtime", "", "Stop what it starts after this long (required): instances 5m to 168h, jobs 1m to 168h, e.g. 2h")
	addInstanceOnlyFlags(cmd)
	addJobOnlyFlags(cmd)
	return cmd
}

func runScheduleCreate(cmd *cobra.Command, args []string) error {
	req, notice, err := buildScheduleRequest(cmd, args, os.LookupEnv, time.Now())
	if err != nil {
		return err
	}
	cmd.SilenceUsage = true
	if notice != "" {
		fmt.Fprintln(os.Stderr, "Note: "+notice)
	}

	client, err := loadClient()
	if err != nil {
		return err
	}
	s, err := client.CreateSchedule(req)
	if err != nil {
		return fmt.Errorf("creating schedule: %w", err)
	}

	if jsonOutput {
		return printJSON(s)
	}
	fmt.Println()
	fmt.Printf("  Schedule created: %s\n", s.Name)
	printScheduleDetails(os.Stdout, s, time.Local)
	fmt.Println()
	fmt.Printf("  Pause it with:  runcrate schedule pause %s\n", s.ID)
	fmt.Printf("  Run it now:     runcrate schedule run-now %s\n", s.ID)
	fmt.Println()
	return nil
}

// ── display ─────────────────────────────────────────────────────────────────

// formatInstant renders an ISO time in loc, or "-".
func formatInstant(iso *string, loc *time.Location) string {
	if iso == nil || *iso == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, *iso)
	if err != nil {
		return *iso
	}
	return t.In(loc).Format(displayTimeLayout)
}

// scheduleWhen is the cron and its zone, or the one-off time in its zone.
func scheduleWhen(s *api.Schedule) string {
	if s.Cron != nil && *s.Cron != "" {
		return fmt.Sprintf("%s (%s)", *s.Cron, s.Timezone)
	}
	if s.RunAt != nil {
		loc, err := time.LoadLocation(s.Timezone)
		if err != nil {
			loc = time.UTC
		}
		t, err := time.Parse(time.RFC3339, *s.RunAt)
		if err != nil {
			return "once " + *s.RunAt
		}
		return fmt.Sprintf("once %s (%s)", t.In(loc).Format("2006-01-02 15:04"), loc)
	}
	return "-"
}

// scheduleLastResult maps last_status to plain words.
func scheduleLastResult(s *api.Schedule) string {
	if s.LastStatus == nil {
		return "-"
	}
	switch *s.LastStatus {
	case "launched":
		return "launched"
	case "failed":
		return "failed"
	case "skipped_budget":
		return "skipped: budget reached"
	case "skipped_overlap":
		return "skipped: still running"
	case "no_access":
		return "no access"
	case "credential_revoked":
		return "paused: key revoked"
	default:
		return *s.LastStatus
	}
}

// scheduleRunsAs is the user each run launches as and how: the last person to
// set up, retime or resume the schedule. Empty when the API sent no run_as.
func scheduleRunsAs(s *api.Schedule) string {
	if s.RunAs == nil || *s.RunAs == "" {
		return ""
	}
	switch s.RunAsCredential {
	case "user":
		return *s.RunAs + " (signed in)"
	case "api_key":
		return *s.RunAs + " through an API key"
	case "agent":
		return *s.RunAs + " through an agent sign-in"
	default:
		return *s.RunAs
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// renderScheduleTable writes the list table, next runs in loc.
func renderScheduleTable(w io.Writer, list []api.Schedule, loc *time.Location) error {
	tw := newTableTo(w)
	fmt.Fprintf(tw, "ID\tNAME\tKIND\tWHEN\tNEXT RUN\tLAST RESULT\tENABLED\n")
	for i := range list {
		s := &list[i]
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			s.ID, truncate(s.Name, 24), s.Kind, scheduleWhen(s),
			formatInstant(s.NextRunAt, loc), scheduleLastResult(s), yesNo(s.Enabled))
	}
	return tw.Flush()
}

func specString(spec map[string]any, key string) string {
	if v, ok := spec[key].(string); ok {
		return v
	}
	return ""
}

// printScheduleDetails writes the schedule's fields, times in loc.
func printScheduleDetails(w io.Writer, s *api.Schedule, loc *time.Location) {
	fmt.Fprintf(w, "  ID:           %s\n", s.ID)
	fmt.Fprintf(w, "  Kind:         %s\n", s.Kind)
	fmt.Fprintf(w, "  When:         %s\n", scheduleWhen(s))
	if s.Cron != nil {
		fmt.Fprintf(w, "  Overlap:      %s\n", s.Overlap)
	}
	fmt.Fprintf(w, "  Enabled:      %s\n", yesNo(s.Enabled))
	fmt.Fprintf(w, "  Next run:     %s\n", formatInstant(s.NextRunAt, loc))
	if cmd := specString(s.Spec, "command"); cmd != "" {
		fmt.Fprintf(w, "  Command:      %s\n", cmd)
	}
	if gpu := specString(s.Spec, "gpu_type"); gpu != "" {
		count, _ := s.Spec["gpu_count"].(float64)
		if count < 1 {
			count = 1
		}
		fmt.Fprintf(w, "  GPU:          %dx %s\n", int(count), gpu)
	} else if typeID := specString(s.Spec, "instance_type_id"); typeID != "" {
		fmt.Fprintf(w, "  Type:         %s\n", typeID)
	}
	fmt.Fprintf(w, "  Max runtime:  %s\n", formatMinutes(s.MaxRuntimeMinutes))
	if keys, ok := s.Spec["env_keys"].([]any); ok && len(keys) > 0 {
		names := make([]string, 0, len(keys))
		for _, k := range keys {
			names = append(names, fmt.Sprint(k))
		}
		fmt.Fprintf(w, "  Env:          %s\n", strings.Join(names, ", "))
	}
}

// ── commands ────────────────────────────────────────────────────────────────

var scheduleCmd = &cobra.Command{
	Use:     "schedule",
	Aliases: []string{"schedules"},
	Short:   "Start instances or jobs later, once or on a cron schedule",
}

var scheduleListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List schedules, newest first",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		list, err := client.ListSchedules()
		if err != nil {
			return fmt.Errorf("fetching schedules: %w", err)
		}

		if jsonOutput {
			return printJSON(list)
		}
		quiet, _ := cmd.Flags().GetBool("quiet")
		if quiet {
			for _, s := range list {
				fmt.Println(s.ID)
			}
			return nil
		}
		if len(list) == 0 {
			fmt.Println("No schedules found. Create one with: runcrate schedule create")
			return nil
		}
		return renderScheduleTable(os.Stdout, list, time.Local)
	},
}

var scheduleGetCmd = &cobra.Command{
	Use:     "get <id>",
	Aliases: []string{"info", "status"},
	Short:   "Show a schedule's details and last run",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		s, err := client.GetSchedule(args[0])
		if err != nil {
			return fmt.Errorf("fetching schedule: %w", err)
		}
		if jsonOutput {
			return printJSON(s)
		}

		fmt.Println()
		fmt.Printf("  Name:         %s\n", s.Name)
		printScheduleDetails(os.Stdout, s, time.Local)
		fmt.Printf("  Last run:     %s\n", formatInstant(s.LastRunAt, time.Local))
		fmt.Printf("  Last result:  %s\n", scheduleLastResult(s))
		if s.LastError != nil && *s.LastError != "" {
			fmt.Printf("  Last error:   %s\n", *s.LastError)
		}
		if s.LastInstanceID != nil {
			fmt.Printf("  Last instance: %s\n", *s.LastInstanceID)
		}
		if s.LastJobID != nil {
			fmt.Printf("  Last job:     %s\n", *s.LastJobID)
		}
		if runsAs := scheduleRunsAs(s); runsAs != "" {
			fmt.Printf("  Runs as:      %s\n", runsAs)
		}
		fmt.Printf("  Runs:         %d\n", s.RunCount)
		fmt.Printf("  Created:      %s\n", s.CreatedAt)
		fmt.Println()
		return nil
	},
}

// setScheduleEnabled pauses (false) or resumes (true) a schedule.
func setScheduleEnabled(client *api.Client, id string, enabled bool) (*api.Schedule, error) {
	return client.UpdateSchedule(id, api.UpdateScheduleRequest{Enabled: &enabled})
}

func newScheduleToggleCmd(use, short, verb string, enabled bool) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := loadClient()
			if err != nil {
				return err
			}
			s, err := setScheduleEnabled(client, args[0], enabled)
			if err != nil {
				return fmt.Errorf("%s schedule: %w", verb, err)
			}
			if jsonOutput {
				return printJSON(s)
			}
			if enabled {
				fmt.Printf("Resumed schedule %s. Next run: %s\n", s.Name, formatInstant(s.NextRunAt, time.Local))
			} else {
				fmt.Printf("Paused schedule %s. It will not run until you resume it.\n", s.Name)
			}
			return nil
		},
	}
}

var (
	schedulePauseCmd  = newScheduleToggleCmd("pause", "Stop a schedule from running until it is resumed", "pausing", false)
	scheduleResumeCmd = newScheduleToggleCmd("resume", "Turn a paused schedule back on", "resuming", true)
)

// confirm asks prompt on w and reads a y/yes answer from r. It returns
// io.EOF when there is nothing to read (no terminal).
func confirm(r io.Reader, w io.Writer, prompt string) (bool, error) {
	fmt.Fprint(w, prompt)
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(w)
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

var scheduleDeleteCmd = &cobra.Command{
	Use:     "delete <id>",
	Aliases: []string{"rm"},
	Short:   "Delete a schedule (what it already started keeps running)",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			s, err := client.GetSchedule(args[0])
			if err != nil {
				return fmt.Errorf("fetching schedule: %w", err)
			}
			ok, err := confirm(os.Stdin, os.Stderr,
				fmt.Sprintf("Delete schedule %s (%s)? What it already started keeps running. [y/N] ", s.Name, s.ID))
			if err != nil {
				return errors.New("no answer to the prompt; pass --yes to delete without asking")
			}
			if !ok {
				fmt.Println("Kept the schedule.")
				return nil
			}
		}

		res, err := client.DeleteSchedule(args[0])
		if err != nil {
			return fmt.Errorf("deleting schedule: %w", err)
		}
		if jsonOutput {
			return printJSON(res)
		}
		fmt.Println("Schedule deleted.")
		return nil
	},
}

var scheduleRunNowCmd = &cobra.Command{
	Use:   "run-now <id>",
	Short: "Start the schedule's instance or job now (its next run stays as it is)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		run, err := client.RunScheduleNow(args[0])
		if api.IsBudgetReached(err) {
			return errors.New(budgetReachedMessage)
		}
		if err != nil {
			return fmt.Errorf("running schedule: %w", err)
		}
		if jsonOutput {
			return printJSON(run)
		}
		switch {
		case run.Job != nil:
			fmt.Printf("Started job %s (%s). Follow it with: runcrate jobs logs -f %s\n", run.Job.Name, run.Job.ID, run.Job.ID)
		case run.Instance != nil:
			fmt.Printf("Started instance %s (%s). Track it with: runcrate instances status %s\n", run.Instance.Name, run.Instance.ID, run.Instance.ID)
		default:
			fmt.Printf("Ran schedule %s: %s\n", run.Schedule.Name, scheduleLastResult(&run.Schedule))
		}
		return nil
	},
}

func init() {
	scheduleCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output as JSON")

	scheduleListCmd.Flags().BoolP("quiet", "q", false, "Only show schedule IDs")
	scheduleDeleteCmd.Flags().BoolP("yes", "y", false, "Delete without asking")

	scheduleCmd.AddCommand(newScheduleCreateCmd())
	scheduleCmd.AddCommand(scheduleListCmd)
	scheduleCmd.AddCommand(scheduleGetCmd)
	scheduleCmd.AddCommand(schedulePauseCmd)
	scheduleCmd.AddCommand(scheduleResumeCmd)
	scheduleCmd.AddCommand(scheduleDeleteCmd)
	scheduleCmd.AddCommand(scheduleRunNowCmd)
	rootCmd.AddCommand(scheduleCmd)
}
