package goaipackage

import (
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// verifikasi dan benerin parameter
func resolveIdentifiers(toolCalls []ToolCall, mcpTools []mcp.Tool, accumulatedResults []ToolExecutionResult, resolverMap map[string]string, query string) []ToolCall {
	if len(resolverMap) == 0 {
		resolverMap = inferResolverMap(mcpTools)
	}

	var resolvedCalls []ToolCall
	injectedResolvers := make(map[string]bool)

	for _, call := range toolCalls {
		fnName := strings.Trim(call.Name, "_")
		args := make(map[string]any)
		for k, v := range call.Arguments {
			args[k] = v
		}

		if isReadOnlyTool(fnName) {
			resolvedCalls = append(resolvedCalls, resolveReadCall(fnName, args, mcpTools, accumulatedResults, resolverMap, query, injectedResolvers)...)
			continue
		}

		resolveWriteCall(fnName, args, mcpTools, accumulatedResults, resolverMap, query)
		resolvedCalls = append(resolvedCalls, ToolCall{Name: fnName, Arguments: args})
	}

	return resolvedCalls
}

// read path: verify and canonicalize params, inject a resolver when reference data is missing.
func resolveReadCall(fnName string, args map[string]any, mcpTools []mcp.Tool, accumulatedResults []ToolExecutionResult, resolverMap map[string]string, query string, injectedResolvers map[string]bool) []ToolCall {
	needsResolver := ""
	tool := toolByName(mcpTools, fnName)

	for paramName, val := range args {
		isIDParam := isIDParamName(paramName)
		if !isIDParam {
			if paramSchemaType(tool, paramName) != "string" {
				continue
			}
		}
		resolverTool, ok := lookupResolver(resolverMap, fnName, paramName)
		if !ok {
			continue
		}
		supplied := strings.TrimSpace(fmt.Sprintf("%v", val))
		if supplied == "" {
			continue
		}

		if !resolverToolAvailable(resolverTool, mcpTools) {
			log.Printf("[resolveIdentifiers] Cannot verify %s=%q: resolver '%s' not permitted. Dropping call '%s'.", paramName, supplied, resolverTool, fnName)
			return nil
		}

		records := findResolverRecords(resolverTool, accumulatedResults)
		if len(records) == 0 {
			needsResolver = resolverTool
			break
		}

		idField := inferIdField(records, paramName)

		if isIDParam {
			if recordHasExactID(supplied, records, idField) || looksCanonicalID(supplied, records, idField) {
				continue // Already a valid ID
			}
		}

		m := bestMatch(supplied, records, idField, 0.6)

		// Fallback: match query string against record values
		if m == nil && isIDParam {
			m = matchRecordFromQuery(query, records, idField)
		}

		if m != nil {
			if isIDParam {
				if idVal, ok := m.Record[idField]; ok {
					args[paramName] = idVal
					log.Printf("[resolveIdentifiers] Auto-corrected %s: %q -> %v via '%s'", paramName, supplied, idVal, resolverTool)
				}
			} else {
				args[paramName] = m.CanonValue
				log.Printf("[resolveIdentifiers] Canonicalized %s: %q -> %v via '%s'", paramName, supplied, m.CanonValue, resolverTool)
			}
		} else {
			needsResolver = resolverTool
			break
		}
	}

	if needsResolver != "" {
		if injectedResolvers[needsResolver] {
			return nil
		}
		if !resolverNeedsNoArgs(needsResolver, mcpTools) {
			return []ToolCall{{Name: fnName, Arguments: args}}
		}
		injectedResolvers[needsResolver] = true
		return []ToolCall{{Name: needsResolver, Arguments: map[string]any{}}}
	}

	return []ToolCall{{Name: fnName, Arguments: args}}
}

// write path: resolve id references when possible, never drop, never inject/deduce a resolver,
// never canonicalize value fields. On failure the call passes through unchanged.
func resolveWriteCall(fnName string, args map[string]any, mcpTools []mcp.Tool, accumulatedResults []ToolExecutionResult, resolverMap map[string]string, query string) {
	tool := toolByName(mcpTools, fnName)

	var idParamNames []string
	var lookupParams []string
	for paramName := range args {
		if _, ok := lookupResolver(resolverMap, fnName, paramName); !ok {
			continue
		}
		if isIDParamName(paramName) {
			idParamNames = append(idParamNames, paramName)
		} else if paramSchemaType(tool, paramName) == "string" {
			lookupParams = append(lookupParams, paramName)
		}
	}

	pendingID := ""
	for _, p := range idParamNames {
		resolverTool, _ := lookupResolver(resolverMap, fnName, p)
		supplied := strings.TrimSpace(fmt.Sprintf("%v", args[p]))
		if supplied == "" || supplied == "<nil>" {
			pendingID = p
			continue
		}
		if !resolverToolAvailable(resolverTool, mcpTools) {
			continue // lenient: leave as-is
		}
		records := findResolverRecords(resolverTool, accumulatedResults)
		if len(records) == 0 {
			continue // no reference data yet: leave as-is
		}
		idField := inferIdField(records, p)
		if recordHasExactID(supplied, records, idField) || looksCanonicalID(supplied, records, idField) {
			continue // Already a valid ID
		}
		if m := bestMatch(supplied, records, idField, 0.6); m != nil {
			if idVal, ok := m.Record[idField]; ok {
				args[p] = idVal
				log.Printf("[resolveIdentifiers] Auto-corrected %s: %q -> %v via '%s'", p, supplied, idVal, resolverTool)
			}
		}
	}

	if pendingID == "" {
		return
	}

	resolverTool, _ := lookupResolver(resolverMap, fnName, pendingID)
	if !resolverToolAvailable(resolverTool, mcpTools) {
		return
	}
	records := findResolverRecords(resolverTool, accumulatedResults)
	if len(records) == 0 {
		return
	}
	idField := inferIdField(records, pendingID)

	m := (*RecordMatch)(nil)
	for _, lp := range lookupParams {
		val := strings.TrimSpace(fmt.Sprintf("%v", args[lp]))
		if val == "" || val == "<nil>" {
			continue
		}
		if m = bestMatch(val, records, idField, 0.6); m != nil {
			if idVal, ok := m.Record[idField]; ok {
				args[pendingID] = idVal
				log.Printf("[resolveIdentifiers] Auto-resolved %s id to %v via lookup param '%s'", fnName, idVal, lp)
			}
			return
		}
	}

	if m = matchRecordFromQuery(query, records, idField); m != nil {
		if idVal, ok := m.Record[idField]; ok {
			args[pendingID] = idVal
			log.Printf("[resolveIdentifiers] Auto-resolved %s id to %v via query", fnName, idVal)
		}
	}
}

func isIDParamName(name string) bool {
	_, ok := idTail(name)
	return ok
}

// idTail returns the trailing id token of a name (entity+id forms like "asset_id", "assetId",
// "assetID", "ASSET_ID") and whether the name is id-shaped. Case-tolerant across Id/ID/iD/_id.
// Non-id words that merely end with lowercase "id" ("avoid", "valid") are correctly rejected.
func idTail(name string) (string, bool) {
	l := len(name)
	if l < 2 {
		return "", false
	}
	if strings.ToLower(name) == "id" {
		return name, true
	}
	if l >= 3 && strings.HasSuffix(strings.ToLower(name), "_id") {
		return name[l-3:], true
	}
	tail := name[l-2:]
	switch tail {
	case "Id", "ID", "iD":
		return tail, true
	}
	return "", false
}

func recordHasExactID(value string, records []map[string]any, idField string) bool {
	for _, r := range records {
		if fmt.Sprintf("%v", r[idField]) == value {
			return true
		}
	}
	return false
}

// looksCanonicalID reports whether value already carries the common prefix of the resolver's
// present in the cached records yet.
func looksCanonicalID(value string, records []map[string]any, idField string) bool {
	if value == "" || len(records) == 0 {
		return false
	}

	prefix := ""
	first := true
	for _, r := range records {
		v := strings.TrimSpace(strings.ToLower(fmt.Sprintf("%v", r[idField])))
		if v == "" || v == "<nil>" {
			continue
		}
		if first {
			prefix = v
			first = false
			continue
		}
		for len(prefix) > 0 && !strings.HasPrefix(v, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
		if prefix == "" {
			return false
		}
	}

	return len(prefix) >= 3 && strings.HasPrefix(strings.ToLower(value), prefix)
}

// cek llm isi param dengan bener apa kagak
func resolverToolAvailable(resolverToolName string, mcpTools []mcp.Tool) bool {
	for _, t := range mcpTools {
		if t.Name == resolverToolName {
			return true
		}
	}
	return false
}

// narik hasil dari kumpulan result.
func findResolverRecords(resolverTool string, accumulatedResults []ToolExecutionResult) []map[string]any {
	var records []map[string]any
	for _, r := range accumulatedResults {
		if r.Tool == resolverTool {
			records = append(records, extractRecords(r.Result)...)
		}
	}
	return records
}

// pembuka wrapper status dan company karena yang bikin backend radak geblek
func extractRecords(result any) []map[string]any {
	var records []map[string]any

	switch v := result.(type) {
	case []map[string]any:
		return v
	case []any:
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				records = append(records, m)
			}
		}
		return records
	case map[string]any:
		for _, val := range v {
			if list, ok := val.([]any); ok && len(list) > 0 {
				if _, isMap := list[0].(map[string]any); isMap {
					for _, item := range list {
						if m, ok := item.(map[string]any); ok {
							records = append(records, m)
						}
					}
					return records
				}
			} else if list, ok := val.([]map[string]any); ok && len(list) > 0 {
				return list
			}
		}
	}

	return records
}

// cari kolom mana yang id
func inferIdField(records []map[string]any, paramName string) string {
	if len(records) == 0 {
		return ""
	}

	sample := records[0]
	candidates := []string{
		paramName,
		strings.ReplaceAll(paramName, "_id", "Id"),
		"id",
		"Id",
		"_id",
		"ID",
		"iD",
	}

	for _, candidate := range candidates {
		if _, ok := sample[candidate]; ok {
			return candidate
		}
	}

	var idKeys []string
	for k := range sample {
		if _, ok := idTail(k); ok {
			idKeys = append(idKeys, k)
		}
	}
	slices.Sort(idKeys)
	if len(idKeys) > 0 {
		return idKeys[0]
	}

	return ""
}

func resolverNeedsNoArgs(resolverToolName string, mcpTools []mcp.Tool) bool {
	for _, t := range mcpTools {
		if t.Name == resolverToolName {
			var required []string
			for _, r := range t.InputSchema.Required {
				if !slices.Contains(INTERNAL_PARAM_NAMES, r) {
					required = append(required, r)
				}
			}
			return len(required) == 0
		}
	}
	return true
}

func lookupResolver(resolverMap map[string]string, toolName, paramName string) (string, bool) {
	if r, ok := resolverMap[toolName+":"+paramName]; ok && r != "" {
		return r, true
	}
	r, ok := resolverMap[paramName]
	return r, ok
}

func normalizeToolName(name string) string {
	var sb strings.Builder
	runes := []rune(name)
	for i, r := range runes {
		if r == '-' || r == '.' {
			sb.WriteRune('_')
		} else if r >= 'A' && r <= 'Z' {
			if i > 0 && runes[i-1] != '_' && runes[i-1] != '-' && runes[i-1] != '.' && (runes[i-1] < 'A' || runes[i-1] > 'Z') {
				sb.WriteRune('_')
			}
			sb.WriteRune(r + ('a' - 'A'))
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

var readActionVerbs = []string{
	"get", "list", "search", "query", "fetch", "read", "find", "view", "show",
	"lookup", "retrieve", "inspect", "scan", "check", "filter",
}

var writeActionVerbs = []string{
	"create", "update", "delete", "add", "remove", "edit", "modify",
	"insert", "register", "save", "set", "generate", "upsert", "reset",
	"post", "patch", "put", "archive", "purge",
}

func isActionVerb(token string) bool {
	for _, v := range readActionVerbs {
		if token == v {
			return true
		}
	}
	for _, v := range writeActionVerbs {
		if token == v {
			return true
		}
	}
	return false
}

// splitActionVerb separates a tool name into its action verb and entity. Handles snake_case,
// camelCase, kebab-case, prefix and suffix forms.
func splitActionVerb(name string) (verb string, entity string) {
	norm := normalizeToolName(name)
	tokens := strings.Split(strings.ToLower(strings.Trim(norm, "_")), "_")
	nonEmpty := tokens[:0]
	for _, t := range tokens {
		if t != "" {
			nonEmpty = append(nonEmpty, t)
		}
	}
	tokens = nonEmpty

	if len(tokens) == 0 {
		return "", name
	}
	if len(tokens) == 1 {
		if isActionVerb(tokens[0]) {
			return tokens[0], ""
		}
		return "", tokens[0]
	}
	if isActionVerb(tokens[0]) {
		return tokens[0], strings.Join(tokens[1:], "_")
	}
	if last := tokens[len(tokens)-1]; isActionVerb(last) {
		return last, strings.Join(tokens[:len(tokens)-1], "_")
	}
	return "", strings.Join(tokens, "_")
}

func isReadOnlyTool(name string) bool {
	verb, _ := splitActionVerb(name)
	for _, v := range readActionVerbs {
		if verb == v {
			return true
		}
	}
	return false
}

// cari kolom id
func inferResolverMap(mcpTools []mcp.Tool) map[string]string {
	resolverMap := make(map[string]string)
	if len(mcpTools) == 0 {
		return resolverMap
	}

	for _, t := range mcpTools {
		toolEntity := entityFromToolName(t.Name)
		for p := range t.InputSchema.Properties {
			if slices.Contains(INTERNAL_PARAM_NAMES, p) {
				continue
			}
			if !isIDParamName(p) {
				continue
			}
			entity := entityFromIDParam(p)
			if entity == "" {
				entity = toolEntity
			}
			if entity == "" {
				continue
			}
			if r := findResolverForEntity(entity, mcpTools, t.Name); r != "" {
				resolverMap[t.Name+":"+p] = r
				if _, exists := resolverMap[p]; !exists {
					resolverMap[p] = r
				}
			}
		}
	}

	for _, t := range mcpTools {
		ownResolver := ""
		for p := range t.InputSchema.Properties {
			if isIDParamName(p) {
				if r, ok := lookupResolver(resolverMap, t.Name, p); ok {
					ownResolver = r
					break
				}
			}
		}
		if ownResolver == "" {
			continue
		}
		for p, prop := range t.InputSchema.Properties {
			if isIDParamName(p) || slices.Contains(INTERNAL_PARAM_NAMES, p) {
				continue
			}
			if _, ok := lookupResolver(resolverMap, t.Name, p); ok {
				continue
			}
			if propMap, ok := prop.(map[string]any); ok {
				if tType, ok := propMap["type"].(string); ok && tType == "string" {
					resolverMap[t.Name+":"+p] = ownResolver
					if _, exists := resolverMap[p]; !exists {
						resolverMap[p] = ownResolver
					}
				}
			}
		}
	}

	return resolverMap
}

func entityFromIDParam(name string) string {
	tail, ok := idTail(name)
	if !ok {
		return ""
	}
	return strings.TrimSuffix(name, tail)
}

func entityFromToolName(name string) string {
	_, entity := splitActionVerb(name)
	return entity
}

func stemEntity(e string) string {
	e = strings.ToLower(strings.TrimSpace(e))
	if strings.HasSuffix(e, "ies") && len(e) > 3 {
		return e[:len(e)-3] + "y"
	}
	if strings.HasSuffix(e, "es") && len(e) > 3 {
		return e[:len(e)-2]
	}
	if strings.HasSuffix(e, "s") && len(e) > 2 {
		return e[:len(e)-1]
	}
	return e
}

func matchEntity(a, b string) bool {
	sa := stemEntity(a)
	sb := stemEntity(b)
	if sa == "" || sb == "" {
		return false
	}
	return sa == sb || strings.Contains(sa, sb) || strings.Contains(sb, sa) ||
		strings.Contains(strings.ToLower(a), strings.ToLower(b)) ||
		strings.Contains(strings.ToLower(b), strings.ToLower(a))
}

// findResolverForEntity picks the tool that can fetch records for the entity. Read tools are
// preferred so write tools never become their own source of truth.
func findResolverForEntity(entity string, mcpTools []mcp.Tool, selfName string) string {
	for _, t := range mcpTools {
		if t.Name == selfName {
			continue
		}
		tEntity := entityFromToolName(t.Name)
		if matchEntity(tEntity, entity) && isReadOnlyTool(t.Name) {
			return t.Name
		}
	}
	for _, t := range mcpTools {
		if t.Name == selfName {
			continue
		}
		tEntity := entityFromToolName(t.Name)
		if matchEntity(tEntity, entity) {
			return t.Name
		}
	}
	return ""
}

func toolByName(mcpTools []mcp.Tool, name string) *mcp.Tool {
	for i := range mcpTools {
		if mcpTools[i].Name == name {
			return &mcpTools[i]
		}
	}
	return nil
}

func paramSchemaType(tool *mcp.Tool, paramName string) string {
	if tool == nil {
		return ""
	}
	if p, ok := tool.InputSchema.Properties[paramName].(map[string]any); ok {
		if t, ok := p["type"].(string); ok {
			return t
		}
	}
	return ""
}

// cari record yang nilainya muncul di query
func matchRecordFromQuery(query string, records []map[string]any, idField string) *RecordMatch {
	if query == "" || len(records) == 0 {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(query))
	for _, r := range records {
		for fieldName, fieldVal := range r {
			valStr := strings.TrimSpace(strings.ToLower(fmt.Sprintf("%v", fieldVal)))
			if len(valStr) >= 4 && strings.Contains(q, valStr) {
				return &RecordMatch{
					Record:     r,
					Field:      fieldName,
					CanonValue: fieldVal,
					IDField:    idField,
					IDValue:    r[idField],
					Score:      1.0,
				}
			}
		}
	}
	return nil
}
