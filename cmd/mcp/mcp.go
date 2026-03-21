// Package mcp provides the `ctx0 mcp` command which runs a stdio MCP server
// exposing Memory and Agenda tools.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"

	"context0/internal/agenda"
	"context0/internal/memory"
)

// NewCmd returns the `mcp` sub-command that starts the stdio MCP server.
func NewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Start the MCP stdio server (Memory + Agenda tools)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPServer()
		},
	}
}

func projectPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func runMCPServer() error {
	s := server.NewMCPServer(
		"context0",
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	registerMemoryTools(s)
	registerAgendaTools(s)

	return server.ServeStdio(s)
}

// --- Memory tools ---

func registerMemoryTools(s *server.MCPServer) {
	// save_memory
	s.AddTool(mcpgo.NewTool("save_memory",
		mcpgo.WithDescription("Persist a memory (category, topic, content)"),
		mcpgo.WithString("category", mcpgo.Description("Category e.g. architecture, fix, feature"), mcpgo.Required()),
		mcpgo.WithString("topic", mcpgo.Description("Short topic title"), mcpgo.Required()),
		mcpgo.WithString("content", mcpgo.Description("Memory content"), mcpgo.Required()),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		cat := req.GetString("category", "")
		topic := req.GetString("topic", "")
		content := req.GetString("content", "")

		eng, err := memory.New(projectPath())
		if err != nil {
			return mcpError(err), nil
		}
		defer eng.Close()

		doc, err := eng.SaveMemory(cat, topic, content)
		if err != nil {
			return mcpError(err), nil
		}
		return mcpJSON(map[string]any{"id": doc.ID, "topic": doc.Topic, "category": doc.Category}), nil
	})

	// query_memory
	s.AddTool(mcpgo.NewTool("query_memory",
		mcpgo.WithDescription("Hybrid search (FTS5 + vector) on memories"),
		mcpgo.WithString("query", mcpgo.Description("Search query"), mcpgo.Required()),
		mcpgo.WithNumber("top_k", mcpgo.Description("Number of results (default 3)")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		query := req.GetString("query", "")
		topK := 3
		if v, ok := req.GetArguments()["top_k"]; ok && v != nil {
			topK = int(v.(float64))
		}

		eng, err := memory.New(projectPath())
		if err != nil {
			return mcpError(err), nil
		}
		defer eng.Close()

		results, err := eng.QueryMemory(query, topK)
		if err != nil {
			return mcpError(err), nil
		}
		return mcpJSON(results), nil
	})

	// update_memory
	s.AddTool(mcpgo.NewTool("update_memory",
		mcpgo.WithDescription("Modify an existing memory by ID"),
		mcpgo.WithNumber("id", mcpgo.Description("Memory ID"), mcpgo.Required()),
		mcpgo.WithString("category", mcpgo.Description("New category (omit to keep existing)")),
		mcpgo.WithString("topic", mcpgo.Description("New topic (omit to keep existing)")),
		mcpgo.WithString("content", mcpgo.Description("New content (omit to keep existing)")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
		idF, _ := args["id"].(float64)
		id := int64(idF)
		cat := req.GetString("category", "")
		topic := req.GetString("topic", "")
		content := req.GetString("content", "")

		eng, err := memory.New(projectPath())
		if err != nil {
			return mcpError(err), nil
		}
		defer eng.Close()

		doc, err := eng.UpdateMemory(id, cat, topic, content)
		if err != nil {
			return mcpError(err), nil
		}
		return mcpJSON(map[string]any{"id": doc.ID, "topic": doc.Topic, "category": doc.Category}), nil
	})

	// delete_memory
	s.AddTool(mcpgo.NewTool("delete_memory",
		mcpgo.WithDescription("Remove a memory by ID"),
		mcpgo.WithNumber("id", mcpgo.Description("Memory ID"), mcpgo.Required()),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
		idF, _ := args["id"].(float64)
		id := int64(idF)

		eng, err := memory.New(projectPath())
		if err != nil {
			return mcpError(err), nil
		}
		defer eng.Close()

		if err := eng.DeleteMemory(id); err != nil {
			return mcpError(err), nil
		}
		return mcpJSON(map[string]any{"status": "deleted", "id": id}), nil
	})
}

// --- Agenda tools ---

func registerAgendaTools(s *server.MCPServer) {
	// create_agenda
	s.AddTool(mcpgo.NewTool("create_agenda",
		mcpgo.WithDescription("Create a new plan with tasks"),
		mcpgo.WithString("title", mcpgo.Description("Agenda title")),
		mcpgo.WithString("description", mcpgo.Description("Agenda description")),
		mcpgo.WithString("tasks", mcpgo.Description(`JSON array of tasks: [{"Details":"...","IsOptional":false,"AcceptanceGuard":"..."}]`), mcpgo.Required()),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		title := req.GetString("title", "")
		desc := req.GetString("description", "")
		tasksJSON := req.GetString("tasks", "")

		var taskInputs []agenda.TaskInput
		if tasksJSON != "" {
			if err := json.Unmarshal([]byte(tasksJSON), &taskInputs); err != nil {
				return mcpError(fmt.Errorf("tasks JSON parse error: %w", err)), nil
			}
		}

		eng, err := agenda.New(projectPath())
		if err != nil {
			return mcpError(err), nil
		}
		defer eng.Close()

		id, err := eng.CreateAgenda(title, desc, taskInputs)
		if err != nil {
			return mcpError(err), nil
		}
		return mcpJSON(map[string]any{"agenda_id": id}), nil
	})

	// list_agendas
	s.AddTool(mcpgo.NewTool("list_agendas",
		mcpgo.WithDescription("List all (or active-only) agendas"),
		mcpgo.WithBoolean("active_only", mcpgo.Description("If true (default), return only active agendas")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		activeOnly := true
		if v, ok := req.GetArguments()["active_only"]; ok && v != nil {
			activeOnly, _ = v.(bool)
		}

		eng, err := agenda.New(projectPath())
		if err != nil {
			return mcpError(err), nil
		}
		defer eng.Close()

		agendas, err := eng.ListAgendas(activeOnly)
		if err != nil {
			return mcpError(err), nil
		}
		return mcpJSON(agendas), nil
	})

	// get_agenda
	s.AddTool(mcpgo.NewTool("get_agenda",
		mcpgo.WithDescription("Get full detail of one agenda including tasks"),
		mcpgo.WithNumber("agenda_id", mcpgo.Description("Agenda ID"), mcpgo.Required()),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		idF, _ := req.GetArguments()["agenda_id"].(float64)
		id := int64(idF)

		eng, err := agenda.New(projectPath())
		if err != nil {
			return mcpError(err), nil
		}
		defer eng.Close()

		a, err := eng.GetAgenda(id)
		if err != nil {
			return mcpError(err), nil
		}
		return mcpJSON(a), nil
	})

	// search_agendas
	s.AddTool(mcpgo.NewTool("search_agendas",
		mcpgo.WithDescription("FTS5 search agendas by title/description"),
		mcpgo.WithString("query", mcpgo.Description("Search query"), mcpgo.Required()),
		mcpgo.WithNumber("limit", mcpgo.Description("Max results (default 10)")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		query := req.GetString("query", "")
		limit := 10
		if v, ok := req.GetArguments()["limit"]; ok && v != nil {
			limit = int(v.(float64))
		}

		eng, err := agenda.New(projectPath())
		if err != nil {
			return mcpError(err), nil
		}
		defer eng.Close()

		results, err := eng.SearchAgendas(query, limit)
		if err != nil {
			return mcpError(err), nil
		}
		return mcpJSON(results), nil
	})

	// update_task
	s.AddTool(mcpgo.NewTool("update_task",
		mcpgo.WithDescription("Mark a task completed or pending"),
		mcpgo.WithNumber("task_id", mcpgo.Description("Task ID"), mcpgo.Required()),
		mcpgo.WithBoolean("is_completed", mcpgo.Description("True to complete, false to reopen"), mcpgo.Required()),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
		taskIDf, _ := args["task_id"].(float64)
		taskID := int64(taskIDf)
		isCompleted, _ := args["is_completed"].(bool)

		eng, err := agenda.New(projectPath())
		if err != nil {
			return mcpError(err), nil
		}
		defer eng.Close()

		if err := eng.UpdateTask(taskID, isCompleted); err != nil {
			return mcpError(err), nil
		}
		return mcpJSON(map[string]any{"status": "ok", "task_id": strconv.FormatInt(taskID, 10)}), nil
	})

	// update_agenda
	s.AddTool(mcpgo.NewTool("update_agenda",
		mcpgo.WithDescription("Edit metadata, add tasks, or deactivate an agenda"),
		mcpgo.WithNumber("agenda_id", mcpgo.Description("Agenda ID"), mcpgo.Required()),
		mcpgo.WithString("title", mcpgo.Description("New title (omit to keep)")),
		mcpgo.WithString("description", mcpgo.Description("New description (omit to keep)")),
		mcpgo.WithBoolean("is_active", mcpgo.Description("Set to false to deactivate (irreversible)")),
		mcpgo.WithString("new_tasks", mcpgo.Description(`JSON array of tasks to append`)),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
		agendaIDf, _ := args["agenda_id"].(float64)
		agendaID := int64(agendaIDf)
		title := req.GetString("title", "")
		desc := req.GetString("description", "")

		var isActive *bool
		if v, ok := args["is_active"]; ok && v != nil {
			b, _ := v.(bool)
			isActive = &b
		}

		var newTasks []agenda.TaskInput
		if v := req.GetString("new_tasks", ""); v != "" {
			if err := json.Unmarshal([]byte(v), &newTasks); err != nil {
				return mcpError(fmt.Errorf("new_tasks JSON parse error: %w", err)), nil
			}
		}

		eng, err := agenda.New(projectPath())
		if err != nil {
			return mcpError(err), nil
		}
		defer eng.Close()

		if err := eng.UpdateAgenda(agendaID, title, desc, isActive, newTasks); err != nil {
			return mcpError(err), nil
		}
		return mcpJSON(map[string]any{"status": "ok"}), nil
	})

	// delete_agenda
	s.AddTool(mcpgo.NewTool("delete_agenda",
		mcpgo.WithDescription("Delete an inactive agenda (irreversible)"),
		mcpgo.WithNumber("agenda_id", mcpgo.Description("Agenda ID"), mcpgo.Required()),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		idF, _ := req.GetArguments()["agenda_id"].(float64)
		agendaID := int64(idF)

		eng, err := agenda.New(projectPath())
		if err != nil {
			return mcpError(err), nil
		}
		defer eng.Close()

		if err := eng.DeleteAgenda(agendaID); err != nil {
			return mcpError(err), nil
		}
		return mcpJSON(map[string]any{"status": "deleted"}), nil
	})
}

// --- helpers ---

func mcpError(err error) *mcpgo.CallToolResult {
	return mcpgo.NewToolResultError(err.Error())
}

func mcpJSON(v any) *mcpgo.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcpError(err)
	}
	return mcpgo.NewToolResultText(string(b))
}
