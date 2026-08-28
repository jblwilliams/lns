package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"lns/internal/projectplan"
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Show what LNS would run without changing anything",
	Long: `Build a deterministic local-development plan from repository signals or an
explicit lns.json. This command does not write files, reserve ports, start
processes, prune leases, or reload the proxy.`,
	Example: `  lns plan
  lns plan --json
  lns plan --path ../my-project`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, _ := cmd.Flags().GetString("path")
		jsonOutput, _ := cmd.Flags().GetBool("json")
		return runPlan(cmd.OutOrStdout(), root, jsonOutput)
	},
}

func init() {
	rootCmd.AddCommand(planCmd)
	planCmd.Flags().StringP("path", "p", ".", "Project root directory")
	planCmd.Flags().Bool("json", false, "Write the plan as JSON with no surrounding text")
}

func runPlan(output io.Writer, root string, jsonOutput bool) error {
	plan, err := projectplan.Build(root)
	if err != nil {
		return err
	}
	return writePlan(output, plan, jsonOutput)
}

func writePlan(output io.Writer, plan projectplan.Plan, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(plan)
	}

	if _, err := fmt.Fprintf(output, "%s (%s)\n%s\n\n", plan.Project.Name, plan.Project.Source, plan.Project.Root); err != nil {
		return err
	}
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "SERVICE\tCOMMAND\tLOCAL NAME\tPORT"); err != nil {
		return err
	}
	for _, service := range plan.Services {
		command := strings.Join(service.Command, " ")
		if service.Script != "" {
			command = "package script " + service.Script
		}
		port := string(service.Port.Strategy)
		if service.Port.Strategy == projectplan.PortFixed {
			port = fmt.Sprintf("%d", service.Port.Fixed)
		}
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", service.Name, command, service.URL, port); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	for _, warning := range plan.Warnings {
		if _, err := fmt.Fprintf(output, "\nwarning: %s\n", warning.Message); err != nil {
			return err
		}
		if warning.Recovery != "" {
			if _, err := fmt.Fprintf(output, "next: %s\n", warning.Recovery); err != nil {
				return err
			}
		}
	}
	return nil
}
