package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"lns/internal/config"
	"lns/internal/projectconfig"
)

const (
	resetScopeRepo   = "repo"
	resetScopeGlobal = "global"
	resetScopeAll    = "all"
)

type resetSummary struct {
	repoPath           string
	globalConfigDir    string
	repoRemoved        bool
	globalRemoved      bool
	repoAlreadyClean   bool
	globalAlreadyClean bool
}

var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Delete repo-local and/or global lns state",
	Run: func(cmd *cobra.Command, args []string) {
		scope, err := selectedResetScope(cmd)
		if err != nil {
			printError("%v", err)
			os.Exit(1)
		}

		root := ""
		if scope == resetScopeRepo || scope == resetScopeAll {
			root, err = resolveRoot(cmd)
			if err != nil {
				printError("%v", err)
				os.Exit(1)
			}
		}

		if !isTerminal(os.Stdin) {
			printError("reset requires an interactive terminal")
			fmt.Println()
			fmt.Print(renderResetPlan(scope, root))
			os.Exit(1)
		}

		fmt.Println()
		fmt.Print(renderResetPlan(scope, root))
		reader := bufio.NewReader(os.Stdin)
		approved, err := promptResetApproval(reader)
		if err != nil {
			printError("%v", err)
			os.Exit(1)
		}
		if !approved {
			color.Yellow("Reset cancelled.")
			return
		}

		if scope == resetScopeGlobal || scope == resetScopeAll {
			settings := loadSettingsOrDefault()
			if isTCPListening(settings.AdminAddr) {
				printError("LNS proxy appears to still be running at %s", settings.AdminAddr)
				fmt.Println("Run `lns stop` first, then rerun reset.")
				os.Exit(1)
			}
		}

		summary, err := executeReset(scope, root)
		if err != nil {
			printError("%v", err)
			os.Exit(1)
		}

		printSuccess("Reset completed")
		if summary.repoPath != "" {
			if summary.repoRemoved {
				fmt.Printf("  Removed repo config: %s\n", summary.repoPath)
			} else if summary.repoAlreadyClean {
				fmt.Printf("  Repo already clean: %s\n", summary.repoPath)
			}
		}
		if summary.globalConfigDir != "" {
			if summary.globalRemoved {
				fmt.Printf("  Removed global state: %s\n", summary.globalConfigDir)
			} else if summary.globalAlreadyClean {
				fmt.Printf("  Global state already clean: %s\n", summary.globalConfigDir)
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(resetCmd)
	resetCmd.Flags().Bool("repo", false, "Delete repo-local lns.json in the current repo")
	resetCmd.Flags().Bool("global", false, "Delete global lns state under ~/.lns")
	resetCmd.Flags().Bool("all", false, "Delete both repo-local and global lns state")
	resetCmd.Flags().StringP("path", "p", "", "Project root directory for --repo or --all (default: current directory)")
}

func selectedResetScope(cmd *cobra.Command) (string, error) {
	repoFlag, _ := cmd.Flags().GetBool("repo")
	globalFlag, _ := cmd.Flags().GetBool("global")
	allFlag, _ := cmd.Flags().GetBool("all")

	count := 0
	scope := ""
	if repoFlag {
		count++
		scope = resetScopeRepo
	}
	if globalFlag {
		count++
		scope = resetScopeGlobal
	}
	if allFlag {
		count++
		scope = resetScopeAll
	}

	if count != 1 {
		return "", fmt.Errorf("choose exactly one reset scope: --repo, --global, or --all")
	}
	return scope, nil
}

func renderResetPlan(scope, root string) string {
	lines := []string{
		color.New(color.FgYellow, color.Bold).Sprint("Reset plan"),
	}

	switch scope {
	case resetScopeRepo:
		lines = append(lines,
			fmt.Sprintf("  - delete repo config: %s", projectconfig.Path(root)),
			"",
			`Type "confirm" to continue, or anything else to cancel.`,
		)
	case resetScopeGlobal:
		lines = append(lines,
			fmt.Sprintf("  - delete global config dir: %s", config.GetConfigDir()),
			"  - this includes settings.json, registry.json, runtime.json, Caddyfile, and projects/*.caddy",
			"",
			`Type "confirm" to continue, or anything else to cancel.`,
		)
	case resetScopeAll:
		lines = append(lines,
			fmt.Sprintf("  - delete repo config: %s", projectconfig.Path(root)),
			fmt.Sprintf("  - delete global config dir: %s", config.GetConfigDir()),
			"  - this includes settings.json, registry.json, runtime.json, Caddyfile, and projects/*.caddy",
			"",
			`Type "confirm" to continue, or anything else to cancel.`,
		)
	}

	return strings.Join(lines, "\n") + "\n"
}

func promptResetApproval(reader *bufio.Reader) (bool, error) {
	fmt.Printf("> ")
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, err
	}
	return strings.ToLower(strings.TrimSpace(line)) == "confirm", nil
}

func executeReset(scope, root string) (resetSummary, error) {
	summary := resetSummary{
		globalConfigDir: config.GetConfigDir(),
	}

	if scope == resetScopeRepo || scope == resetScopeAll {
		summary.repoPath = projectconfig.Path(root)
		if err := os.Remove(summary.repoPath); err != nil {
			if os.IsNotExist(err) {
				summary.repoAlreadyClean = true
			} else {
				return summary, fmt.Errorf("remove repo config %s: %w", summary.repoPath, err)
			}
		} else {
			summary.repoRemoved = true
		}
	}

	if scope == resetScopeGlobal || scope == resetScopeAll {
		if _, err := os.Stat(summary.globalConfigDir); err != nil {
			if os.IsNotExist(err) {
				summary.globalAlreadyClean = true
			} else {
				return summary, fmt.Errorf("inspect global config dir %s: %w", summary.globalConfigDir, err)
			}
		} else if err := os.RemoveAll(summary.globalConfigDir); err != nil {
			return summary, fmt.Errorf("remove global config dir %s: %w", summary.globalConfigDir, err)
		} else {
			summary.globalRemoved = true
		}
	}

	return summary, nil
}
