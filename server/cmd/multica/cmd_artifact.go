package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var artifactCmd = &cobra.Command{
	Use:   "artifact",
	Short: "Submit and inspect artifacts",
	Long: `Submit a file as an artifact owned by the current agent task, or read one back.

The server accepts these commands only while artifacts_v1 is on. While it is
off they fail with the server's 404. Attachment upload and download are
unchanged.

A task token can only create, update, get, and check status for artifacts
owned by that token's task. It cannot read or write the rest of the workspace.`,
}

var artifactCreateCmd = &cobra.Command{
	Use:     "create",
	Short:   "Create an artifact from a file and commit the first revision",
	Example: `  multica artifact create --task "$MULTICA_TASK_ID" --file ./report.md --title "Weekly report"`,
	RunE:    runArtifactCreate,
}

var artifactUpdateCmd = &cobra.Command{
	Use:   "update <artifact-id>",
	Short: "Commit a new revision of an artifact",
	Args:  exactArgs(1),
	RunE:  runArtifactUpdate,
}

var artifactGetCmd = &cobra.Command{
	Use:   "get <artifact-id>",
	Short: "Print artifact metadata",
	Args:  exactArgs(1),
	RunE:  runArtifactGet,
}

var artifactStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Print the state of an artifact submit operation",
	RunE:  runArtifactStatus,
}

func init() {
	artifactCmd.AddCommand(artifactCreateCmd)
	artifactCmd.AddCommand(artifactUpdateCmd)
	artifactCmd.AddCommand(artifactGetCmd)
	artifactCmd.AddCommand(artifactStatusCmd)

	artifactCreateCmd.Flags().String("task", "", "Task id (defaults to MULTICA_TASK_ID)")
	artifactCreateCmd.Flags().String("file", "", "Local file to submit")
	artifactCreateCmd.Flags().String("title", "", "Display title")
	artifactUpdateCmd.Flags().String("base", "", "Current revision id this update is based on")
	artifactUpdateCmd.Flags().String("file", "", "Local file to submit")
	artifactGetCmd.Flags().String("revision", "", "Revision id (defaults to the current head)")
	artifactStatusCmd.Flags().String("operation", "", "Operation id returned by create or update")
}

func runArtifactCreate(cmd *cobra.Command, _ []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	taskID, _ := cmd.Flags().GetString("task")
	if taskID == "" {
		taskID = client.TaskID
	}
	if taskID == "" {
		return fmt.Errorf("no task in context: pass --task or set MULTICA_TASK_ID")
	}
	if client.TaskID != "" && !strings.EqualFold(client.TaskID, taskID) {
		return fmt.Errorf("task_id must match the request's task token")
	}
	path, _ := cmd.Flags().GetString("file")
	title, _ := cmd.Flags().GetString("title")
	if path == "" || title == "" {
		return fmt.Errorf("--file and --title are required")
	}
	data, err := readArtifactFile(path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cli.AtLeastAPITimeout(60*time.Second))
	defer cancel()
	result, err := client.CreateArtifact(ctx, taskID, title, path, data)
	if err != nil {
		return fmt.Errorf("create artifact: %w", err)
	}
	return cli.PrintJSON(os.Stdout, result)
}

func runArtifactUpdate(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	base, _ := cmd.Flags().GetString("base")
	path, _ := cmd.Flags().GetString("file")
	if base == "" || path == "" {
		return fmt.Errorf("--base and --file are required")
	}
	data, err := readArtifactFile(path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cli.AtLeastAPITimeout(60*time.Second))
	defer cancel()
	result, err := client.UpdateArtifact(ctx, args[0], base, path, data)
	if err != nil {
		return fmt.Errorf("update artifact: %w", err)
	}
	return cli.PrintJSON(os.Stdout, result)
}

func runArtifactGet(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	revision, _ := cmd.Flags().GetString("revision")
	ctx, cancel := context.WithTimeout(context.Background(), cli.AtLeastAPITimeout(30*time.Second))
	defer cancel()
	result, err := client.GetArtifact(ctx, args[0], revision)
	if err != nil {
		return fmt.Errorf("get artifact: %w", err)
	}
	return cli.PrintJSON(os.Stdout, result)
}

func runArtifactStatus(cmd *cobra.Command, _ []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	operationID, _ := cmd.Flags().GetString("operation")
	if operationID == "" {
		return fmt.Errorf("--operation is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), cli.AtLeastAPITimeout(30*time.Second))
	defer cancel()
	result, err := client.ArtifactOperationStatus(ctx, operationID)
	if err != nil {
		return fmt.Errorf("artifact status: %w", err)
	}
	return cli.PrintJSON(os.Stdout, result)
}

func readArtifactFile(path string) ([]byte, error) {
	if isHTTPURL(path) {
		return nil, fmt.Errorf("artifact file must be a local path, not a URL: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", path, err)
	}
	return data, nil
}
