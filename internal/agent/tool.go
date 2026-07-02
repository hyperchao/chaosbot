// Package agent defines the tool-using loop and the tool boundary.
//
// The agent loop depends on the Tool interface and the Registry only;
// concrete tools (filesystem, shell, web, time, ...) live in
// internal/tools/<area> and are wired at the composition root
// (cmd/chaosbot/main.go) via hyperchao/di.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"chaosbot/internal/provider"
)

// Tool is the boundary between the agent loop and one capability
// (filesystem, shell, web, time, ...). The agent depends on this
// interface only; concrete tools live in internal/tools/<area>.
type Tool interface {
	// Name returns the model-visible identifier of the tool. Must
	// be unique within a Registry. Convention: snake_case verb_noun
	// (e.g. "read_file", "web_fetch").
	Name() string

	// Description returns a short docstring the LLM sees. Should
	// be one or two sentences explaining what the tool does and
	// when to use it.
	Description() string

	// Parameters returns a JSON Schema object describing the
	// tool's argument shape. The agent loop hands this to the LLM
	// verbatim (no parsing, no validation) via
	// provider.ToolSpec.Parameters.
	Parameters() json.RawMessage

	// Invoke executes the tool with the given arguments. args is
	// the raw JSON object the LLM produced, matched against the
	// schema returned by Parameters. Implementations must respect
	// ctx cancellation. Errors are surfaced to the LLM as a tool
	// message; the agent loop does not abort on a tool error.
	Invoke(ctx context.Context, args json.RawMessage) (string, error)
}

// ErrToolNotFound is the sentinel returned by Registry.Invoke when
// no tool is registered under the requested name.
var ErrToolNotFound = errors.New("agent: tool not found")

// Registry holds the set of tools available to one agent session.
// The zero value is not usable; construct via NewRegistry.
//
// Not safe for concurrent use; the agent loop is single-threaded
// per architecture.md §6.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry returns an empty Registry. Add tools with Register;
// calling Register with a name that is already registered
// overwrites the previous tool (last-write-wins).
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool to the registry, or overwrites an existing
// tool with the same name. Useful for runtime additions such as a
// REPL "/tools" reload.
func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

// Specs returns one provider.ToolSpec per registered tool, in
// unspecified order (map iteration).
func (r *Registry) Specs() []provider.ToolSpec {
	out := make([]provider.ToolSpec, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, provider.ToolSpec{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}
	return out
}

// Names returns the registered tool names, sorted alphabetically.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Invoke dispatches a tool call by name. Returns ErrToolNotFound
// (wrapped with %w and the requested name appended) when no tool
// is registered under that name. Tool execution errors are
// propagated as-is.
func (r *Registry) Invoke(ctx context.Context, name string, args json.RawMessage) (string, error) {
	t, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrToolNotFound, name)
	}
	return t.Invoke(ctx, args)
}
