# Dokumentasi Teknis `go_ai_package`

Dokumentasi lengkap seluruh fungsi, tipe data, variabel, dan cara penggunaan library AI Agent Go modular.

---

## 1. Konfigurasi Global & LLM Client (`models.go`)

### `SetAIConfig(apiKey string, baseURL string)`
Mengatur kredensial LLM secara terprogram tanpa membutuhkan file `.env`.
- **Parameter**:
  - `apiKey`: Token/API key penyedia LLM (OpenRouter/OpenAI).
  - `baseURL`: Endpoint API LLM (contoh: `https://openrouter.ai/api/v1`).
- **Contoh**:
  ```go
  SetAIConfig("sk-or-v1-xxx", "https://openrouter.ai/api/v1")
  ```

### `SetAPIKey(apiKey string)` & `SetBaseURL(baseURL string)`
Mengatur API Key atau Base URL secara terpisah.
- **Parameter**: `apiKey` (string) / `baseURL` (string).

### `ChatGenerate(ctx, messages, tools, maxNewTokens, model, retries)`
Fungsi inti pemanggilan completion LLM dengan retry dan exponential backoff.
- **Parameter**:
  - `ctx`: `context.Context` untuk timeout/cancellation.
  - `messages`: Slice `[]Message` (Role: system/user/assistant, Content: string).
  - `tools`: Skema tool (opsional, `nil` jika tanpa tool calling native).
  - `maxNewTokens`: Batas token output (default: 4096).
  - `model`: Nama/alias model AI.
  - `retries`: Jumlah pengulangan jika gagal (rekomendasi: 2–3).
- **Return**: `(*ChatResult, error)`

### `SanitizeModelReply(raw string) string`
Membersihkan teks mentah LLM dari tag `<thought>`, `<think>`, token DSML, atau sisa payload JSON.
- **Parameter**: `raw` (string).
- **Return**: string bersih siap tampil ke user.

---

## 2. Tiga Agen Utama AI (`models.go`)

### Agen 1: `SearchIntent` & `SearchIntentJSON`
Menganalisis kebutuhan tool dan alasan berdasarkan prompt user.
- **Fungsi**:
  - `SearchIntent(ctx, query, userContext, model, intentOpts...) *IntentResult`
  - `SearchIntentJSON(ctx, query, userContext, model, intentOpts...) (string, error)`
- **Parameter**:
  - `ctx`: `context.Context`.
  - `query`: Pertanyaan/instruksi user.
  - `userContext`: Map konteks tambahan (`map[string]any`).
  - `model`: Model LLM (kosongkan `""` untuk default).
  - `intentOpts`: Opsi kustom prompt (misal: `WithIntentPrompt("...")`).
- **Return**: `*IntentResult` atau JSON `string` format `{"tools": [...], "reason": "..."}`.
- **Contoh**:
  ```go
  intent := SearchIntent(ctx, "Tampilkan daftar user aktif", nil, "")
  fmt.Println(intent.Tools, intent.Reason)
  ```

### Agen 2: `CallTools` & `CallToolsJSON`
Menjalankan reasoning loop multi-turn, pemanggilan tool MCP paralel, dan ekstraksi konteks data.
- **Fungsi**:
  - `CallTools(ctx, client, mcpTools, query, role, userContext, maxIterations, model, toolsOpts...) (*AgentResult, error)`
  - `CallToolsJSON(ctx, client, mcpTools, query, role, userContext, maxIterations, model, toolsOpts...) (string, error)`
- **Parameter**:
  - `ctx`: `context.Context`.
  - `client`: `*client.Client` koneksi MCP server.
  - `mcpTools`: Daftar tool MCP `[]mcp.Tool`.
  - `query`: Pertanyaan user.
  - `role`: Role pengguna untuk verifikasi RBAC (misal: `"admin"`, `"staff"`).
  - `userContext`: Map konteks sesi chat (`map[string]any`).
  - `maxIterations`: Batas loop perbaikan (minimal: 3).
  - `model`: Model LLM.
  - `toolsOpts`: Opsi guardrails kustom (misal: `WithToolsPrompt("...")`).
- **Return**: `*AgentResult` atau JSON `string` format `{"context": "...", "attachment": {...}, "tool_results": [...]}`.
- **Contoh**:
  ```go
  toolRes, err := CallTools(ctx, mcpClient, mcpTools, "Ambil data user", "admin", nil, 3, "",
      WithToolsPrompt("Jangan izinkan update tanpa ID."),
  )
  ```

### Agen 3: `GenerateResponse` & `GenerateResponseJSON`
Menyusun jawaban akhir manusiawi berdasarkan hasil konteks yang diekstrak.
- **Fungsi**:
  - `GenerateResponse(ctx, query, contextStr, userContext, model, respOpts...) *ResponseResult`
  - `GenerateResponseJSON(ctx, query, contextStr, userContext, model, respOpts...) (string, error)`
- **Parameter**:
  - `ctx`: `context.Context`.
  - `query`: Pertanyaan user.
  - `contextStr`: Data konteks hasil dari `CallTools` (`toolRes.Context`).
  - `userContext`: Map konteks tambahan.
  - `model`: Model LLM.
  - `respOpts`: Opsi kustom sistem respon (misal: `WithResponsePrompt("...")`).
- **Return**: `*ResponseResult` atau JSON `string` format `{"answer": "..."}`.
- **Contoh**:
  ```go
  finalResp := GenerateResponse(ctx, query, toolRes.Context, nil, "",
      WithResponsePrompt("Jawab secara ringkas dalam bahasa Indonesia."),
  )
  fmt.Println(finalResp.Answer)
  ```

---

## 3. Struktur Data & Opsi (`types.go`)

### Model Otentikasi & Konteks
- `UserAuth`: Struct otentikasi (`Role`, `UserID`, `CompanyID`, `Name`).
- `ToUserAuth(v any, defaultRole ...string) UserAuth`: Mengubah map atau objek auth ke struct `UserAuth`.
- `ChatMessage`: Struct pesan riwayat (`Role`, `Chat`).
- `FormatChatHistoryForLlm(history []ChatMessage) string`: Mengubah list riwayat pesan ke format dialog teks LLM.

### Output Structs
- `IntentResult`: Hasil intent (`Tools`, `Reason`, `TargetTool`, `Category`, `IsOffTopic`).
  - Method: `.ToJSON()`, `.ToJSONIndent()`, `.String()`.
- `AgentResult`: Hasil tool calling (`Context`, `Attachment`, `ToolResults`).
  - Method: `.ToJSON()`, `.ToJSONIndent()`, `.String()`.
- `ResponseResult`: Hasil jawaban akhir (`Answer`).
  - Method: `.ToJSON()`, `.ToJSONIndent()`, `.String()`.

### Custom Guardrails & Functional Options
- **Intent**: `WithIntentPrompt(prompt string)` / `NewIntentPrompt(opts...)`
- **Tools**: `WithToolsPrompt(prompt string)`, `WithToolsList(tools []string)` / `NewToolsPrompt(opts...)`
- **Response**: `WithResponsePrompt(prompt string)` / `NewResponsePrompt(opts...)`
- **RBAC**: `WithRBACMap(rules map[string][]string)` / `NewRBACRule(opts...)`
- **MCP Link**: `WithMCPLink(link string)` / `NewMCPLink(opts...)`

---

## 4. Koneksi MCP & Eksekusi Tool (`toolCall.go`)

### `SetRBACMap(rules map[string][]string)` / `SetRBACRules(rbac RBACRule)`
Mengatur daftar tool yang boleh diakses per role user.
- **Contoh**:
  ```go
  SetRBACMap(map[string][]string{
      "admin": {"get_users", "create_user", "delete_user"},
      "user":  {"get_users"},
  })
  ```

### `ConnectAndLoadKnownTools(ctx, mcpLink)`
Menghubungkan ke MCP server via SSE atau Stdio, mengambil tools, dan mendaftarkannya otomatis.
- **Parameter**:
  - `ctx`: `context.Context`.
  - `mcpLink`: `MCPLink` (URL SSE misal: `http://localhost:8080/sse` atau path skrip).
- **Return**: `(*client.Client, []mcp.Tool, error)`

### `FetchMCPTools(ctx, client)`
Mengambil daftar tool aktif dari MCP Server.
- **Parameter**: `ctx`, `*client.Client`.
- **Return**: `([]mcp.Tool, error)`.

### `ShrinkToolCatalog(tools []mcp.Tool) []mcp.Tool`
Memangkas deskripsi tool menggunakan CLI `caveman-shrink` atau algoritma native untuk menghemat token prompt.
- **Parameter**: `tools ([]mcp.Tool)`.
- **Return**: `[]mcp.Tool` terkompresi.

---

## 5. Database Riwayat Chat (`historyRepo.go`)

Menggunakan SQLite Pure-Go (`modernc.org/sqlite`) tanpa membutuhkan compiler C / GCC.

### `SetDBPath(path string)`
Mengatur lokasi file SQLite (default: `data/chat_history.db`).
- **Parameter**: `path` (string).

### `NewHistoryRepository()`
Membuat instance repository dan otomatis menginisialisasi tabel `chat_history`.
- **Return**: `(*HistoryRepository, error)`.

### `repo.LoadContext(req *RequestChat)`
Mengambil 10 pesan riwayat chat terakhir dan lampiran aktif berdasarkan `CacheID`.
- **Parameter**: `req (*RequestChat)`.
- **Return**: `(map[string]any, error)`.

### `repo.SaveTurn(cacheID, userID, role, message, attachBool, filePath, fileName, extractedText)`
Menyimpan satu giliran percakapan user/bot ke database SQLite.
- **Parameter**: String identitas, pesan, dan lampiran.
- **Return**: `error`.

### `CloseHistoryDB()`
Menutup koneksi SQLite bersama saat aplikasi shutdown.
- **Return**: `error`.

---

## 6. Ekstraksi Dokumen & OCR (`extract.go`)

### `ExtractText(content []byte, filename string) (string, error)`
Mendeteksi ekstensi file dan mengekstrak teks secara otomatis.
- **Format Didukung**: PDF (`.pdf`), Excel (`.xlsx`, `.xls`), CSV (`.csv`), Gambar (`.png`, `.jpg`, `.jpeg`, `.webp`).
- **Parameter**: `content` ([]byte), `filename` (string).
- **Return**: `(string, error)`.

### `ExtractExcelText(content []byte) (string, error)`
Mengekstrak seluruh sheet, header, dan baris dari file Excel menjadi teks tabular.

### `ExtractCSVText(content []byte) (string, error)`
Mengekstrak file CSV menjadi teks berstruktur baris & header.

### `ExtractImageOCR(content []byte, filename string) (string, error)`
Mengekstrak teks dari gambar menggunakan preprocessing High-Pass Filter dan OCR Tesseract.

---

## 7. Resolver Model AI (`resolve.go`)

### `ResolveModel(name string) string`
Mengonversi alias/nama pendek model menjadi ID model yang valid di katalog.
- **Parameter**: `name` (misal: `"deepseek"`, `"gpt"`, `"qwen"`).
- **Return**: string nama model lengkap.

---

## 8. Contoh Alur Lengkap Plug & Play

```go
package main

import (
	"context"
	"fmt"
	"log"
)

func main() {
	ctx := context.Background()

	// 1. Setup API & RBAC
	SetAIConfig("YOUR_API_KEY", "https://openrouter.ai/api/v1")
	SetRBACMap(map[string][]string{
		"admin": {"get_data", "update_data"},
	})

	// 2. Hubungkan ke MCP Tools Server
	client, tools, err := ConnectAndLoadKnownTools(ctx, NewMCPLink(WithMCPLink("http://localhost:8080/sse")))
	if err != nil {
		log.Fatalf("Gagal koneksi MCP: %v", err)
	}

	query := "Tampilkan data terbaru"
	role := "admin"

	// 3. Agen 1: Identifikasi Intent
	intent := SearchIntent(ctx, query, nil, "")
	fmt.Printf("[Intent] Tools: %v, Reason: %s\n", intent.Tools, intent.Reason)

	// 4. Agen 2: Jalankan Tool Calling & Ekstraksi Data
	agentRes, err := CallTools(ctx, client, tools, query, role, nil, 3, "")
	if err != nil {
		log.Fatalf("Gagal CallTools: %v", err)
	}

	// 5. Agen 3: Susun Jawaban Akhir
	response := GenerateResponse(ctx, query, agentRes.Context, nil, "",
		WithResponsePrompt("Jawab secara ringkas dan profesional."),
	)
	fmt.Printf("[Jawaban Akhir]:\n%s\n", response.Answer)
}
```
