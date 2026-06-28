package jsonc_test

import (
	"testing"

	"github.com/pycabbage/conduit/internal/jsonc"
)

func TestToJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "no comments",
			in:   `[{"id":"bot"}]`,
			want: `[{"id":"bot"}]`,
		},
		{
			name: "line comment",
			in:   "{\n// comment\n\"id\":\"bot\"\n}",
			want: "{\n\n\"id\":\"bot\"\n}",
		},
		{
			name: "inline line comment",
			in:   `{"intents":513 // GUILDS + GUILD_MESSAGES` + "\n}",
			want: "{\"intents\":513 \n}",
		},
		{
			name: "block comment",
			in:   `{"id":/* ignored */"bot"}`,
			want: `{"id":"bot"}`,
		},
		{
			name: "url in string is preserved",
			in:   `{"url":"https://example.com"}`,
			want: `{"url":"https://example.com"}`,
		},
		{
			name: "escaped quote in string",
			in:   `{"msg":"say \"hi\" // not a comment"}`,
			want: `{"msg":"say \"hi\" // not a comment"}`,
		},
		{
			name: "escaped backslash then quote",
			in:   `{"path":"C:\\\\"}`,
			want: `{"path":"C:\\\\"}`,
		},
		{
			name: "leading comments",
			in:   "// header\n[{\"id\":\"bot\"}]",
			want: "\n[{\"id\":\"bot\"}]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(jsonc.ToJSON([]byte(tt.in)))
			if got != tt.want {
				t.Errorf("ToJSON(%q)\n got  %q\n want %q", tt.in, got, tt.want)
			}
		})
	}
}
