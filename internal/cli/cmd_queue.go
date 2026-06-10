package cli

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/singularityos-lab/torchio/internal/control"
)

// runQueue implements `torchio queue <action>`, a thin client of the daemon.
//
//	torchio queue ls [--printer NAME] [--all]
//	torchio queue cancel <job-id>
//	torchio queue history <job-id>
func runQueue(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: torchio queue ls|cancel|history [args]")
	}
	switch args[0] {
	case "ls":
		return queueList(args[1:])
	case "cancel":
		return queueCancel(args[1:])
	case "history":
		return queueHistory(args[1:])
	default:
		return fmt.Errorf("unknown queue action %q (ls, cancel, history)", args[0])
	}
}

func queueList(args []string) error {
	var printer string
	all := true
	// Minimal flag handling: --printer NAME, --active.
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--printer":
			if i+1 >= len(args) {
				return fmt.Errorf("--printer needs a name")
			}
			printer = args[i+1]
			i++
		case "--active":
			all = false
		default:
			return fmt.Errorf("unknown flag %q", args[i])
		}
	}

	client, err := dialDaemon()
	if err != nil {
		return err
	}
	defer client.Close()

	jobs, err := client.ListJobs(control.ListJobsArgs{Printer: printer, Active: !all})
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		fmt.Println("no jobs")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "JOB-ID\tNAME\tSTATE\tFORMAT\tSIZE\tCREATED")
	for _, j := range jobs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n",
			j.ID, j.Name, j.State, j.Format, j.Size, fmtMs(j.CreatedMs))
	}
	return w.Flush()
}

func queueCancel(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: torchio queue cancel <job-id>")
	}
	client, err := dialDaemon()
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.CancelJob(args[0]); err != nil {
		return err
	}
	fmt.Printf("cancelled %s\n", args[0])
	return nil
}

func queueHistory(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: torchio queue history <job-id>")
	}
	client, err := dialDaemon()
	if err != nil {
		return err
	}
	defer client.Close()

	h, err := client.JobHistory(args[0])
	if err != nil {
		return err
	}
	fmt.Printf("job %s (%s) state=%s\n", h.Job.ID, h.Job.Name, h.Job.State)
	if h.Job.Error != "" {
		fmt.Printf("error: %s\n", h.Job.Error)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "SEQ\tWHEN\tKIND\tFROM\tTO\tACTOR\tCAUSE")
	for _, e := range h.Events {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			e.Seq, fmtMs(e.AtMs), e.Kind, e.From, e.To, e.Actor, e.Cause)
	}
	return w.Flush()
}

// fmtMs renders unix-millis as a short local timestamp.
func fmtMs(ms int64) string {
	if ms == 0 {
		return "-"
	}
	return time.UnixMilli(ms).Format("15:04:05")
}
