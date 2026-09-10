package goaipackage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	GeneratorModel1 = "ox-alpha-free"
	GeneratorModel2 = "glm-5.3-flash"
	GeneratorModel3 = "mimo-v2.5"
	GeneratorModel4 = "gpt-5.6-luna"
	GeneratorModel5 = "qwen3.8-flash"
	GeneratorModel6 = "deepseek-v4-flash"
	GeneratorModel7 = "liquid/lfm-2.5-2.6b:free"
	GeneratorModel8 = "z-ai/glm-5.2:free"
)

var GeneratorModel = GeneratorModel6

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatResult struct {
	RawOutput        string `json:"raw_output"`
	InputTokens      int    `json:"input_tokens"`
	OutputTokens     int    `json:"output_tokens"`
	ReasoningDetails any    `json:"reasoning_details,omitempty"`
}

var (
	GlobalAIKey   string
	GlobalBaseURL string
)

// SetAIConfig sets global AI API credentials programmatically.
func SetAIConfig(apiKey, baseURL string) {
	if apiKey != "" {
		GlobalAIKey = apiKey
	}
	if baseURL != "" {
		GlobalBaseURL = baseURL
	}
}

// SetAPIKey sets the global AI API key programmatically.
func SetAPIKey(apiKey string) {
	GlobalAIKey = apiKey
}

// SetBaseURL sets the global AI base URL programmatically.
func SetBaseURL(baseURL string) {
	GlobalBaseURL = baseURL
}

// ChatGenerate calls the OpenRouter/OpenAI chat completion API with retry & backoff.
func ChatGenerate(ctx context.Context, messages []Message, tools any, maxNewTokens int, model string, retries int) (*ChatResult, error) {
	_ = godotenv.Load(".env")

	if model == "" {
		model = GeneratorModel
	}
	if maxNewTokens <= 0 {
		maxNewTokens = 4096
	}

	baseURL := GlobalBaseURL
	if baseURL == "" {
		baseURL = os.Getenv("BASE_AI_URL")
	}
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}

	apiKey := GlobalAIKey
	if apiKey == "" {
		apiKey = os.Getenv("AI_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}

	if strings.Contains(baseURL, "openrouter.ai") && !strings.Contains(model, "/") {
		log.Printf("[chat_generate] Model '%s' lacks vendor prefix for OpenRouter, defaulting to '%s'", model, GeneratorModel7)
		model = GeneratorModel7
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/chat/completions"

	reqBody := map[string]any{
		"model":      model,
		"messages":   messages,
		"max_tokens": maxNewTokens,
		"extra_body": map[string]any{"reasoning": map[string]any{"enabled": true}},
	}
	if tools != nil {
		reqBody["tools"] = tools
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 90 * time.Second}
	var lastErr error

	for attempt := 0; attempt <= retries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}

		req.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}

		resp, err := client.Do(req)
		if err == nil {
			defer resp.Body.Close()

			var res struct {
				Choices []struct {
					Message struct {
						Content          string `json:"content"`
						ReasoningContent string `json:"reasoning_content"`
						ReasoningDetails any    `json:"reasoning_details"`
						Reasoning        any    `json:"reasoning"`
					} `json:"message"`
				} `json:"choices"`
				Usage struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
				} `json:"usage"`
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}

			if decodeErr := json.NewDecoder(resp.Body).Decode(&res); decodeErr == nil {
				if resp.StatusCode == http.StatusOK {
					var rawOutput string
					var reasoning any

					if len(res.Choices) > 0 {
						rawOutput = res.Choices[0].Message.Content
						if strings.TrimSpace(rawOutput) == "" && strings.TrimSpace(res.Choices[0].Message.ReasoningContent) != "" {
							rawOutput = res.Choices[0].Message.ReasoningContent
						}
						reasoning = res.Choices[0].Message.ReasoningDetails
						if reasoning == nil {
							reasoning = res.Choices[0].Message.Reasoning
						}
						if reasoning == nil {
							reasoning = res.Choices[0].Message.ReasoningContent
						}
					}

					return &ChatResult{
						RawOutput:        rawOutput,
						InputTokens:      res.Usage.PromptTokens,
						OutputTokens:     res.Usage.CompletionTokens,
						ReasoningDetails: reasoning,
					}, nil
				}
				err = fmt.Errorf("api error (%d): %s", resp.StatusCode, res.Error.Message)
			} else {
				err = decodeErr
			}
		}

		lastErr = err
		wait := computeRetryWait(resp, attempt)

		if attempt < retries {
			log.Printf("[chat_generate] attempt %d/%d failed (%v), retrying in %v", attempt+1, retries+1, err, wait)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		} else {
			log.Printf("[chat_generate] all %d attempts failed: %v", retries+1, err)
		}
	}

	return nil, lastErr
}

func computeRetryWait(resp *http.Response, attempt int) time.Duration {
	if resp != nil {
		if sec, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && sec > 0 {
			return time.Duration(sec) * time.Second
		}
	}
	return time.Duration(1<<attempt) * time.Second
}

func getTemporalContextPrompt() string {
	now := time.Now()
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	startOfWeek := now.AddDate(0, 0, -(weekday - 1))

	dateReference := fmt.Sprintf(
		"Current Date Reference:\n"+
			"- Today: %s\n"+
			"- Start of Current Week (Monday): %s\n"+
			"- Current Month: %s (matches date prefix '%s')\n"+
			"- Current Year: %s\n"+
			"- Start of Current Month: %s\n"+
			"- Current ISO Timestamp: %s",
		now.Format("2006-01-02 (Monday)"),
		startOfWeek.Format("2006-01-02"),
		now.Format("January 2006"),
		now.Format("2006-01"),
		now.Format("2006"),
		now.Format("2006-01-01"),
		now.Format(time.RFC3339),
	)
	return dateReference
}

// extractContext inspects accumulated API tool results, performs analytical reasoning & filtering with the LLM,
// determines whether data is sufficient (<verdict status="ENOUGH"> vs <verdict status="NEED_MORE">),
// and returns (isEnough bool, extractedFacts string, missingInfo string).
func extractContext(ctx context.Context, query string, intent *IntentResult, accumulatedResults []ToolExecutionResult, userContext map[string]any, iteration int, maxIterations int, model string) (bool, string, string) {
	var history []ChatMessage
	if userContext != nil {
		if h, ok := userContext["history"].([]ChatMessage); ok {
			history = h
		}
	}
	historyStr := FormatChatHistoryForLlm(history)
	hasAttachment := false
	if userContext != nil {
		if attachText, ok := userContext["attachment_text"].(string); ok && attachText != "" {
			hasAttachment = true
		}
	}

	onlyNoTools := len(accumulatedResults) > 0
	for _, res := range accumulatedResults {
		if res.Tool != "no_tools" && res.Tool != "no_tool" {
			onlyNoTools = false
			break
		}
	}

	if (len(accumulatedResults) == 0 || onlyNoTools) && historyStr == "" && !hasAttachment {
		return true, "No records found from API tool.", ""
	}

	if model == "" {
		model = GeneratorModel
	}

	var rawDataStr string
	if len(accumulatedResults) == 0 || onlyNoTools {
		rawDataStr = "No external tool executed."
	} else {
		rawDataStr = formatMultiRawDataForLlm(accumulatedResults)
	}
	temporalInfo := getTemporalContextPrompt()
	companyInfo := ""
	if userContext != nil {
		if comp, ok := userContext["company"].(string); ok && comp != "" {
			companyInfo = fmt.Sprintf(" The requesting user belongs to company '%s'.", comp)
		} else if compInfo, ok := userContext["company_info"].(map[string]any); ok {
			if compName, ok := compInfo["company_name"].(string); ok && compName != "" {
				companyInfo = fmt.Sprintf(" The requesting user belongs to company '%s'.", compName)
			}
		}
	}

	systemPrompt := fmt.Sprintf(
		"You are an analytical data extraction, context filtering, and reasoning engine.%s\n\n"+
			"%s\n\n"+
			"Your role is to analyze the user's question across all available context:\n"+
			"1. Data returned from tools\n"+
			"2. Recent Conversation History\n"+
			"3. Attached documents (if provided)\n\n"+
			"You must perform all necessary analytical reasoning, date filtering, counting, filtering of chat history, and context extraction, "+
			"and determine whether additional data is needed to answer the question.\n\n"+
			"Instructions & Rules:\n"+
			"1. **Sufficiency Evaluation (Line 1)**:\n"+
			"   - On the VERY FIRST LINE of your response, output a verdict tag:\n"+
			"     * If crucial information is still missing from the tool data to answer what the user asked: `<verdict status=\"NEED_MORE\" missing=\"specific missing detail\"/>`\n"+
			"     * If and ONLY if the gathered data and context contain the actual answers to the user's specific request: `<verdict status=\"ENOUGH\"/>`\n"+
			"     * If the user's question does not require additional data: `<verdict status=\"ENOUGH\"/>`\n"+
			"2. **Chat History Filtering & Distillation**:\n"+
			"   - Carefully inspect the Recent Conversation History.\n"+
			"   - Filter and extract ONLY the specific facts, comparisons, IDs, file analyses, or context relevant to the user's current question.\n"+
			"   - Exclude unrelated pleasantries or outdated chatter.\n"+
			"3. **Temporal & Date Reasoning**:\n"+
			"   - Use the Current Date Reference above to resolve relative time terms (e.g. 'this month', 'today', 'this year', 'last week').\n"+
			"4. **Factual Accuracy & Completeness**:\n"+
			"   - If no records match the criteria/filters, explicitly state that no matching records were found.\n"+
			"   - Do NOT invent, assume, or hallucinate records not in the data.\n"+
			"5. **Live Data Precedence & Freshness**:\n"+
			"   - Live data from tools is the single source of truth and strictly supersedes any past statements in conversation history.\n"+
			"6. **Output Format**:\n"+
			"   - Following the verdict tag, present the distilled and extracted facts clearly.",
		companyInfo,
		temporalInfo,
	)

	var userPromptBuilder strings.Builder
	if historyStr != "" {
		userPromptBuilder.WriteString(fmt.Sprintf("=== RECENT CONVERSATION HISTORY (RAW) ===\n%s\n\n", historyStr))
	}
	if userContext != nil {
		if attachText, ok := userContext["attachment_text"].(string); ok && attachText != "" {
			attachName := "Attached Document"
			if name, ok := userContext["attachment_name"].(string); ok && name != "" {
				attachName = name
			}
			userPromptBuilder.WriteString(fmt.Sprintf("=== ATTACHED DOCUMENT CONTEXT (%s) ===\n%s\n\n", attachName, attachText))
		}
	}
	userPromptBuilder.WriteString(fmt.Sprintf("=== LIVE DATABASE DATA ===\n%s\n\n=== USER QUESTION ===\n%s", rawDataStr, query))

	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPromptBuilder.String()},
	}

	res, err := ChatGenerate(ctx, messages, nil, 32768, model, 3)
	if err != nil {
		log.Printf("[extractContext] Failed to extract context with LLM: %v", err)
		return true, rawDataStr, ""
	}
	log.Printf("[extractContext] Iteration %d tokens -> input: %d, output: %d, total: %d", iteration, res.InputTokens, res.OutputTokens, res.InputTokens+res.OutputTokens)

	rawExtraction := strings.TrimSpace(res.RawOutput)
	if rawExtraction == "" {
		rawExtraction = rawDataStr
	}

	// Parse verdict tag
	verdictRegex := regexp.MustCompile(`(?i)<verdict\s+status=["'](ENOUGH|NEED_MORE)["'](?:\s+missing=["'](.*?)["'])?`)
	isEnough := true
	missingInfo := ""

	if match := verdictRegex.FindStringSubmatch(rawExtraction); len(match) >= 2 {
		status := strings.ToUpper(match[1])
		if len(match) >= 3 {
			missingInfo = strings.TrimSpace(match[2])
		}
		if status == "NEED_MORE" && iteration < maxIterations {
			isEnough = false
		}
	}

	// 2. Never allow extractContext to claim ENOUGH if target tool has not executed yet
	if intent == nil {
		intent = searchIntent(ctx, query, userContext, model)
	}
	hasExecutedTarget := false
	for _, res := range accumulatedResults {
		if res.Tool == intent.TargetTool {
			hasExecutedTarget = true
			break
		}
	}
	if !hasExecutedTarget && intent.TargetTool != "" && intent.TargetTool != "no_tools" && iteration < maxIterations {
		isEnough = false
		if missingInfo == "" {
			missingInfo = fmt.Sprintf("The requested action for tool '%s' has not been executed yet.", intent.TargetTool)
		}
	}

	tagStripRegex := regexp.MustCompile(`(?i)<verdict\s+.*?/>`)
	extractedFacts := strings.TrimSpace(tagStripRegex.ReplaceAllString(rawExtraction, ""))

	if extractedFacts == "" {
		extractedFacts = rawDataStr
	}

	return isEnough, extractedFacts, missingInfo
}

// parseIntentJSON parses and normalizes the JSON output into tools and reason.
func parseIntentJSON(raw string) (*IntentResult, error) {
	clean := strings.TrimSpace(raw)
	// Strip markdown code fences if present
	reFence := regexp.MustCompile(`(?s)` + "```" + `(?:json)?\s*(.*?)\s*` + "```")
	if match := reFence.FindStringSubmatch(clean); len(match) > 1 {
		clean = strings.TrimSpace(match[1])
	} else {
		start := strings.Index(clean, "{")
		end := strings.LastIndex(clean, "}")
		if start >= 0 && end > start {
			clean = clean[start : end+1]
		}
	}

	var rawMap map[string]any
	if err := json.Unmarshal([]byte(clean), &rawMap); err != nil {
		return nil, fmt.Errorf("failed to parse intent JSON: %w (raw: %s)", err, clean)
	}

	result := &IntentResult{}
	if r, ok := rawMap["reason"].(string); ok {
		result.Reason = r
	}

	// Extract tools (handle []string, single string, or "tool" / "target_tool" keys)
	if toolsList, ok := rawMap["tools"].([]any); ok {
		for _, t := range toolsList {
			if str, ok := t.(string); ok && strings.TrimSpace(str) != "" {
				result.Tools = append(result.Tools, strings.TrimSpace(str))
			}
		}
	} else if toolStr, ok := rawMap["tools"].(string); ok && strings.TrimSpace(toolStr) != "" {
		result.Tools = []string{strings.TrimSpace(toolStr)}
	} else if singleTool, ok := rawMap["tool"].(string); ok && strings.TrimSpace(singleTool) != "" {
		result.Tools = []string{strings.TrimSpace(singleTool)}
	} else if targetTool, ok := rawMap["target_tool"].(string); ok && strings.TrimSpace(targetTool) != "" {
		result.Tools = []string{strings.TrimSpace(targetTool)}
	}

	// Derive target tool and automatic flags
	if len(result.Tools) > 0 {
		result.TargetTool = result.Tools[0]
	} else {
		result.TargetTool = "no_tools"
	}

	toolLower := strings.ToLower(result.TargetTool)
	if toolLower == "no_tools" || toolLower == "no_tool" || toolLower == "none" || len(result.Tools) == 0 {
		result.IsOffTopic = true
		result.Category = "unrelated"
	} else {
		result.Category = result.TargetTool
		result.IsMutation = !isReadOnlyTool(result.TargetTool)
		result.IsDelete = strings.Contains(toolLower, "delete") || strings.Contains(toolLower, "remove")
		result.IsPDFReport = strings.Contains(toolLower, "pdf") || strings.Contains(toolLower, "csv") || strings.Contains(toolLower, "excel")
	}

	return result, nil
}

// searchIntent is an LLM agent function that semantically classifies user intent.
func searchIntent(ctx context.Context, query string, userContext map[string]any, model string, intentOpts ...RequestIntentOption) *IntentResult {
	if model == "" {
		model = GeneratorModel
	}

	var customIntentPrompt string
	if len(intentOpts) > 0 {
		ip := NewIntentPrompt(intentOpts...)
		if ip.Prompt != "" {
			customIntentPrompt = ip.Prompt
		} else if ip.Text != "" {
			customIntentPrompt = ip.Text
		}
	}
	if customIntentPrompt == "" && userContext != nil {
		if ip, ok := userContext["intent_prompt"].(IntentPrompt); ok {
			customIntentPrompt = ip.Prompt
			if customIntentPrompt == "" {
				customIntentPrompt = ip.Text
			}
		} else if ipPtr, ok := userContext["intent_prompt"].(*IntentPrompt); ok && ipPtr != nil {
			customIntentPrompt = ipPtr.Prompt
			if customIntentPrompt == "" {
				customIntentPrompt = ipPtr.Text
			}
		} else if pStr, ok := userContext["intent_prompt"].(string); ok {
			customIntentPrompt = pStr
		}
	}

	baseInstruction := `You are an Intent Classification Agent.
Analyze the user query and any context to determine which tools are required and provide the reasoning.

You must respond with ONLY a valid JSON object matching this schema:
{
  "tools": ["tool_name"],
  "reason": "brief explanation of why this tool is selected or why no tools are needed"
}

If no tools are required (e.g. greetings or off-topic questions), respond with:
{
  "tools": [],
  "reason": "greeting / conversation without tool usage"
}

IMPORTANT: Output ONLY the raw JSON object. Do not include markdown codeblocks or conversational text.`

	systemInstruction := baseInstruction
	if customIntentPrompt != "" {
		systemInstruction += fmt.Sprintf("\n\nCustom Intent Instructions:\n%s", customIntentPrompt)
	}

	var userPrompt strings.Builder
	if userContext != nil {
		if hist, ok := userContext["history"].([]ChatMessage); ok && len(hist) > 0 {
			userPrompt.WriteString(fmt.Sprintf("=== PREVIOUS CONVERSATION HISTORY ===\n%s\n\n", FormatChatHistoryForLlm(hist)))
		}
		hasUploaded, _ := userContext["has_user_uploaded_file"].(bool)
		if attachText, ok := userContext["attachment_text"].(string); ok && attachText != "" {
			attachName := "Attached Document"
			if name, ok := userContext["attachment_name"].(string); ok && name != "" {
				attachName = name
			}
			if hasUploaded {
				userPrompt.WriteString(fmt.Sprintf("=== USER-UPLOADED FILE FOR THIS REQUEST (%s) ===\n%s\n\n", attachName, attachText))
			} else {
				userPrompt.WriteString(fmt.Sprintf("=== PREVIOUS CONVERSATION REFERENCE DOCUMENT (%s) ===\n%s\n\n", attachName, attachText))
			}
		}
	}
	userPrompt.WriteString(fmt.Sprintf("=== USER QUERY ===\n%s", query))

	messages := []Message{
		{Role: "system", Content: systemInstruction},
		{Role: "user", Content: userPrompt.String()},
	}

	res, err := ChatGenerate(ctx, messages, nil, 4096, model, 2)
	if err != nil {
		log.Printf("[searchIntent] ChatGenerate error: %v", err)
		return &IntentResult{
			Category:   "unknown",
			TargetTool: "no_tools",
			Reason:     err.Error(),
		}
	}

	result, err := parseIntentJSON(res.RawOutput)
	if err != nil {
		log.Printf("[searchIntent] %v", err)
		return &IntentResult{
			Category:   "unknown",
			TargetTool: "no_tools",
			Reason:     err.Error(),
		}
	}

	log.Printf("[searchIntent] Query: %q -> Category: %q, TargetTool: %q, IsMutation: %t, IsDelete: %t, IsPDFReport: %t, IsOffTopic: %t",
		query, result.Category, result.TargetTool, result.IsMutation, result.IsDelete, result.IsPDFReport, result.IsOffTopic)

	return result
}

// SearchIntent classifies user intent and returns the result directly as a JSON string.
func SearchIntent(ctx context.Context, query string, userContext map[string]any, model string, intentOpts ...RequestIntentOption) (string, error) {
	res := searchIntent(ctx, query, userContext, model, intentOpts...)
	return res.ToJSON()
}

func hasTool(tools []mcp.Tool, name string) bool {
	cleanTarget := strings.Trim(strings.ToLower(name), "_")
	for _, t := range tools {
		if strings.EqualFold(strings.Trim(t.Name, "_"), cleanTarget) {
			return true
		}
	}
	return false
}

// CallTools orchestrates multi-turn tool calling, reasoning loops, and returns the result directly as a JSON string.
func CallTools(ctx context.Context, client *client.Client, mcpTools []mcp.Tool, query string, role string, userContext map[string]any, maxIterations int, model string, toolsOpts ...RequestToolsOption) (string, error) {
	res, err := executeCallTools(ctx, client, mcpTools, query, role, userContext, maxIterations, model, toolsOpts...)
	if err != nil {
		return "", err
	}
	return res.ToJSON()
}

// executeCallTools handles internal tool execution logic
func executeCallTools(ctx context.Context, client *client.Client, mcpTools []mcp.Tool, query string, role string, userContext map[string]any, maxIterations int, model string, toolsOpts ...RequestToolsOption) (*AgentResult, error) {
	// 1. RBAC at the top: immediately enforce role permissions and filter tools
	role = strings.ToLower(strings.TrimSpace(role))
	availableTools := filterRoles(mcpTools, role)
	if len(availableTools) == 0 {
		return &AgentResult{Context: "I do not have permissions or tools to access that information."}, nil
	}
	UpdateKnownTools(availableTools)

	if model == "" {
		model = GeneratorModel
	}
	if maxIterations < 3 {
		maxIterations = 3
	}

	// 2. Resolve intent via agent function searchIntent()
	intent := searchIntent(ctx, query, userContext, model)

	// Immediate rejection: off-topic requests
	if intent.IsOffTopic || intent.Category == "unrelated" {
		log.Printf("[callTools] Blocked off-topic request (%s): %q", intent.Category, query)
		return &AgentResult{
			Context: "I do not have information or unable to do that.",
		}, nil
	}

	// 3. RBAC permission check: if intent requires a tool, verify it exists in availableTools
	if intent.TargetTool != "" && intent.TargetTool != "no_tools" && !hasTool(availableTools, intent.TargetTool) {
		log.Printf("[callTools] Blocked query (%s): tool %q not permitted for role %s", intent.Category, intent.TargetTool, role)
		return &AgentResult{Context: fmt.Sprintf("You do not have permission to use %s.", intent.TargetTool)}, nil
	}

	// Apply Caveman tool catalog compression skill
	availableTools = ShrinkToolCatalog(availableTools)

	resolverMap := inferResolverMap(availableTools)
	companyName := ""
	if userContext != nil {
		if comp, ok := userContext["company"].(string); ok && comp != "" {
			companyName = comp
		} else if compInfo, ok := userContext["company_info"].(map[string]any); ok {
			if compName, ok := compInfo["company_name"].(string); ok && compName != "" {
				companyName = compName
			}
		}
	}

	// Extract custom prompt / guardrails from ToolsPrompt or userContext
	var customToolsPrompt string
	if len(toolsOpts) > 0 {
		tp := NewToolsPrompt(toolsOpts...)
		if tp.Prompt != "" {
			customToolsPrompt = tp.Prompt
		}
	}
	if customToolsPrompt == "" && userContext != nil {
		if tp, ok := userContext["tools_prompt"].(ToolsPrompt); ok {
			customToolsPrompt = tp.Prompt
		} else if tpPtr, ok := userContext["tools_prompt"].(*ToolsPrompt); ok && tpPtr != nil {
			customToolsPrompt = tpPtr.Prompt
		} else if pStr, ok := userContext["tools_prompt"].(string); ok {
			customToolsPrompt = pStr
		}
	}

	executedSignatures := make(map[string]bool)
	var accumulatedResults []ToolExecutionResult
	extractedFactsSoFar := ""
	missingInfoHint := ""

	toolGuide := buildToolGuide(availableTools)
	baseSystemInstruction := `You are an AI Tool Calling Assistant.
Analyze the user request and conversation history to select and execute the necessary tools accurately.

Guidelines:
- If you encounter identifiers, preserve their exact format.
- Provide direct and concise tool calls without conversational pleasantries.
- Populate all required tool arguments based on the user request and context.

Available Tools Guide:
%s

Execution Rules:
1. **Thinking (<thought>...</thought>)**: Analyze the user's intent and determine what data or tools are required.
2. **Tool Execution**: Call only authorized tools available in the Available Tools Guide.
3. **No Tools Needed**: If no tools are required (e.g. conversational greetings or questions that do not need external actions), call 'no_tools'.

Output format:
If tools are needed:
<thought>Brief reasoning on why the tool is selected and what arguments are used</thought>
<tool_call>
{"name": "tool_name", "arguments": {...}}
</tool_call>

If NO tools are needed:
<thought>Brief reasoning</thought>
<tool_call>
{"name": "no_tools", "arguments": {"reason": "greeting or no tool required"}}
</tool_call>`

	systemInstruction := fmt.Sprintf(baseSystemInstruction, toolGuide)
	if customToolsPrompt != "" {
		systemInstruction += fmt.Sprintf("\n\nCustom Instructions & Guardrails:\n%s", customToolsPrompt)
	}

	if companyName != "" {
		systemInstruction += fmt.Sprintf("\nUser context / organization: '%s'.", companyName)
	}

	var history []ChatMessage
	if userContext != nil {
		if h, ok := userContext["history"].([]ChatMessage); ok {
			history = h
		}
	}
	historyStr := FormatChatHistoryForLlm(history)

	var routerUserPrompt strings.Builder
	if historyStr != "" {
		routerUserPrompt.WriteString(fmt.Sprintf("=== RECENT CONVERSATION HISTORY ===\n%s\n\n", historyStr))
	}
	if userContext != nil {
		if attachText, ok := userContext["attachment_text"].(string); ok && attachText != "" {
			attachName := "Attached Document"
			if name, ok := userContext["attachment_name"].(string); ok && name != "" {
				attachName = name
			}
			routerUserPrompt.WriteString(fmt.Sprintf("=== ATTACHED DOCUMENT CONTEXT (%s) ===\n%s\n\n", attachName, attachText))
		}
	}
	routerUserPrompt.WriteString(fmt.Sprintf("=== QUESTION ===\n%s", query))

	// 3. Conversational message history maintained across iterations
	messages := []Message{
		{Role: "system", Content: systemInstruction},
		{Role: "user", Content: routerUserPrompt.String()},
	}

	for iteration := 1; iteration <= maxIterations; iteration++ {
		res, err := ChatGenerate(ctx, messages, nil, 32768, model, 3)
		if err != nil {
			log.Printf("[callTools] ChatGenerate error on iteration %d: %v", iteration, err)
			break
		}
		log.Printf("[callTools] Iteration %d tokens -> input: %d, output: %d, total: %d", iteration, res.InputTokens, res.OutputTokens, res.InputTokens+res.OutputTokens)

		rawOutput := res.RawOutput
		log.Printf("[callTools] Iteration %d LLM output: %s", iteration, rawOutput)

		toolCalls := tryParseToolCalls(rawOutput)

		// Filter out any tool calls not present in availableTools
		var authorizedCalls []ToolCall
		for _, tc := range toolCalls {
			if tc.Name == "no_tools" || tc.Name == "no_tool" || hasTool(availableTools, tc.Name) {
				authorizedCalls = append(authorizedCalls, tc)
			} else {
				log.Printf("[callTools] Dropped unauthorized tool call %q (not available in session)", tc.Name)
			}
		}

		toolCalls = resolveIdentifiers(authorizedCalls, availableTools, accumulatedResults, resolverMap, query)

		var newCalls []ToolCall
		for _, tc := range toolCalls {
			argsJson, _ := json.Marshal(tc.Arguments)
			sig := fmt.Sprintf("%s:%s", tc.Name, string(argsJson))
			if !executedSignatures[sig] {
				executedSignatures[sig] = true
				newCalls = append(newCalls, tc)
			}
		}

		hasRealTools := false
		var realCalls []ToolCall
		for _, tc := range newCalls {
			if tc.Name != "no_tools" && tc.Name != "no_tool" {
				hasRealTools = true
				realCalls = append(realCalls, tc)
			}
		}

		if !hasRealTools {
			// If the user's intent requires a tool available in this session, guide the model to execute it
			if iteration < maxIterations && hasTool(availableTools, intent.TargetTool) {
				promptText := fmt.Sprintf("Please proceed with the operation by calling the appropriate tool (%s) from your Available Tools Guide.", intent.TargetTool)
				messages = append(messages,
					Message{Role: "assistant", Content: rawOutput},
					Message{Role: "user", Content: promptText},
				)
				continue
			}
			cleanReply := SanitizeModelReply(rawOutput)
			if cleanReply != "" && cleanReply == "I do not have information or unable to do that." && (intent.IsOffTopic || intent.Category == "unrelated") {
				return &AgentResult{Context: cleanReply, ToolResults: accumulatedResults}, nil
			}
			if iteration == 1 {
				// Filter history and context with extractContext
				_, facts, _ := extractContext(ctx, query, intent, accumulatedResults, userContext, iteration, maxIterations, model)
				return &AgentResult{Context: facts, ToolResults: accumulatedResults}, nil
			}
			if len(accumulatedResults) > 0 {
				_, facts, _ := extractContext(ctx, query, intent, accumulatedResults, userContext, iteration, maxIterations, model)
				attach := extractGeneratedAttachment(accumulatedResults)
				return &AgentResult{
					Context:     facts,
					Attachment:  attach,
					ToolResults: accumulatedResults,
				}, nil
			}
			if cleanReply != "" {
				return &AgentResult{Context: cleanReply, ToolResults: accumulatedResults}, nil
			}
			return &AgentResult{Context: "I do not have information or unable to do that.", ToolResults: accumulatedResults}, nil
		}

		newCalls = realCalls

		execResults := executeToolsParallel(ctx, client, newCalls, availableTools, ToUserAuth(userContext, role))
		accumulatedResults = append(accumulatedResults, execResults...)

		isEnough, facts, missing := extractContext(ctx, query, intent, accumulatedResults, userContext, iteration, maxIterations, model)
		extractedFactsSoFar = facts
		missingInfoHint = missing

		if isEnough || iteration >= maxIterations {
			break
		}

		// Keep true conversational history in the loop for the next iteration:
		messages = append(messages, Message{Role: "assistant", Content: rawOutput})
		toolResultText := formatMultiRawDataForLlm(execResults)
		userFeedback := fmt.Sprintf("Tool Execution Results:\n%s", toolResultText)
		if missingInfoHint != "" {
			userFeedback += fmt.Sprintf("\nMissing information needed to complete the user's request: %s\nProceed with the next tool call.", missingInfoHint)
		}
		messages = append(messages, Message{Role: "user", Content: userFeedback})
	}

	generatedAttach := extractGeneratedAttachment(accumulatedResults)
	if generatedAttach != nil {
		extractedFactsSoFar += fmt.Sprintf("\n\n=== GENERATED ATTACHMENT ===\nA file report has been generated successfully and attached to this message:\n- Filename: %s\n- URL: %s\nInform the user that the report has been generated and is attached to this message for them to download directly.", generatedAttach.Name, generatedAttach.URL)
	}

	return &AgentResult{
		Context:     extractedFactsSoFar,
		Attachment:  generatedAttach,
		ToolResults: accumulatedResults,
	}, nil
}

func extractGeneratedAttachment(accumulated []ToolExecutionResult) *AttachmentInfo {
	for i := len(accumulated) - 1; i >= 0; i-- {
		res := accumulated[i]
		toolLower := strings.ToLower(res.Tool)
		if strings.Contains(toolLower, "generate_pdf") || strings.Contains(toolLower, "pdf") ||
			strings.Contains(toolLower, "generate_csv") || strings.Contains(toolLower, "csv") ||
			strings.Contains(toolLower, "generate_excel") || strings.Contains(toolLower, "excel") {
			if m, ok := res.Result.(map[string]any); ok {
				downloadURL, _ := m["download_url"].(string)
				filename, _ := m["filename"].(string)
				if downloadURL != "" {
					if filename == "" {
						filename = filepath.Base(downloadURL)
					}
					var size int64
					if sNum, ok := m["size"].(float64); ok {
						size = int64(sNum)
					} else if sInt, ok := m["size"].(int64); ok {
						size = sInt
					} else if sInt, ok := m["size"].(int); ok {
						size = int64(sInt)
					}
					mimeType, _ := m["type"].(string)
					if mimeType == "" {
						ext := strings.ToLower(filepath.Ext(filename))
						switch ext {
						case ".csv":
							mimeType = "text/csv"
						case ".xlsx":
							mimeType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
						default:
							mimeType = "application/pdf"
						}
					}
					return &AttachmentInfo{
						Name: filename,
						URL:  downloadURL,
						Type: mimeType,
						Size: size,
					}
				}
			}
		}
	}
	return nil
}

func cleanContextPayload(contextStr string) string {
	trimmed := strings.TrimSpace(contextStr)
	if trimmed == "" {
		return "No records found."
	}
	// If contextStr is a JSON payload with "context" field, extract it directly
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		var m map[string]any
		if err := json.Unmarshal([]byte(trimmed), &m); err == nil {
			if ctxVal, ok := m["context"].(string); ok && ctxVal != "" {
				trimmed = strings.TrimSpace(ctxVal)
			}
		}
	}
	reConsecutiveNewlines := regexp.MustCompile(`\n{3,}`)
	return reConsecutiveNewlines.ReplaceAllString(trimmed, "\n\n")
}

// GenerateResponse generates the final response and returns the result directly as a JSON string.
func GenerateResponse(ctx context.Context, query string, contextStr string, userContext map[string]any, model string, respOpts ...RequestResponseOption) (string, error) {
	res := generateResponse(ctx, query, contextStr, userContext, model, respOpts...)
	return res.ToJSON()
}

// generateResponse handles internal response generation logic
func generateResponse(ctx context.Context, query string, contextStr string, userContext map[string]any, model string, respOpts ...RequestResponseOption) *ResponseResult {
	if model == "" {
		model = GeneratorModel
	}

	var customPrompt string
	if len(respOpts) > 0 {
		rp := NewResponsePrompt(respOpts...)
		if rp.Prompt != "" {
			customPrompt = rp.Prompt
		}
	}
	if customPrompt == "" && userContext != nil {
		if rp, ok := userContext["response_prompt"].(ResponsePrompt); ok {
			customPrompt = rp.Prompt
		} else if rpPtr, ok := userContext["response_prompt"].(*ResponsePrompt); ok && rpPtr != nil {
			customPrompt = rpPtr.Prompt
		} else if pStr, ok := userContext["response_prompt"].(string); ok {
			customPrompt = pStr
		}
	}

	baseSystemPrompt := `You are an AI Assistant.
Analyze the context data and answer the user's question accurately, concisely, and factually based on the provided context.

Guidelines:
1. Factual Accuracy: If a detail isn't in the context, do not mention it, invent it, or assume it.
2. Tone: Answer in plain, clear, professional, and concise sentences.
3. Missing Data Values: When presenting records with missing or empty values, display them as "-".`

	systemPrompt := baseSystemPrompt
	if customPrompt != "" {
		systemPrompt = customPrompt
	}

	cleanedContext := cleanContextPayload(contextStr)

	var userPromptBuilder strings.Builder
	if userContext != nil {
		if hist, ok := userContext["history"].([]ChatMessage); ok && len(hist) > 0 {
			userPromptBuilder.WriteString(fmt.Sprintf("=== PREVIOUS CONVERSATION HISTORY ===\n%s\n\n", FormatChatHistoryForLlm(hist)))
		}
		if attachText, ok := userContext["attachment_text"].(string); ok && attachText != "" {
			attachName := "Attached Document"
			if name, ok := userContext["attachment_name"].(string); ok && name != "" {
				attachName = name
			}
			userPromptBuilder.WriteString(fmt.Sprintf("=== ATTACHED DOCUMENT CONTEXT (%s) ===\n%s\n\n", attachName, attachText))
		}
	}
	userPromptBuilder.WriteString(fmt.Sprintf("=== CONTEXT DATA ===\n%s\n\n=== QUESTION ===\n%s", cleanedContext, query))

	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPromptBuilder.String()},
	}

	res, err := ChatGenerate(ctx, messages, nil, 32768, model, 3)
	if err != nil {
		log.Printf("[generateResponse] Failed to generate response: %v", err)
		return &ResponseResult{Answer: "I do not have information or unable to do that."}
	}
	log.Printf("[generateResponse] tokens -> input: %d, output: %d, total: %d", res.InputTokens, res.OutputTokens, res.InputTokens+res.OutputTokens)
	finalAns := SanitizeModelReply(res.RawOutput)
	if finalAns == "" {
		return &ResponseResult{Answer: "I do not have information or unable to do that."}
	}
	return &ResponseResult{Answer: finalAns}
}
