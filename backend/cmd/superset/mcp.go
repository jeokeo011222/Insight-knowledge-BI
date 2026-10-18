package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"superset/internal/mcp"
	"superset/internal/server"
)

// mcpCmd runs the MCP (Model Context Protocol) bridge over stdio.
//
// It is deliberately a separate process, not an HTTP mount: MCP clients
// (Claude Code, Cursor, any agent host) launch a stdio server and speak
// JSON-RPC to it. Keeping it out of the web server also means the bridge can
// only do what its configured token can do — it holds no extra privileges.
//
// Configuration is by environment variable so it drops straight into a client
// config file:
//
//	SUPERSET_MCP_URL       server root, default http://localhost:8088
//	SUPERSET_MCP_TOKEN     bearer token (preferred)
//	SUPERSET_MCP_USERNAME  used with PASSWORD when no token is set
//	SUPERSET_MCP_PASSWORD
func mcpCmd() *cobra.Command {
	var (
		baseURL  string
		token    string
		username string
		password string
	)
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve the BI bridge over MCP (stdio, JSON-RPC)",
		Long: "Exposes list_datasets / describe_dataset / dry_plan / semantic_query / run_sql to an " +
			"MCP client. Every call is made as the configured user, so permissions, row-level " +
			"security and masking still apply.",
		RunE: func(cmd *cobra.Command, args []string) error {
			url := firstNonEmpty(baseURL, os.Getenv("SUPERSET_MCP_URL"), os.Getenv("SUPERSET_URL"))
			if url == "" {
				url = "http://localhost:8088"
			}
			token := firstNonEmpty(token, os.Getenv("SUPERSET_MCP_TOKEN"))
			username := firstNonEmpty(username, os.Getenv("SUPERSET_MCP_USERNAME"))
			password := firstNonEmpty(password, os.Getenv("SUPERSET_MCP_PASSWORD"))
			if token == "" && (username == "" || password == "") {
				return fmt.Errorf("no credentials: set SUPERSET_MCP_TOKEN, or SUPERSET_MCP_USERNAME + SUPERSET_MCP_PASSWORD")
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			client := mcp.NewClient(url, token)
			if token == "" {
				if err := client.Login(ctx, username, password); err != nil {
					return fmt.Errorf("login failed: %w", err)
				}
			}
			// Fail fast on an unreachable server: a client that starts a bridge
			// which can never answer wastes a whole session before the first
			// error message arrives.
			if _, err := client.Get(ctx, "/info/", nil); err != nil {
				fmt.Fprintf(os.Stderr, "warning: server probe failed (%v); continuing anyway\n", err)
			}

			srv := mcp.NewServer(client, server.Version)
			// Logs must never go to stdout: stdout is the JSON-RPC channel, and a
			// stray line corrupts the stream for the client.
			fmt.Fprintf(os.Stderr, "superset mcp bridge ready (server=%s, transport=stdio)\n", url)
			return srv.Serve(ctx, os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&baseURL, "url", "", "server root, e.g. http://localhost:8088")
	cmd.Flags().StringVar(&token, "token", "", "bearer token")
	cmd.Flags().StringVar(&username, "username", "", "username (used when --token is empty)")
	cmd.Flags().StringVar(&password, "password", "", "password (used when --token is empty)")
	return cmd
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
