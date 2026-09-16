package goaipackage // Gunakan nama package (bukan main) jika ingin dijadikan plugin/library

import (
	"encoding/json"
	"strings"
)

type UserAuth struct {
	Role      string `json:"role"`
	UserID    string `json:"user_id"`
	CompanyID string `json:"company_id"`
	Name      string `json:"name"`
}

// ToUserAuth converts a map, struct, or nested auth object into a normalized UserAuth struct.
// It flexibly handles common field aliases (role/Role, user_id/UserID, company_id/company/tenant_id, name/username)
// and extracts nested auth objects if present, eliminating redundant key injections.
func ToUserAuth(v any, defaultRole ...string) UserAuth {
	switch val := v.(type) {
	case UserAuth:
		if val.Role == "" && len(defaultRole) > 0 {
			val.Role = defaultRole[0]
		}
		return val
	case *UserAuth:
		if val != nil {
			res := *val
			if res.Role == "" && len(defaultRole) > 0 {
				res.Role = defaultRole[0]
			}
			return res
		}
	case map[string]any:
		// 1. If nested "auth", "credentials", or "user_auth" exists, extract from it first
		for _, key := range []string{"auth", "credentials", "user_auth", "UserAuth"} {
			if nested, ok := val[key]; ok && nested != nil {
				nestedAuth := ToUserAuth(nested, defaultRole...)
				if nestedAuth != (UserAuth{}) {
					return nestedAuth
				}
			}
		}

		auth := UserAuth{}

		// 2. Role
		for _, key := range []string{"role", "Role", "user_role"} {
			if r, ok := val[key].(string); ok && strings.TrimSpace(r) != "" {
				auth.Role = strings.TrimSpace(r)
				break
			}
		}
		if auth.Role == "" && len(defaultRole) > 0 {
			auth.Role = defaultRole[0]
		}

		// 3. UserID
		for _, key := range []string{"user_id", "UserID", "userId", "userid"} {
			if uid, ok := val[key].(string); ok && strings.TrimSpace(uid) != "" {
				auth.UserID = strings.TrimSpace(uid)
				break
			}
		}

		// 4. CompanyID
		for _, key := range []string{"company_id", "CompanyID", "companyId", "company", "tenant_id", "TenantID", "tenantId"} {
			if comp, ok := val[key].(string); ok && strings.TrimSpace(comp) != "" {
				auth.CompanyID = strings.TrimSpace(comp)
				break
			}
		}

		// 5. Name / Username
		for _, key := range []string{"username", "Username", "name", "Name", "user_name"} {
			if name, ok := val[key].(string); ok && strings.TrimSpace(name) != "" {
				auth.Name = strings.TrimSpace(name)
				break
			}
		}

		return auth
	}
	auth := UserAuth{}
	if len(defaultRole) > 0 {
		auth.Role = defaultRole[0]
	}
	return auth
}

type RequestChat struct {
	Auth       UserAuth `json:"auth"` // Disimpan agar RBAC & Company scoping bisa dibaca engine
	CacheID    string   `json:"cache_id"`
	Chat       string   `json:"chat"`                  // Pesan / query dari user
	Model      string   `json:"model,omitempty"`       // Model override (opsional)
	Attach     bool     `json:"attach"`                // Status ada/tidaknya lampiran
	LinkAttach string   `json:"link_attach,omitempty"` // Path lokal / URL file lampiran
	API_KEY    string   `json:"api_key"`
	LLM_URl    string   `json:"llm_url"`
}

type RequestChatOption func(*RequestChat)

// Opsi untuk custom CacheID / Session ID
func WithCacheID(id string) RequestChatOption {
	return func(r *RequestChat) {
		if id != "" {
			r.CacheID = id
		}
	}
}

// Opsi untuk menyertakan lampiran file
func WithAttachment(filePath string) RequestChatOption {
	return func(r *RequestChat) {
		if filePath != "" && filePath != "-" {
			r.Attach = true
			r.LinkAttach = filePath
		}
	}
}

// Opsi untuk override model LLM
func WithModel(model string, apiKey string, llmUrl string) RequestChatOption {
	return func(r *RequestChat) {
		if model != "" {
			r.Model = model
		}
		if apiKey != "" {
			r.API_KEY = apiKey
		}
		if llmUrl != "" {
			r.LLM_URl = llmUrl
		}
	}
}

// Constructor utama
func NewRequestChat(auth UserAuth, chat string, opts ...RequestChatOption) RequestChat {
	req := RequestChat{
		Auth:    auth,
		CacheID: auth.UserID, // Default: ID user
		Chat:    chat,
		Attach:  false,
	}

	for _, opt := range opts {
		opt(&req)
	}

	return req
}

type AttachmentInfo struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

// IntentResult represents the output of SearchIntent.
type IntentResult struct {
	Tools      []string `json:"tools"`
	Reason     string   `json:"reason"`
	TargetTool string   `json:"target_tool,omitempty"`
	Category   string   `json:"category,omitempty"`
	IsMutation bool     `json:"is_mutation,omitempty"`
	IsDelete   bool     `json:"is_delete,omitempty"`
	IsUpdate   bool     `json:"is_update,omitempty"`
	IsReport   bool     `json:"is_report,omitempty"`
	IsOffTopic  bool     `json:"is_off_topic,omitempty"`
	IsPermitted bool     `json:"is_permitted"`
}

func (r *IntentResult) ToJSON() (string, error) {
	if r == nil {
		return "{}", nil
	}
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *IntentResult) ToJSONIndent() (string, error) {
	if r == nil {
		return "{}", nil
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *IntentResult) String() string {
	s, _ := r.ToJSON()
	return s
}

// AgentResult represents the output of CallTools.
type AgentResult struct {
	Context     string                `json:"context"`
	Attachment  *AttachmentInfo       `json:"attachment,omitempty"`
	ToolResults []ToolExecutionResult `json:"tool_results,omitempty"`
}

func (r *AgentResult) ToJSON() (string, error) {
	if r == nil {
		return "{}", nil
	}
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *AgentResult) ToJSONIndent() (string, error) {
	if r == nil {
		return "{}", nil
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *AgentResult) String() string {
	s, _ := r.ToJSON()
	return s
}

// ResponseResult represents the output of GenerateResponse.
type ResponseResult struct {
	Answer string `json:"answer"`
}

func (r *ResponseResult) ToJSON() (string, error) {
	if r == nil {
		return "{}", nil
	}
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *ResponseResult) ToJSONIndent() (string, error) {
	if r == nil {
		return "{}", nil
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *ResponseResult) String() string {
	s, _ := r.ToJSON()
	return s
}

type ChatMessage struct {
	Role string `json:"role"`
	Chat string `json:"chat"`
}

// FormatChatHistoryForLlm formats chat history messages into a dialog block for the LLM
func FormatChatHistoryForLlm(history []ChatMessage) string {
	if len(history) == 0 {
		return ""
	}
	var lines []string
	for _, msg := range history {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "User"
		}
		lines = append(lines, role+": "+msg.Chat)
	}
	return strings.Join(lines, "\n")
}

type ResponsePrompt struct {
	Prompt string `json:"prompt"`
}

type ToolsPrompt struct {
	Prompt string   `json:"prompt"`
	Tools  []string `json:"tools,omitempty"`
}

type IntentPrompt struct {
	Prompt string `json:"prompt"`
	Text   string `json:"text,omitempty"`
}

type RequestResponseOption func(*ResponsePrompt)
type RequestToolsOption func(*ToolsPrompt)
type RequestIntentOption func(*IntentPrompt)

// WithResponsePrompt sets custom response generation instructions
func WithResponsePrompt(prompt string) RequestResponseOption {
	return func(r *ResponsePrompt) {
		if prompt != "" {
			r.Prompt = prompt
		}
	}
}

// NewResponsePrompt constructs a ResponsePrompt with options
func NewResponsePrompt(opts ...RequestResponseOption) ResponsePrompt {
	r := ResponsePrompt{}
	for _, opt := range opts {
		opt(&r)
	}
	return r
}

// WithToolsPrompt sets custom tool calling prompt / guardrails
func WithToolsPrompt(prompt string) RequestToolsOption {
	return func(r *ToolsPrompt) {
		if prompt != "" {
			r.Prompt = prompt
		}
	}
}

// WithToolsList sets specific allowed tools list
func WithToolsList(tools []string) RequestToolsOption {
	return func(r *ToolsPrompt) {
		if len(tools) > 0 {
			r.Tools = tools
		}
	}
}

// NewToolsPrompt constructs a ToolsPrompt with options
func NewToolsPrompt(opts ...RequestToolsOption) ToolsPrompt {
	tp := ToolsPrompt{}
	for _, opt := range opts {
		opt(&tp)
	}
	return tp
}

// WithIntentPrompt sets custom intent prompt
func WithIntentPrompt(prompt string) RequestIntentOption {
	return func(i *IntentPrompt) {
		if prompt != "" {
			i.Prompt = prompt
		}
	}
}

// NewIntentPrompt constructs an IntentPrompt with options
func NewIntentPrompt(opts ...RequestIntentOption) IntentPrompt {
	ip := IntentPrompt{}
	for _, opt := range opts {
		opt(&ip)
	}
	return ip
}

type RBACRule struct {
	Rules map[string][]string `json:"rules"` // role -> list of allowed tools
}

type RequestRBACOption func(*RBACRule)

// WithRBACMap sets rules from a map[string][]string
func WithRBACMap(rules map[string][]string) RequestRBACOption {
	return func(r *RBACRule) {
		if r.Rules == nil {
			r.Rules = make(map[string][]string)
		}
		for role, tools := range rules {
			roleKey := strings.ToLower(strings.TrimSpace(role))
			r.Rules[roleKey] = tools
		}
	}
}

// NewRBACRule builds an RBACRule from user options
func NewRBACRule(opts ...RequestRBACOption) RBACRule {
	rbac := RBACRule{
		Rules: make(map[string][]string),
	}
	for _, opt := range opts {
		opt(&rbac)
	}
	return rbac
}

type MCPLink struct {
	Link string `json:"link"`
}

type RequestMCPOption func(*MCPLink)

// WithMCPLink sets the server URL / endpoint in MCPLink
func WithMCPLink(link string) RequestMCPOption {
	return func(m *MCPLink) {
		if link != "" {
			m.Link = link
		}
	}
}

// NewMCPLink creates an MCPLink with user options
func NewMCPLink(opts ...RequestMCPOption) MCPLink {
	m := MCPLink{}
	for _, opt := range opts {
		opt(&m)
	}
	return m
}
