package goaipackage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

var (
	rbacMu                sync.RWMutex
	ROLE_TOOL_PERMISSIONS = make(map[string][]string)
)

// SetRBACRules sets accessible tools per role using an RBACRule instance.
func SetRBACRules(rbac RBACRule) {
	SetRBACMap(rbac.Rules)
}

// SetRBACMap sets accessible tools per role directly using map[string][]string.
func SetRBACMap(rules map[string][]string) {
	rbacMu.Lock()
	defer rbacMu.Unlock()

	perms := make(map[string][]string)
	for role, tools := range rules {
		roleKey := strings.ToLower(strings.TrimSpace(role))
		var cleanTools []string
		for _, tool := range tools {
			cTool := strings.TrimSpace(tool)
			if cTool != "" {
				cleanTools = append(cleanTools, cTool)
			}
		}
		perms[roleKey] = cleanTools
	}
	ROLE_TOOL_PERMISSIONS = perms
}

var (
	knownToolsMu  sync.RWMutex
	knownTools    = map[string]bool{"no_tools": true, "no_tool": true}
	knownToolList []string
)

// updateKnownTools populates the known tools from the available MCP tools.
func updateKnownTools(tools []mcp.Tool) {
	knownToolsMu.Lock()
	defer knownToolsMu.Unlock()

	knownTools = map[string]bool{
		"no_tools": true,
		"no_tool":  true,
	}
	var list []string
	for _, t := range tools {
		name := strings.ToLower(strings.TrimSpace(t.Name))
		if name != "" {
			knownTools[name] = true
			list = append(list, t.Name)
		}
	}
	knownToolList = list
}

func isKnownTool(toolName string) bool {
	knownToolsMu.RLock()
	defer knownToolsMu.RUnlock()
	return knownTools[strings.ToLower(strings.TrimSpace(toolName))]
}

func getKnownToolList() []string {
	knownToolsMu.RLock()
	defer knownToolsMu.RUnlock()
	dst := make([]string, len(knownToolList))
	copy(dst, knownToolList)
	return dst
}

var INTERNAL_PARAM_NAMES = []string{"credentials", "UserAuth", "user_auth", "auth"}

type ToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type ToolExecutionResult struct {
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args"`
	Result any            `json:"result"`
}

// shrinkToolCatalog shrinks the MCP tool catalog before it hits the prompt using caveman-shrink CLI or fallback native minifier.
func shrinkToolCatalog(tools []mcp.Tool) []mcp.Tool {
	if len(tools) == 0 {
		return tools
	}

	payload, err := json.Marshal(map[string]any{"tools": tools})
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()

		var cmd *exec.Cmd
		if _, err := exec.LookPath("caveman-shrink"); err == nil {
			cmd = exec.CommandContext(ctx, "caveman-shrink")
		} else if _, err := exec.LookPath("npx"); err == nil {
			cmd = exec.CommandContext(ctx, "npx", "--yes", "caveman-shrink")
		}

		if cmd != nil {
			cmd.Stdin = bytes.NewReader(payload)
			var out bytes.Buffer
			cmd.Stdout = &out

			if err := cmd.Run(); err == nil && out.Len() > 0 {
				var shrunkResp struct {
					Tools []mcp.Tool `json:"tools"`
				}
				if err := json.Unmarshal(out.Bytes(), &shrunkResp); err == nil && len(shrunkResp.Tools) > 0 {
					log.Printf("[caveman-shrink] Successfully shrunk %d tools via CLI", len(shrunkResp.Tools))
					return shrunkResp.Tools
				}
			}
		}
	}

	// Fallback to native Caveman compression
	return nativeCavemanShrink(tools)
}

// nativeCavemanShrink provides a fast, zero-dependency token minifier for tool descriptions and schemas.
func nativeCavemanShrink(tools []mcp.Tool) []mcp.Tool {
	fillerRegex := regexp.MustCompile(`(?i)\b(a|an|the|this|that|these|those|is|are|was|were|will|would|should|can|could|to|for|of|in|on|at|by|with|from|lookup|fetches|retrieves|information|data|details|specific|internal|system)\b`)
	spacesRegex := regexp.MustCompile(`\s+`)

	shrunk := make([]mcp.Tool, len(tools))
	for i, t := range tools {
		copyTool := t
		desc := t.Description

		// Clean description: remove filler words & condense
		cleaned := fillerRegex.ReplaceAllString(desc, " ")
		cleaned = spacesRegex.ReplaceAllString(cleaned, " ")
		copyTool.Description = strings.TrimSpace(cleaned)

		shrunk[i] = copyTool
	}
	return shrunk
}

// 1. Setup & Connection

// newServerConnectionWithLink connects to an MCP server using an MCPLink.
// It supports HTTP/HTTPS (SSE) URLs as well as local scripts/executables via Stdio.
func newServerConnectionWithLink(ctx context.Context, mcpLink MCPLink) (*client.Client, error) {
	var c *client.Client
	var err error

	link := strings.TrimSpace(mcpLink.Link)
	if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
		c, err = client.NewSSEMCPClient(link)
		if err != nil {
			return nil, fmt.Errorf("failed to create SSE MCP client: %w", err)
		}
		if err = c.Start(ctx); err != nil {
			return nil, fmt.Errorf("failed to start SSE MCP client: %w", err)
		}
	} else {
		serverScript := link
		if serverScript == "" {
			serverScript, _ = filepath.Abs(filepath.Join("..", "mcp_server", "server.go"))
		}
		c, err = client.NewStdioMCPClient("go", nil, "run", serverScript)
		if err != nil {
			return nil, fmt.Errorf("failed to create Stdio MCP client: %w", err)
		}
	}

	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, err = c.Initialize(initCtx, mcp.InitializeRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize MCP client: %w", err)
	}
	return c, nil
}

// fetchMCPTools queries the MCP server to retrieve all registered tools.
func fetchMCPTools(ctx context.Context, c *client.Client) ([]mcp.Tool, error) {
	res, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list tools from MCP server: %w", err)
	}
	return res.Tools, nil
}

// ConnectAndLoadKnownTools connects to the MCP server via MCPLink,
// fetches all available tools, and populates knownTools automatically.
func ConnectAndLoadKnownTools(ctx context.Context, mcpLink MCPLink) (*client.Client, []mcp.Tool, error) {
	c, err := newServerConnectionWithLink(ctx, mcpLink)
	if err != nil {
		return nil, nil, err
	}

	tools, err := fetchMCPTools(ctx, c)
	if err != nil {
		return c, nil, err
	}

	updateKnownTools(tools)
	return c, tools, nil
}

func filterRoles(mcp_tools []mcp.Tool, role string) []mcp.Tool {
	rbacMu.RLock()
	defer rbacMu.RUnlock()

	if len(mcp_tools) == 0 {
		return nil
	}
	// If RBAC rules are not configured, all tools are permitted by default
	if len(ROLE_TOOL_PERMISSIONS) == 0 {
		dst := make([]mcp.Tool, len(mcp_tools))
		copy(dst, mcp_tools)
		return dst
	}

	role_key := strings.ToLower(strings.TrimSpace(role))
	allowed, ok := ROLE_TOOL_PERMISSIONS[role_key]
	if !ok {
		// Fallback to wildcard "*" role if defined
		if wildcardAllowed, hasWildcard := ROLE_TOOL_PERMISSIONS["*"]; hasWildcard {
			allowed = wildcardAllowed
			ok = true
		} else {
			return nil
		}
	}

	// Check if allowed contains wildcard "*" or "all"
	for _, a := range allowed {
		cleanA := strings.TrimSpace(strings.ToLower(a))
		if cleanA == "*" || cleanA == "all" {
			dst := make([]mcp.Tool, len(mcp_tools))
			copy(dst, mcp_tools)
			return dst
		}
	}

	var tool_filtered []mcp.Tool
	for _, t := range mcp_tools {
		tClean := strings.Trim(strings.ToLower(t.Name), "_")
		for _, a := range allowed {
			aClean := strings.Trim(strings.ToLower(a), "_")
			if aClean == tClean || strings.EqualFold(t.Name, strings.TrimSpace(a)) {
				tool_filtered = append(tool_filtered, t)
				break
			}
		}
	}
	return tool_filtered
}

// isRolePermittedForTool checks if a role has permission to execute a specific tool.
func isRolePermittedForTool(role string, toolName string) bool {
	cleanTarget := strings.Trim(strings.ToLower(strings.TrimSpace(toolName)), "_")
	if cleanTarget == "" || cleanTarget == "no_tools" || cleanTarget == "no_tool" || cleanTarget == "none" {
		return true
	}

	rbacMu.RLock()
	defer rbacMu.RUnlock()

	// If no RBAC configured, allow all
	if len(ROLE_TOOL_PERMISSIONS) == 0 {
		return true
	}

	role_key := strings.ToLower(strings.TrimSpace(role))
	allowed, ok := ROLE_TOOL_PERMISSIONS[role_key]
	if !ok {
		if wildcardAllowed, hasWildcard := ROLE_TOOL_PERMISSIONS["*"]; hasWildcard {
			allowed = wildcardAllowed
			ok = true
		} else {
			return false
		}
	}

	for _, a := range allowed {
		aClean := strings.Trim(strings.ToLower(strings.TrimSpace(a)), "_")
		if aClean == "*" || aClean == "all" || aClean == cleanTarget || strings.EqualFold(toolName, strings.TrimSpace(a)) {
			return true
		}
	}
	return false
}

// buildToolGuide formats available MCP tools and descriptions into markdown list for the system prompt.
func buildToolGuide(availableMcpTools []mcp.Tool) string {
	if len(availableMcpTools) == 0 {
		return "- No tools available for your role."
	}

	var lines []string
	for _, t := range availableMcpTools {
		desc := t.Description
		if desc == "" {
			desc = "No description."
		}
		lines = append(lines, fmt.Sprintf("- **`%s`**: %s", t.Name, desc))
	}

	return strings.Join(lines, "\n")
}

// extractBalancedJSONObjects scans text and extracts all balanced top-level JSON objects.
// This is immune to regex greediness, nested objects/arrays, and trailing non-JSON markup.
func extractBalancedJSONObjects(text string) []string {
	var objects []string
	runes := []rune(text)
	n := len(runes)

	for i := 0; i < n; i++ {
		if runes[i] != '{' {
			continue
		}
		depth := 0
		inString := false
		escape := false
		start := i

		for j := i; j < n; j++ {
			r := runes[j]
			if escape {
				escape = false
				continue
			}
			if r == '\\' && inString {
				escape = true
				continue
			}
			if r == '"' {
				inString = !inString
				continue
			}
			if inString {
				continue
			}
			if r == '{' {
				depth++
			} else if r == '}' {
				depth--
				if depth == 0 {
					objects = append(objects, string(runes[start:j+1]))
					i = j
					break
				}
			}
		}
	}
	return objects
}

// 2. Parsing AI response
// parsing tool call dari yappingan ai
func tryParseToolCalls(text string) []ToolCall {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var calls []ToolCall

	// 1. Multi-vendor tag-based matching:
	// - DeepSeek DSML: <tool_call>... closed by </tool_call>, </|DSML|>, <||DSML||...>, or <|tool_call_end|>
	// - OpenAI / Llama / Hermes: <|tool_call_start|>...<|tool_call_end|>
	// - Qwen / Tongyi: <|action_start|><|plugin|>...<|action_end|>
	// - Claude / Anthropic: <tool_use>...</tool_use> or <function_calls>...
	// - Markdown code blocks: ```json ... ``` or ```tool_call ... ```
	tagPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?si)<tool_call>\s*(.*?)\s*(?:</tool_call>|</\s*\|\s*DSML\s*\|>|<\s*(?:\|\s*)*DSML(?:\|\s*)*[^>]*>|<\|tool_call_end\|>|$)`),
		regexp.MustCompile(`(?s)<\|tool_call_start\|>\s*(.*?)\s*(?:<\|tool_call_end\|>|$)`),
		regexp.MustCompile(`(?s)<\|action_start\|><\|plugin\|>\s*(.*?)\s*(?:<\|action_end\|>|$)`),
		regexp.MustCompile(`(?s)<tool_use>\s*(.*?)\s*(?:</tool_use>|$)`),
		regexp.MustCompile(`(?s)` + "```" + `(?:tool_call|json)?\s*(.*?)\s*` + "```"),
	}

	for _, re := range tagPatterns {
		matches := re.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			if len(m) >= 2 {
				inner := strings.TrimSpace(m[1])
				if jsonCalls := parseCallsJson(inner); len(jsonCalls) > 0 {
					calls = append(calls, jsonCalls...)
				} else if pyCalls := parsePythonToolCalls(inner); len(pyCalls) > 0 {
					calls = append(calls, pyCalls...)
				}
			}
		}
	}
	if len(calls) > 0 {
		return calls
	}

	// 2. Python-style function calls anywhere in text (e.g. [add_asset(name='...', ...), ...])
	if pyCalls := parsePythonToolCalls(text); len(pyCalls) > 0 {
		return pyCalls
	}

	// 3. JSON array or object anywhere in text
	if jsonCalls := parseCallsJson(text); len(jsonCalls) > 0 {
		return jsonCalls
	}

	// 4. Fallback: balanced-brace JSON object scanner (handles nested arrays, quotes, and trailing tokens)
	for _, objStr := range extractBalancedJSONObjects(text) {
		if parsed := parseSingleCallJson(objStr); parsed != nil && parsed.Name != "" {
			calls = append(calls, *parsed)
		}
	}
	if len(calls) > 0 {
		return calls
	}
	// 5. Fallback: heuristic tool mention with ID regex (skip if text is an educational/tutorial explanation)
	tutorialPattern := regexp.MustCompile(`(?i)\b(contoh|misalnya|sebagai contoh|for example|e\.g\.|you can use|anda bisa menggunakan|cara menggunakan|tutorial|documentation)\b`)
	if tutorialPattern.MatchString(text) {
		return nil
	}

	for _, tool := range getKnownToolList() {
		baseName := strings.TrimSuffix(tool, "s")
		pattern := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(baseName) + `s?(?:\(\))?\b`)
		if pattern.MatchString(text) {
			idRegex := regexp.MustCompile(`(?:asset_id|id)["'\s:=]+([a-zA-Z0-9_\-]+)`)
			if idMatch := idRegex.FindStringSubmatch(text); len(idMatch) >= 2 {
				args := map[string]any{"id": idMatch[1]}
				log.Printf("[try_parse_tool_calls] Recovered tool call from text mention with ID: %s %v", tool, args)
				return []ToolCall{{Name: tool, Arguments: args}}
			}
		}
	}

	return nil
}

// SanitizeModelReply removes all thinking, tool call markup, DSML tokens, and internal model tags across all models.
// Guarantees that raw tool JSON or vendor tokens NEVER leak to the chat UI.
func SanitizeModelReply(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}

	cleaned := raw

	// 1. Thinking / reasoning tags
	cleaned = regexp.MustCompile(`(?s)<thought>.*?(?:</thought>|$)`).ReplaceAllString(cleaned, "")
	cleaned = regexp.MustCompile(`(?s)<think>.*?(?:</think>|$)`).ReplaceAllString(cleaned, "")
	cleaned = regexp.MustCompile(`(?s)(?:^|\n)(?:Thought|Thinking):\s*.*?(?:\n\n|$)`).ReplaceAllString(cleaned, "")

	// 2. Tool call markup across models (DeepSeek DSML, Qwen, OpenAI, Claude, markdown fences)
	cleaned = regexp.MustCompile(`(?si)<tool_call>.*?(?:</tool_call>|</\s*\|\s*DSML\s*\|>|<\s*(?:\|\s*)*DSML(?:\|\s*)*[^>]*>|<\|tool_call_end\|>|$)`).ReplaceAllString(cleaned, "")
	cleaned = regexp.MustCompile(`(?s)<\|tool_call_start\|>.*?(?:<\|tool_call_end\|>|$)`).ReplaceAllString(cleaned, "")
	cleaned = regexp.MustCompile(`(?s)<\|action_start\|>.*?(?:<\|action_end\|>|$)`).ReplaceAllString(cleaned, "")
	cleaned = regexp.MustCompile(`(?s)<function_calls>.*?(?:</function_calls>|$)`).ReplaceAllString(cleaned, "")
	cleaned = regexp.MustCompile(`(?s)<tool_use>.*?(?:</tool_use>|$)`).ReplaceAllString(cleaned, "")
	cleaned = regexp.MustCompile("(?s)```"+`(?:tool_call|json)?\s*\{.*?"(?:name|function|tool|action)"[^`+"`"+`]*\}\s*`+"```").ReplaceAllString(cleaned, "")
	cleaned = regexp.MustCompile("(?s)```"+`(?:tool_call)\s*.*?(?:`+"```"+`|$)`).ReplaceAllString(cleaned, "")

	// 3. Model delimiters and tokens
	cleaned = regexp.MustCompile(`(?si)<\s*/?\s*(?:\|\s*)*DSML(?:\|\s*)*[^>]*>`).ReplaceAllString(cleaned, "")
	cleaned = regexp.MustCompile(`(?s)<\|im_start\|>.*?(\n|$)`).ReplaceAllString(cleaned, "")
	cleaned = regexp.MustCompile(`(?s)<\|im_end\|>`).ReplaceAllString(cleaned, "")
	cleaned = regexp.MustCompile(`(?s)<\|endoftext\|>`).ReplaceAllString(cleaned, "")
	cleaned = regexp.MustCompile(`(?s)<\|assistant\|>`).ReplaceAllString(cleaned, "")
	cleaned = regexp.MustCompile(`(?s)<\|observation\|>`).ReplaceAllString(cleaned, "")

	// 4. Strip residual raw JSON tool payloads if any remain without tags
	for _, obj := range extractBalancedJSONObjects(cleaned) {
		if parsed := parseSingleCallJson(obj); parsed != nil && parsed.Name != "" {
			cleaned = strings.Replace(cleaned, obj, "", 1)
		}
	}

	// 5. Normalise whitespace
	cleaned = regexp.MustCompile(`\n{3,}`).ReplaceAllString(cleaned, "\n\n")
	return strings.TrimSpace(cleaned)
}

// parseCallsJson handles either a JSON array [...] or single JSON object {...}
func parseCallsJson(raw string) []ToolCall {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	// Try JSON array of tool calls
	if strings.HasPrefix(raw, "[") {
		var list []map[string]any
		if err := json.Unmarshal([]byte(raw), &list); err == nil {
			var calls []ToolCall
			for _, item := range list {
				itemBytes, _ := json.Marshal(item)
				if parsed := parseSingleCallJson(string(itemBytes)); parsed != nil {
					calls = append(calls, *parsed)
				}
			}
			if len(calls) > 0 {
				return calls
			}
		}
	}

	// Try single JSON call
	if parsed := parseSingleCallJson(raw); parsed != nil {
		return []ToolCall{*parsed}
	}
	return nil
}

type pyParser struct {
	runes []rune
	pos   int
	n     int
}

func (p *pyParser) skipWhitespace() {
	for p.pos < p.n {
		r := p.runes[p.pos]
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			p.pos++
		} else {
			break
		}
	}
}


func (p *pyParser) parseValue() any {
	p.skipWhitespace()
	if p.pos >= p.n {
		return nil
	}
	r := p.runes[p.pos]
	if r == '\'' || r == '"' {
		return p.parseString()
	}
	if r == '[' {
		return p.parseList()
	}
	if r == '{' {
		return p.parseDict()
	}
	return p.parsePrimitive()
}

func (p *pyParser) parseString() string {
	quote := p.runes[p.pos]
	p.pos++ // skip opening quote
	var sb strings.Builder
	escape := false
	for p.pos < p.n {
		r := p.runes[p.pos]
		if escape {
			sb.WriteRune(r)
			escape = false
			p.pos++
			continue
		}
		if r == '\\' {
			escape = true
			p.pos++
			continue
		}
		if r == quote {
			p.pos++ // skip closing quote
			break
		}
		sb.WriteRune(r)
		p.pos++
	}
	return sb.String()
}

func (p *pyParser) parseList() []any {
	p.pos++ // skip '['
	var list []any
	for p.pos < p.n {
		p.skipWhitespace()
		if p.pos >= p.n || p.runes[p.pos] == ']' {
			if p.pos < p.n {
				p.pos++ // skip ']'
			}
			break
		}
		val := p.parseValue()
		list = append(list, val)
		p.skipWhitespace()
		if p.pos < p.n && p.runes[p.pos] == ',' {
			p.pos++ // skip ','
		}
	}
	return list
}

func (p *pyParser) parseDict() map[string]any {
	p.pos++ // skip '{'
	m := make(map[string]any)
	for p.pos < p.n {
		p.skipWhitespace()
		if p.pos >= p.n || p.runes[p.pos] == '}' {
			if p.pos < p.n {
				p.pos++ // skip '}'
			}
			break
		}
		var key string
		r := p.runes[p.pos]
		if r == '\'' || r == '"' {
			key = p.parseString()
		} else {
			start := p.pos
			for p.pos < p.n && isIdentRune(p.runes[p.pos]) {
				p.pos++
			}
			key = string(p.runes[start:p.pos])
		}
		p.skipWhitespace()
		if p.pos < p.n && (p.runes[p.pos] == ':' || p.runes[p.pos] == '=') {
			p.pos++ // skip ':' or '='
		}
		val := p.parseValue()
		if key != "" {
			m[key] = val
		}
		p.skipWhitespace()
		if p.pos < p.n && p.runes[p.pos] == ',' {
			p.pos++
		}
	}
	return m
}

func (p *pyParser) parsePrimitive() any {
	start := p.pos
	for p.pos < p.n {
		r := p.runes[p.pos]
		if r == ',' || r == ']' || r == '}' || r == ')' || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			break
		}
		p.pos++
	}
	raw := strings.TrimSpace(string(p.runes[start:p.pos]))
	if strings.EqualFold(raw, "true") {
		return true
	}
	if strings.EqualFold(raw, "false") {
		return false
	}
	if strings.EqualFold(raw, "none") || strings.EqualFold(raw, "null") {
		return nil
	}
	if num, err := strconv.ParseFloat(raw, 64); err == nil {
		return num
	}
	return raw
}

func isIdentRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

func parsePythonKwargs(argsStr string) map[string]any {
	p := &pyParser{runes: []rune(argsStr), pos: 0, n: len([]rune(argsStr))}
	args := make(map[string]any)
	for p.pos < p.n {
		p.skipWhitespace()
		if p.pos >= p.n {
			break
		}
		if p.runes[p.pos] == ',' {
			p.pos++
			p.skipWhitespace()
		}
		if p.pos >= p.n {
			break
		}

		var key string
		r := p.runes[p.pos]
		if r == '\'' || r == '"' {
			key = p.parseString()
		} else {
			start := p.pos
			for p.pos < p.n && isIdentRune(p.runes[p.pos]) {
				p.pos++
			}
			key = string(p.runes[start:p.pos])
		}
		if key == "" {
			p.pos++
			continue
		}

		p.skipWhitespace()
		if p.pos < p.n && (p.runes[p.pos] == '=' || p.runes[p.pos] == ':') {
			p.pos++ // skip '=' or ':'
		}
		val := p.parseValue()
		args[key] = val
	}
	return args
}

// parsePythonToolCalls parses Python/LFM-style tool calls like:
// add_asset(name='...', category='...', ...)
// [generate_pdf{headers=[...], data=[...], title='...'}]
func parsePythonToolCalls(text string) []ToolCall {
	var calls []ToolCall
	runes := []rune(text)
	n := len(runes)

	for i := 0; i < n; i++ {
		if (runes[i] >= 'a' && runes[i] <= 'z') || (runes[i] >= 'A' && runes[i] <= 'Z') || runes[i] == '_' {
			start := i
			for i < n && isIdentRune(runes[i]) {
				i++
			}
			toolName := string(runes[start:i])
			if len(getKnownToolList()) > 0 && !isKnownTool(toolName) {
				continue
			}

			// Skip spaces to opening delimiter '(' or '{'
			for i < n && (runes[i] == ' ' || runes[i] == '\t' || runes[i] == '\n' || runes[i] == '\r') {
				i++
			}
			if i >= n {
				continue
			}
			openDelim := runes[i]
			var closeDelim rune
			if openDelim == '(' {
				closeDelim = ')'
			} else if openDelim == '{' {
				closeDelim = '}'
			} else {
				continue
			}
			i++ // skip openDelim

			argsStart := i
			inSingleQuote := false
			inDoubleQuote := false
			escape := false
			delimDepth := 1

			for i < n {
				r := runes[i]
				if escape {
					escape = false
					i++
					continue
				}
				if r == '\\' {
					escape = true
					i++
					continue
				}
				if r == '\'' && !inDoubleQuote {
					inSingleQuote = !inSingleQuote
				} else if r == '"' && !inSingleQuote {
					inDoubleQuote = !inDoubleQuote
				} else if !inSingleQuote && !inDoubleQuote {
					if r == openDelim {
						delimDepth++
					} else if r == closeDelim {
						delimDepth--
						if delimDepth == 0 {
							break
						}
					}
				}
				i++
			}

			argsStr := string(runes[argsStart:i])
			args := parsePythonKwargs(argsStr)
			calls = append(calls, ToolCall{
				Name:      strings.ToLower(toolName),
				Arguments: args,
			})
		}
	}
	return calls
}

// bersihin json tool call
func parseSingleCallJson(raw string) *ToolCall {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var call map[string]any
	if err := json.Unmarshal([]byte(raw), &call); err != nil {
		nameRegex := regexp.MustCompile(`"(?:name|function|tool|action)"\s*:\s*"([^"]+)"`)
		nameMatch := nameRegex.FindStringSubmatch(raw)
		if len(nameMatch) < 2 {
			return nil
		}
		args := map[string]any{}
		argsRegex := regexp.MustCompile(`(?s)"(?:arguments|parameters|input|args)"\s*:\s*(\{.*?\})`)
		if argsMatch := argsRegex.FindStringSubmatch(raw); len(argsMatch) >= 2 {
			_ = json.Unmarshal([]byte(argsMatch[1]), &args)
		}
		call = map[string]any{"name": nameMatch[1], "arguments": args}
	}

	// Nested OpenAI function object: {"type": "function", "function": {"name": "...", "arguments": ...}}
	if fnObj, ok := call["function"].(map[string]any); ok {
		for k, v := range fnObj {
			if _, exists := call[k]; !exists {
				call[k] = v
			}
		}
	}

	rawName, _ := call["name"].(string)
	if rawName == "" {
		rawName, _ = call["function"].(string)
	}
	if rawName == "" {
		rawName, _ = call["tool"].(string)
	}
	if rawName == "" {
		rawName, _ = call["action"].(string)
	}

	parts := strings.Split(rawName, ".")
	name := strings.Trim(parts[len(parts)-1], "@_ ")
	if name == "" {
		return nil
	}

	var args map[string]any
	if a, ok := call["arguments"].(map[string]any); ok {
		args = a
	} else if aStr, ok := call["arguments"].(string); ok && strings.HasPrefix(strings.TrimSpace(aStr), "{") {
		_ = json.Unmarshal([]byte(aStr), &args)
	} else if p, ok := call["parameters"].(map[string]any); ok {
		args = p
	} else if pStr, ok := call["parameters"].(string); ok && strings.HasPrefix(strings.TrimSpace(pStr), "{") {
		_ = json.Unmarshal([]byte(pStr), &args)
	} else if inp, ok := call["input"].(map[string]any); ok {
		args = inp
	} else if inpStr, ok := call["input"].(string); ok && strings.HasPrefix(strings.TrimSpace(inpStr), "{") {
		_ = json.Unmarshal([]byte(inpStr), &args)
	} else if ag, ok := call["args"].(map[string]any); ok {
		args = ag
	}

	if args == nil {
		args = map[string]any{}
	}
	if _, ok := args["properties"]; ok {
		args = map[string]any{}
	}

	return &ToolCall{Name: name, Arguments: args}
}

// 3. Tool Execution
// executeToolsParallel runs read-only tools concurrently, but executes mutating/write
// tools sequentially in order to preserve causal dependency and prevent database race conditions.
func executeToolsParallel(ctx context.Context, client *client.Client, toolCalls []ToolCall, mcpTools []mcp.Tool, auth UserAuth) []ToolExecutionResult {
	if len(toolCalls) == 0 {
		return nil
	}

	results := make([]ToolExecutionResult, len(toolCalls))

	// Check if any tool call is a mutating/write operation
	hasWrite := false
	for _, tc := range toolCalls {
		if !isReadOnlyTool(tc.Name) {
			hasWrite = true
			break
		}
	}

	if !hasWrite {
		// All tools are read-only: run all concurrently in parallel
		var wg sync.WaitGroup
		for i, tc := range toolCalls {
			wg.Add(1)
			go func(idx int, call ToolCall) {
				defer wg.Done()
				results[idx] = executeSingleTool(ctx, client, call.Name, call.Arguments, mcpTools, auth)
			}(i, tc)
		}
		wg.Wait()
		return results
	}

	// Mutating/write tools present: execute sequentially to avoid race conditions & preserve causal order
	for i, tc := range toolCalls {
		results[i] = executeSingleTool(ctx, client, tc.Name, tc.Arguments, mcpTools, auth)
	}
	return results
}

// run the goddamn tool
func executeSingleTool(ctx context.Context, client *client.Client, fnName string, args map[string]any, mcpTools []mcp.Tool, auth UserAuth) ToolExecutionResult {
	fnNameCleaned := strings.Trim(fnName, "_")
	targetTool := ""

	for _, t := range mcpTools {
		if t.Name == fnNameCleaned || strings.EqualFold(t.Name, fnNameCleaned) {
			targetTool = t.Name
			break
		}
	}

	if targetTool == "" {
		for _, t := range mcpTools {
			if strings.Contains(strings.ToLower(t.Name), strings.ToLower(fnNameCleaned)) {
				targetTool = t.Name
				break
			}
		}
	}

	if targetTool == "" && (fnNameCleaned == "no_tool" || fnNameCleaned == "no_tools") {
		targetTool = "no_tools"
	}

	if targetTool == "" {
		return ToolExecutionResult{
			Tool:   fnName,
			Args:   args,
			Result: map[string]any{"error": fmt.Sprintf("Tool '%s' not available for current role.", fnName)},
		}
	}

	callArgs := make(map[string]any)
	for k, v := range args {
		callArgs[k] = v
	}
	if auth != (UserAuth{}) {
		callArgs["credentials"] = auth
		callArgs["UserAuth"] = auth
	}

	res, err := client.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      targetTool,
			Arguments: callArgs,
		},
	})
	if err != nil {
		return ToolExecutionResult{
			Tool:   targetTool,
			Args:   args,
			Result: map[string]any{"error": err.Error()},
		}
	}

	rawContent := "{}"
	for _, c := range res.Content {
		if textContent, ok := c.(mcp.TextContent); ok {
			rawContent = textContent.Text
			break
		}
	}

	var parsedResult any
	if err := json.Unmarshal([]byte(rawContent), &parsedResult); err != nil {
		parsedResult = rawContent
	}

	return ToolExecutionResult{
		Tool:   targetTool,
		Args:   args,
		Result: parsedResult,
	}
}

// 4. Formatting output for LLM
// formatMultiRawDataForLlm formats accumulated API tool results into a compact block for the LLM.
func formatMultiRawDataForLlm(accumulatedResults []ToolExecutionResult) string {
	if len(accumulatedResults) == 0 {
		return "No API data returned."
	}

	var blocks []string
	for _, item := range accumulatedResults {
		toolName := item.Tool
		if toolName == "" {
			toolName = "unknown_tool"
		}
		argsBytes, _ := json.Marshal(item.Args)
		resultStr := formatRawDataForLlm(item.Result)
		blocks = append(blocks, fmt.Sprintf("--- Data from Tool '%s' (args: %s) ---\n%s", toolName, string(argsBytes), resultStr))
	}

	return strings.Join(blocks, "\n\n")
}

const MaxToolResultChars = 6000

// formatRawDataForLlm compresses and formats raw API result data compactly to minimize prompt tokens,
// capping output to avoid context window overflow.
func formatRawDataForLlm(result any) string {
	if result == nil {
		return "None"
	}

	var formatted string
	switch v := result.(type) {
	case string:
		vTrimmed := strings.TrimSpace(v)
		// If string contains JSON with indentation, compact it
		var parsed any
		if err := json.Unmarshal([]byte(vTrimmed), &parsed); err == nil {
			if compacted, err := json.Marshal(parsed); err == nil {
				formatted = string(compacted)
			} else {
				formatted = vTrimmed
			}
		} else {
			formatted = vTrimmed
		}
	default:
		if b, err := json.Marshal(result); err == nil {
			formatted = string(b)
		} else {
			formatted = fmt.Sprintf("%v", result)
		}
	}

	// Truncate oversized output to prevent blowing up the LLM context window
	if len(formatted) > MaxToolResultChars {
		formatted = formatted[:MaxToolResultChars] + "\n... [Data truncated to prevent context window overflow. Please narrow your query]"
	}

	return formatted
}
