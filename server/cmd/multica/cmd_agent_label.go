package main

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// multica agent label {list|add|remove} — manages the labels attached to a
// specific agent. The label itself is managed via `multica label ...`.

var agentLabelCmd = &cobra.Command{
	Use:   "label",
	Short: "Manage labels on an agent",
}

var agentLabelListCmd = &cobra.Command{
	Use:   "list <agent-id>",
	Short: "List labels on an agent",
	Args:  exactArgs(1),
	RunE:  runAgentLabelList,
}

var agentLabelAddCmd = &cobra.Command{
	Use:   "add <agent-id> <label-id>",
	Short: "Attach a label to an agent",
	Args:  exactArgs(2),
	RunE:  runAgentLabelAdd,
}

var agentLabelRemoveCmd = &cobra.Command{
	Use:   "remove <agent-id> <label-id>",
	Short: "Remove a label from an agent",
	Args:  exactArgs(2),
	RunE:  runAgentLabelRemove,
}

func init() {
	agentLabelCmd.AddCommand(agentLabelListCmd)
	agentLabelCmd.AddCommand(agentLabelAddCmd)
	agentLabelCmd.AddCommand(agentLabelRemoveCmd)

	agentLabelListCmd.Flags().String("output", "table", "Output format: table or json")
	agentLabelAddCmd.Flags().String("output", "table", "Output format: table or json")
	agentLabelRemoveCmd.Flags().String("output", "table", "Output format: table or json")
	agentLabelListCmd.Flags().Bool("full-id", false, "Show full UUIDs in table output")
	agentLabelAddCmd.Flags().Bool("full-id", false, "Show full UUIDs in table output")
	agentLabelRemoveCmd.Flags().Bool("full-id", false, "Show full UUIDs in table output")

	// Register under the top-level `agent` command.
	agentCmd.AddCommand(agentLabelCmd)
}

func runAgentLabelList(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var result map[string]any
	if err := client.GetJSON(ctx, "/api/agents/"+url.PathEscape(args[0])+"/labels", &result); err != nil {
		return fmt.Errorf("list agent labels: %w", err)
	}
	labelsRaw, _ := result["labels"].([]any)

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, labelsRaw)
	}
	fullID, _ := cmd.Flags().GetBool("full-id")
	printLabelTable(labelsRaw, fullID)
	return nil
}

func runAgentLabelAdd(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	labelRef, err := resolveLabelID(ctx, client, args[1])
	if err != nil {
		return fmt.Errorf("resolve label: %w", err)
	}

	body := map[string]any{"label_id": labelRef.ID}
	var result map[string]any
	if err := client.PostJSON(ctx, "/api/agents/"+url.PathEscape(args[0])+"/labels", body, &result); err != nil {
		return fmt.Errorf("attach label: %w", err)
	}
	labelsRaw, _ := result["labels"].([]any)

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, labelsRaw)
	}
	fullID, _ := cmd.Flags().GetBool("full-id")
	printLabelTable(labelsRaw, fullID)
	return nil
}

func runAgentLabelRemove(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	labelRef, err := resolveLabelID(ctx, client, args[1])
	if err != nil {
		return fmt.Errorf("resolve label: %w", err)
	}

	if err := client.DeleteJSON(ctx, "/api/agents/"+url.PathEscape(args[0])+"/labels/"+labelRef.ID); err != nil {
		return fmt.Errorf("detach label: %w", err)
	}

	var result map[string]any
	output, _ := cmd.Flags().GetString("output")
	if err := client.GetJSON(ctx, "/api/agents/"+url.PathEscape(args[0])+"/labels", &result); err != nil {
		if output == "json" {
			return cli.PrintJSON(os.Stdout, map[string]any{"detached": true})
		}
		fmt.Fprintln(os.Stdout, "Label detached.")
		return nil
	}
	labelsRaw, _ := result["labels"].([]any)
	if output == "json" {
		return cli.PrintJSON(os.Stdout, labelsRaw)
	}
	fullID, _ := cmd.Flags().GetBool("full-id")
	printLabelTable(labelsRaw, fullID)
	return nil
}
