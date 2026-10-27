package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

// confirmProduction asks the user, through the MCP client, to confirm a
// change on a production connection. Clients that cannot ask are refused:
// the user must see the exact SQL before it runs.
func confirmProduction(ctx context.Context, req *mcp.CallToolRequest, c *store.Connection, sqlText string, risk engine.Risk) error {
	shown := sqlText
	if len(shown) > 4000 {
		shown = shown[:4000] + "\n…"
	}

	err := elicitConfirmation(ctx, req, fmt.Sprintf("Run these %s statements on the production database %q?\n\n%s", risk, c.Name, shown), "Run on production")
	if err != nil && !canElicit(req) {
		return fmt.Errorf("%q is a production database; changes need the user's confirmation, which this MCP client cannot ask for. "+
			"Show the SQL to the user and let them run it in dataport", c.Name)
	}

	return err
}

func canElicit(req *mcp.CallToolRequest) bool {
	params := req.Session.InitializeParams()

	return params != nil && params.Capabilities != nil && params.Capabilities.Elicitation != nil
}

// elicitConfirmation asks the user, through the MCP client, to confirm what
// msg shows. Clients that cannot ask are refused.
func elicitConfirmation(ctx context.Context, req *mcp.CallToolRequest, msg, title string) error {
	refused := errors.New("this needs the user's confirmation, which this MCP client cannot ask for. " +
		"Show the SQL to the user and let them run it in dataport")

	if !canElicit(req) {
		return refused
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	res, err := req.Session.Elicit(ctx, &mcp.ElicitParams{
		Message: msg,
		RequestedSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"confirm": map[string]any{"type": "boolean", "title": title, "description": "Run exactly this SQL with these values"},
			},
			"required": []string{"confirm"},
		},
	})
	if err != nil {
		return refused
	}

	if res.Action != "accept" || res.Content["confirm"] != true {
		return errors.New("the user did not confirm the change; nothing was run")
	}

	return nil
}
