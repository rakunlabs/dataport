package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/dataport/internal/config"
	"github.com/rakunlabs/dataport/internal/store"
)

// The MCP endpoint lets AI agents work with the databases shared with a
// user. A client signs in with one of the user's access tokens and gets
// exactly what the user gets in the workspace: the same grants, read-only
// enforcement, production confirmation and query log.

const mcpToolPrefix = "dataport_"

type mcpCaller struct {
	user  *store.User
	token *store.AccessToken
	// scope is the one connection the caller may use: the one of the
	// endpoint (/mcp/<name>) or the one the token was made for. Empty
	// allows every connection of the user.
	scope string
}

func (s *Server) mcpHandler() http.Handler {
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		caller := mcpCallerFrom(r.Context())
		if caller == nil {
			return nil
		}

		return s.newMCPServer(caller)
	}, &mcp.StreamableHTTPOptions{
		SessionTimeout:               30 * time.Minute,
		Stateless:                    s.cfg.MCP.Stateless,
		PropagateRequestCancellation: true,
		// Every request carries a bearer token, which a page tricked into
		// calling the endpoint does not have; a reverse proxy on the same
		// host would otherwise be refused.
		DisableLocalhostProtection: true,
	})

	return limitBody(4 << 20)(withCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, status, msg := s.mcpAuthenticate(r)
		if caller == nil {
			// The header sends OAuth clients to the metadata, where they
			// learn how to sign in (RFC 9728).
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata=%q, scope="mcp"`, s.resourceMetadataURL(r)))
			http.Error(w, msg, status)

			return
		}

		info := &auth.TokenInfo{UserID: caller.user.ID, Extra: map[string]any{"caller": caller}}
		if caller.token.ExpiresAt > 0 {
			info.Expiration = time.UnixMilli(caller.token.ExpiresAt)
		}

		// The SDK reads the caller from its own context key, which only its
		// middleware sets; the token was checked above.
		verified := func(context.Context, string, *http.Request) (*auth.TokenInfo, error) { return info, nil }
		auth.RequireBearerToken(verified, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(handler).ServeHTTP(w, r)
	})))
}

// mcpAuthenticate checks the bearer token of a request and the connection
// of its endpoint. The token is checked again on every request, so a
// revoked token or a disabled user stops at once.
func (s *Server) mcpAuthenticate(r *http.Request) (*mcpCaller, int, string) {
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "bearer") {
		return nil, http.StatusUnauthorized, "sign in: send an access token as a bearer token"
	}

	user, t, err := s.auth.AuthenticateToken(r.Context(), fields[1])
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			slog.Error("mcp authenticate", "error", err)

			return nil, http.StatusInternalServerError, "cannot check the token"
		}

		return nil, http.StatusUnauthorized, "unknown or expired token"
	}

	caller := &mcpCaller{user: user, token: t, scope: t.ConnectionID}

	name := r.PathValue("conn")
	if name == "" {
		return caller, 0, ""
	}

	g, err := s.grantByName(r, user, name)
	if err != nil {
		return nil, http.StatusNotFound, "no connection " + name + " for you"
	}

	if t.ConnectionID != "" && t.ConnectionID != g.Connection.ID {
		return nil, http.StatusForbidden, "this token is for another connection"
	}

	caller.scope = g.Connection.ID

	return caller, 0, ""
}

func mcpCallerFrom(ctx context.Context) *mcpCaller {
	info := auth.TokenInfoFromContext(ctx)
	if info == nil {
		return nil
	}

	c, _ := info.Extra["caller"].(*mcpCaller)

	return c
}

// callerOf returns the caller of a tool call. The token is checked again on
// every HTTP request, so a revoked token or a disabled user stops at once.
func callerOf(req *mcp.CallToolRequest) (*mcpCaller, error) {
	if req.Extra != nil && req.Extra.TokenInfo != nil {
		if c, _ := req.Extra.TokenInfo.Extra["caller"].(*mcpCaller); c != nil {
			return c, nil
		}
	}

	return nil, errors.New("not signed in")
}

func (s *Server) newMCPServer(caller *mcpCaller) *mcp.Server {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Users notebooks are not open to get no notebook tools; each tool
	// still checks, as the setting may change during a session.
	notebooks := s.notebooksAllowed(ctx, caller.user)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "dataport",
		Title:   "dataport",
		Version: config.Service,
	}, &mcp.ServerOptions{
		Instructions: s.mcpInstructions(caller, notebooks),
	})

	s.registerMCPTools(server, caller.token.ReadOnly, notebooks)

	return server
}

func (s *Server) mcpInstructions(caller *mcpCaller, notebooks bool) string {
	text := mcpInstructions(s.mcpCfg(), caller.token.ReadOnly, notebooks)
	if caller.scope == "" {
		return text
	}

	name := caller.scope

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if c, err := s.store.GetConnection(ctx, caller.scope); err == nil {
		name = c.Name + " (" + c.DBType + ")"
	}

	return text + "\nThis endpoint serves one connection, " + name + "; the connection argument of the tools may be omitted.\n"
}

func mcpInstructions(cfg config.MCP, readOnly, notebooks bool) string {
	var b strings.Builder

	b.WriteString("dataport gives you the databases an administrator shared with the user who created your access token. ")
	b.WriteString("You act as that user: their grants, read-only limits and production rules apply, and every statement is written to an audit log.\n\n")
	b.WriteString("Workflow:\n")
	b.WriteString("1. Call " + mcpToolPrefix + "list_connections to see the databases and their SQL dialect.\n")
	b.WriteString("2. Call " + mcpToolPrefix + "schema_context for a compact overview of a schema, or " +
		mcpToolPrefix + "list_tables and " + mcpToolPrefix + "describe_table for a few tables.\n")
	b.WriteString("3. Write SQL against the real names you read and run it with " + mcpToolPrefix + "query.\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Never invent table or column names; inspect the schema first.\n")
	b.WriteString("- Write SQL in the dialect of the connection's db_type.\n")
	fmt.Fprintf(&b, "- Results are capped at %d rows per statement and long values are shortened. ", cfg.MaxRows)
	b.WriteString("If truncated is true you see part of the result: narrow it with WHERE, LIMIT or aggregates.\n")
	b.WriteString("- Each call runs in one transaction managed by dataport; BEGIN, COMMIT and ROLLBACK are refused.\n")

	if readOnly {
		b.WriteString("- This token is read-only: no write tool is available. If the user needs a change, tell them to make it in dataport.\n")
	} else {
		b.WriteString("- " + mcpToolPrefix + "query only reads. Use " + mcpToolPrefix + "execute for changes, only where the connection's access allows them: ")
		b.WriteString("write allows changing data, ddl also changing the schema, read neither. ")
		b.WriteString("and only after the user asked for the change. Always scope UPDATE and DELETE with a specific WHERE clause.\n")
		b.WriteString("- On production connections the user is asked to confirm the exact SQL before it runs.\n")
	}

	b.WriteString("- A refused statement is a policy decision; do not try to work around it.\n")

	if !notebooks {
		return b.String()
	}

	b.WriteString("\nNotebooks: " + mcpToolPrefix + "list_notebooks, " + mcpToolPrefix + "get_notebook and " + mcpToolPrefix + "run_notebook work with the user's saved SQL notebooks; ")
	b.WriteString("cells run in dependency order and each is its own transaction. Read a notebook before changing or running it. ")
	b.WriteString("In template cells bind every value with {{ param ... }}; never quote or print values into SQL.\n")

	if !readOnly {
		b.WriteString("Only save, publish, share or delete notebooks when the user asked for it; " + mcpToolPrefix + "save_notebook replaces the whole document and needs the current version.\n")
	}

	return b.String()
}
