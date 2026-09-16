package goaipackage

import (
	"testing"
)

func TestParseIntentJSON(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantTool   string
		wantOffTop bool
		wantErr    bool
	}{
		{
			name:       "Standard JSON",
			raw:        `{"tools": ["get_todos"], "reason": "fetch list"}`,
			wantTool:   "get_todos",
			wantOffTop: false,
			wantErr:    false,
		},
		{
			name:       "Markdown codeblock JSON",
			raw:        "```json\n{\"tools\": [\"search_tokopedia\"], \"reason\": \"search product\"}\n```",
			wantTool:   "search_tokopedia",
			wantOffTop: false,
			wantErr:    false,
		},
		{
			name:       "LFM Special Token Tool Call",
			raw:        "<|tool_call_start|>[get_todos()]<|tool_call_end|>",
			wantTool:   "get_todos",
			wantOffTop: false,
			wantErr:    false,
		},
		{
			name:       "Special Token with Arguments",
			raw:        "<|tool_call_start|>[get_todos(limit=5)]<|tool_call_end|>",
			wantTool:   "get_todos",
			wantOffTop: false,
			wantErr:    false,
		},
		{
			name:       "XML Style Tool Call",
			raw:        "<tool_call>get_todos()</tool_call>",
			wantTool:   "get_todos",
			wantOffTop: false,
			wantErr:    false,
		},
		{
			name:       "Embedded JSON in Tool Token",
			raw:        `<|tool_call_start|>{"name": "get_todos"}<|tool_call_end|>`,
			wantTool:   "get_todos",
			wantOffTop: false,
			wantErr:    false,
		},
		{
			name:       "Bracketed Function Format",
			raw:        "[get_todos()]",
			wantTool:   "get_todos",
			wantOffTop: false,
			wantErr:    false,
		},
		{
			name:       "Unrelated text without tool",
			raw:        "Halo, ada yang bisa saya bantu hari ini?",
			wantTool:   "",
			wantOffTop: false,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := parseIntentJSON(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseIntentJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if res == nil {
					t.Fatalf("expected non-nil result")
				}
				if res.TargetTool != tt.wantTool {
					t.Errorf("TargetTool = %q, want %q", res.TargetTool, tt.wantTool)
				}
				if res.IsOffTopic != tt.wantOffTop {
					t.Errorf("IsOffTopic = %v, want %v", res.IsOffTopic, tt.wantOffTop)
				}
			}
		})
	}
}
