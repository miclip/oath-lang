package main

// `oath plugin install` — wire a coding assistant up to the substrate.
//
// WHY THIS IS A CLI COMMAND AND NOT A DOC. Every piece of the workflow already
// existed: 20 MCP tools including behaviour queries (find_spec/find_implies/
// find_equiv), the guarantee ladder, licence evaluation, mutation testing. What
// did not exist was any way for an assistant to FIND that surface. A capability
// nobody can discover is indistinguishable from one that was never built, and the
// gap was packaging rather than substrate.
//
// WHAT IT WRITES, and the reason each file is where it is:
//
//	.mcp.json          the tool surface — without this the agents have no tools
//	.claude/agents/    the subagents, whose separation is the product
//	.claude/skills/    when to reach for the registry at all
//
// WHAT THE SUBAGENT SPLIT IS, STATED HONESTLY. It is a workflow discipline: an
// agent that writes properties to fit a body it already wrote writes weak
// properties, and an agent that wrote the code is the worst candidate to attack
// it. Keeping the roles apart improves the result.
//
// IT IS NOT A CHECKED PROPERTY, and the first draft of this file claimed it was.
// The registry sees the authenticated principal of each put and nothing else —
// subagents in one assistant session share that principal, so it sees ONE
// submitter and cannot distinguish a specifier from an implementer.
// attributeAuthorship derives separation by diffing a submission against the
// PREVIOUS version (spec authorship inherited when props are unchanged, body
// authorship when the body is), so it distinguishes SUCCESSIVE puts by DIFFERENT
// submitters and, within one session, distinguishes nothing.
//
// Making it checkable would need a signing key per agent and separate
// submissions, and even then the property is defeatable by one party holding two
// keys (#82). So the plugin ships the discipline and says plainly that it is one.
//
// MERGE, NEVER CLOBBER. A project's .mcp.json is theirs and probably has servers
// in it. This adds keys and preserves everything else — silently overwriting a
// developer's tool configuration to install a plugin would be a poor advertisement
// for a system whose entire pitch is that provenance is preserved.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// pluginFile is one file the installer writes.
type pluginFile struct {
	Path    string // relative to the install root
	Content string
}

// oathMCPServers is the tool surface. Two servers, deliberately:
//
//   - `oath` runs a LOCAL store over stdio. No credential, no network, and it is
//     what makes put/prove/eval work while iterating.
//   - `oath-registry` is the public registry over HTTP. Reads need a token; a
//     token authorizes SERVICE ACCESS, never name ownership, so publishing still
//     requires a key.
func oathMCPServers(registry string) map[string]any {
	return map[string]any{
		"oath": map[string]any{
			"command": "oath",
			"args":    []string{"serve"},
		},
		"oath-registry": map[string]any{
			"type": "http",
			"url":  strings.TrimSuffix(registry, "/") + "/mcp",
			"headers": map[string]any{
				"Authorization": "Bearer ${OATH_TOKEN}",
			},
		},
	}
}

// mergeMCP adds the Oath servers to an existing config without disturbing it.
// Returns the merged JSON and the names actually added.
func mergeMCP(existing []byte, registry string) ([]byte, []string, error) {
	cfg := map[string]any{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &cfg); err != nil {
			return nil, nil, fmt.Errorf("existing MCP config is not valid JSON, refusing to overwrite it: %w", err)
		}
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	var added []string
	for name, spec := range oathMCPServers(registry) {
		if _, taken := servers[name]; taken {
			continue // theirs wins; never silently replace a configured server
		}
		servers[name] = spec
		added = append(added, name)
	}
	sort.Strings(added)
	cfg["mcpServers"] = servers
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return append(out, '\n'), added, nil
}

func cmdPluginInstall(args []string) {
	dir, registry := ".", "https://registry.oath-lang.org"
	dryRun, userScope, codex := false, false, false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--dir" && i+1 < len(args):
			dir = args[i+1]
			i++
		case args[i] == "--registry" && i+1 < len(args):
			registry = args[i+1]
			i++
		case args[i] == "--dry-run":
			dryRun = true
		case args[i] == "--user":
			userScope = true
		case args[i] == "--codex":
			codex = true
		case args[i] == "--claude-code":
			// default
		default:
			fail(fmt.Errorf("unknown flag %q\nusage: oath plugin install [--codex] [--user] [--dir <path>] [--registry <url>] [--dry-run]", args[i]))
		}
	}

	root := dir
	if userScope {
		root = oathHomeParent()
	}

	files := pluginAssets(codex)

	// The MCP config is merged rather than written, so it is computed against
	// whatever is already there.
	mcpRel := ".mcp.json"
	if codex {
		mcpRel = ".codex/mcp.json"
	}
	mcpPath := filepath.Join(root, mcpRel)
	existing, _ := os.ReadFile(mcpPath)
	merged, added, err := mergeMCP(existing, registry)
	if err != nil {
		fail(err)
	}

	target := "Claude Code"
	if codex {
		target = "Codex"
	}
	fmt.Printf("Installing the Oath plugin for %s into %s\n\n", target, root)

	for _, f := range files {
		fmt.Printf("  %s\n", filepath.Join(root, f.Path))
	}
	fmt.Printf("  %s", mcpPath)
	switch {
	case len(added) == 2:
		fmt.Printf("   (adding servers: %s)\n", strings.Join(added, ", "))
	case len(added) == 1:
		fmt.Printf("   (adding server: %s; the other name was already configured and was left alone)\n", added[0])
	default:
		fmt.Printf("   (both server names already configured — left untouched)\n")
	}

	if dryRun {
		fmt.Printf("\n--dry-run: nothing was written.\n")
		return
	}

	for _, f := range files {
		p := filepath.Join(root, f.Path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			fail(err)
		}
		if err := os.WriteFile(p, []byte(f.Content), 0o644); err != nil {
			fail(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(mcpPath), 0o755); err != nil {
		fail(err)
	}
	if err := os.WriteFile(mcpPath, merged, 0o644); err != nil {
		fail(err)
	}

	fmt.Printf("\nInstalled.\n\n")
	fmt.Printf("  The local `oath` server needs no credential. The registry server reads\n")
	fmt.Printf("  $OATH_TOKEN — a token authorizes SERVICE ACCESS, not name ownership, so\n")
	fmt.Printf("  publishing still requires a key (`oath keygen`, then `publish --key`).\n\n")
	fmt.Printf("  Restart %s to pick up the new tools.\n", target)
}

// oathHomeParent is the user-scope install root (~), where an assistant looks for
// user-level configuration.
func oathHomeParent() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// pluginAssets returns the files to write. They are embedded rather than copied
// from the repository so an installed binary works anywhere.
func pluginAssets(codex bool) []pluginFile {
	if codex {
		return []pluginFile{{Path: "AGENTS.md", Content: codexAgents}}
	}
	out := []pluginFile{
		{Path: ".claude/skills/registry-first/SKILL.md", Content: skillRegistryFirst},
	}
	for name, body := range map[string]string{
		"oath-search":     agentSearch,
		"oath-properties": agentProperties,
		"oath-implement":  agentImplement,
		"oath-adversary":  agentAdversary,
	} {
		out = append(out, pluginFile{Path: ".claude/agents/" + name + ".md", Content: body})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
