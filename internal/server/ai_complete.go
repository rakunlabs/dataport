package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

// Inline completion: while the user types, the editor asks for the text
// that goes at the cursor and shows it greyed out, to take with Tab. It is
// set up apart from the assistant, as it wants a small and fast model.
//
// Three ways to ask, by what the service offers:
//   - fim: POST /completions with prompt and suffix, the fill-in-the-middle
//     form of Ollama and DeepSeek (/beta);
//   - codestral: POST /fim/completions, as Mistral's Codestral takes it;
//   - chat: POST /chat/completions, for services with chat models only,
//     told to answer with the missing text alone.

const (
	aiCompleteSetting = "ai_completion"
	aiCompleteTimeout = 20 * time.Second
	aiCompleteTokens  = 128
	// The editor text sent around the cursor, and the schema lines.
	aiCompletePrefix = 6000
	aiCompleteSuffix = 2000
	aiCompleteTables = 60

	aiModeFIM       = "fim"
	aiModeCodestral = "codestral"
	aiModeChat      = "chat"
)

type aiCompleteConfig struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
	// Shared takes the assistant's API address and key; the model is
	// still this one.
	Shared  bool   `json:"shared"`
	BaseURL string `json:"base_url"`
	// Path is the endpoint after the address, such as /api/generate; empty
	// takes the one of the kind.
	Path   string `json:"path,omitempty"`
	Model  string `json:"model"`
	KeyEnc string `json:"key_enc,omitempty"`
}

func (s *Server) loadAIComplete(ctx context.Context) (aiCompleteConfig, error) {
	raw, err := s.store.GetSetting(ctx, aiCompleteSetting)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return aiCompleteConfig{Mode: aiModeFIM}, nil
		}

		return aiCompleteConfig{}, err
	}

	var v aiCompleteConfig
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return aiCompleteConfig{}, fmt.Errorf("decode ai completion settings; %w", err)
	}

	if v.Mode == "" {
		v.Mode = aiModeFIM
	}

	return v, nil
}

// aiRoute is where a completion request goes.
type aiRoute struct {
	Mode    string
	BaseURL string
	Path    string
	Model   string
	KeyEnc  string
	// Proxy is the assistant's proxy setting, which covers this service too.
	Proxy string
}

// aiCompleteRoute is the service inline completion uses, ok when it is on
// and set up.
func (s *Server) aiCompleteRoute(ctx context.Context) (aiRoute, bool, error) {
	v, err := s.loadAIComplete(ctx)
	if err != nil {
		return aiRoute{}, false, err
	}

	r := aiRoute{Mode: v.Mode, BaseURL: v.BaseURL, Path: v.Path, Model: v.Model, KeyEnc: v.KeyEnc}

	// The assistant's settings hold the shared address and the proxy; one
	// that cannot be read stops completion rather than go around the proxy.
	a, err := s.loadAI(ctx)
	if err != nil {
		return aiRoute{}, false, err
	}

	r.Proxy = a.Proxy

	if v.Shared {
		r.BaseURL, r.KeyEnc = a.BaseURL, a.KeyEnc
	}

	return r, r.BaseURL != "" && r.Model != "", nil
}

func (s *Server) aiCompleteReady(ctx context.Context) bool {
	v, err := s.loadAIComplete(ctx)
	if err != nil || !v.Enabled {
		return false
	}

	_, ok, err := s.aiCompleteRoute(ctx)

	return err == nil && ok
}

// ////////////////////////////////////////////
// admin settings

type aiCompleteView struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
	Shared  bool   `json:"shared"`
	BaseURL string `json:"base_url"`
	Path    string `json:"path"`
	Model   string `json:"model"`
	HasKey  bool   `json:"has_key"`
}

func (s *Server) adminGetAIComplete(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadAIComplete(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, aiCompleteView{Enabled: v.Enabled, Mode: v.Mode, Shared: v.Shared, BaseURL: v.BaseURL, Path: v.Path, Model: v.Model, HasKey: v.KeyEnc != ""})
}

func (s *Server) adminSetAIComplete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool   `json:"enabled"`
		Mode    string `json:"mode"`
		Shared  bool   `json:"shared"`
		BaseURL string `json:"base_url"`
		Path    string `json:"path"`
		Model   string `json:"model"`
		// Key nil keeps the saved key, "" removes it.
		Key *string `json:"key"`
	}

	if !readJSON(w, r, &in) {
		return
	}

	ctx := r.Context()

	current, err := s.loadAIComplete(ctx)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	v := aiCompleteConfig{Enabled: in.Enabled, Mode: strings.TrimSpace(in.Mode), Shared: in.Shared,
		BaseURL: strings.TrimRight(strings.TrimSpace(in.BaseURL), "/"), Path: strings.TrimSpace(in.Path), Model: strings.TrimSpace(in.Model), KeyEnc: current.KeyEnc}

	if v.Mode == "" {
		v.Mode = aiModeFIM
	}

	switch {
	case v.Mode != aiModeFIM && v.Mode != aiModeCodestral && v.Mode != aiModeChat:
		writeError(w, http.StatusBadRequest, "the completion kind must be fim, codestral or chat")

		return
	case len(v.Model) > 200:
		writeError(w, http.StatusBadRequest, "the model name is too long")

		return
	case !aiPathValid(v.Path):
		writeError(w, http.StatusBadRequest, "the endpoint path must start with / and be at most 200 characters, such as /api/generate")

		return
	case v.Enabled && v.Model == "":
		writeError(w, http.StatusBadRequest, "the model is required while inline completion is on")

		return
	}

	if !v.Shared {
		// The address is checked as the assistant's is.
		if msg := (aiConfig{BaseURL: v.BaseURL}).check(); msg != "" {
			writeError(w, http.StatusBadRequest, msg)

			return
		}

		if v.Enabled && v.BaseURL == "" {
			writeError(w, http.StatusBadRequest, "the API address is required while inline completion is on")

			return
		}
	} else if v.Enabled {
		a, err := s.loadAI(ctx)
		if err != nil {
			writeStoreError(w, err)

			return
		}

		if a.BaseURL == "" {
			writeError(w, http.StatusBadRequest, "set the assistant's API address first, or give completion one of its own")

			return
		}
	}

	if in.Key != nil {
		key := strings.TrimSpace(*in.Key)
		if len(key) > 4096 {
			writeError(w, http.StatusBadRequest, "the API key is too long")

			return
		}

		if v.KeyEnc, err = s.cipher.Encrypt(key); err != nil {
			writeError(w, http.StatusInternalServerError, "the API key cannot be saved")

			return
		}
	}

	raw, err := json.Marshal(v)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if err := s.store.SetSetting(ctx, aiCompleteSetting, string(raw)); err != nil {
		writeStoreError(w, err)

		return
	}

	s.adminGetAIComplete(w, r)
}

// adminTestAIComplete completes a sample statement with the saved settings.
func (s *Server) adminTestAIComplete(w http.ResponseWriter, r *http.Request) {
	route, ok, err := s.aiCompleteRoute(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if !ok {
		writeError(w, http.StatusBadRequest, "save the API address and the model first")

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), aiCompleteTimeout)
	defer cancel()

	start := time.Now()

	text, err := s.aiInfill(ctx, route, aiInfillInput{
		Prefix: "SELECT name, email\nFROM users\nWHERE ",
		DBType: "postgres",
		Tables: []string{"users(id integer, name text, email text, created_at timestamp, active boolean)"},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"text": text, "ms": time.Since(start).Milliseconds()})
}

// ////////////////////////////////////////////
// completing

type aiInfillInput struct {
	Prefix string   `json:"prefix"`
	Suffix string   `json:"suffix"`
	DBType string   `json:"db_type"`
	Tables []string `json:"tables"`
}

func (s *Server) aiInlineComplete(w http.ResponseWriter, r *http.Request) {
	var in aiInfillInput
	if !readJSON(w, r, &in) {
		return
	}

	ctx := r.Context()

	v, err := s.loadAIComplete(ctx)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	route, ok, err := s.aiCompleteRoute(ctx)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if !v.Enabled || !ok {
		writeError(w, http.StatusForbidden, "inline completion is not set up on this server")

		return
	}

	ctx, cancel := context.WithTimeout(ctx, aiCompleteTimeout)
	defer cancel()

	text, err := s.aiInfill(ctx, route, in)
	if err != nil {
		// The editor went on and dropped the request.
		if errors.Is(r.Context().Err(), context.Canceled) {
			return
		}

		writeError(w, http.StatusBadGateway, err.Error())

		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

// aiInfill asks the service for the text between prefix and suffix.
func (s *Server) aiInfill(ctx context.Context, route aiRoute, in aiInfillInput) (string, error) {
	key, err := s.cipher.Decrypt(route.KeyEnc)
	if err != nil {
		return "", errors.New("the saved API key cannot be read; enter it again in the settings")
	}

	prefix := clipStart(in.Prefix, aiCompletePrefix)
	suffix := clip(in.Suffix, aiCompleteSuffix)
	header := aiSchemaHeader(in.DBType, in.Tables)

	var (
		path string
		body map[string]any
	)

	switch route.Mode {
	case aiModeChat:
		path = "/chat/completions"
		body = map[string]any{
			"model":       route.Model,
			"temperature": 0,
			"max_tokens":  aiCompleteTokens * 2,
			"stream":      false,
			"messages": []map[string]string{
				{"role": "system", "content": aiCompleteSystem},
				{"role": "user", "content": header + "\n" + prefix + aiCursor + suffix},
			},
		}
	case aiModeCodestral:
		path = "/fim/completions"
		body = map[string]any{"model": route.Model, "prompt": header + prefix, "suffix": suffix, "max_tokens": aiCompleteTokens, "temperature": 0, "stream": false}
	default:
		path = "/completions"
		body = map[string]any{"model": route.Model, "prompt": header + prefix, "suffix": suffix, "max_tokens": aiCompleteTokens, "temperature": 0, "stream": false}
	}

	if route.Path != "" {
		path = route.Path
	}

	// Ollama's own /api/generate takes its limits as options and answers
	// with response.
	if p, _, _ := strings.Cut(path, "?"); strings.HasSuffix(p, "/api/generate") && route.Mode != aiModeChat {
		body = map[string]any{"model": route.Model, "prompt": header + prefix, "suffix": suffix, "stream": false, "raw": false,
			"options": map[string]any{"num_predict": aiCompleteTokens, "temperature": 0}}
	}

	endpoint, err := aiURL(route.BaseURL, path)
	if err != nil {
		return "", err
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")

	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	// Not tried again: the user has typed on by the time a retry answers.
	resp, err := aiDo(ctx, aiClient(route.Proxy), req, raw, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	// /completions answers with text, Codestral and chat with a message,
	// Ollama's /api/generate with response, llama.cpp's /completion and
	// /infill with content.
	var out struct {
		Choices []struct {
			Text    string `json:"text"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Response *string `json:"response"`
		Content  *string `json:"content"`
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", errors.New("the AI service sent no answer")
	}

	var text string

	switch {
	case len(out.Choices) > 0:
		text = out.Choices[0].Text
		if text == "" {
			text = out.Choices[0].Message.Content
		}
	case out.Response != nil:
		text = *out.Response
	case out.Content != nil:
		text = *out.Content
	default:
		return "", errors.New("the AI service sent no answer")
	}

	return aiCleanCompletion(text, prefix, suffix, route.Mode == aiModeChat), nil
}

const aiCursor = "<CURSOR>"

const aiCompleteSystem = "You are the SQL autocomplete of a database editor. The user's editor text follows, with " + aiCursor +
	" where the cursor is. Answer with only the text to insert at " + aiCursor + ": no explanation, no Markdown, no code fences, " +
	"and do not repeat the text before or after the cursor. Complete the current statement, or the current line when it is long; " +
	"use only the tables and columns given or already written. When nothing fits, answer with nothing."

// aiSchemaHeader tells the model the dialect and the tables the statement
// uses, as SQL comments put before the text.
func aiSchemaHeader(dbType string, tables []string) string {
	var b strings.Builder

	if dbType != "" {
		fmt.Fprintf(&b, "-- %s SQL\n", clip(oneLine(dbType), 40))
	}

	if len(tables) > aiCompleteTables {
		tables = tables[:aiCompleteTables]
	}

	if len(tables) > 0 {
		b.WriteString("-- tables:\n")

		for _, t := range tables {
			if t = oneLine(t); t != "" {
				fmt.Fprintf(&b, "-- %s\n", clip(t, 600))
			}
		}
	}

	if b.Len() > 0 {
		b.WriteString("\n")
	}

	return b.String()
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

var (
	aiThink = regexp.MustCompile(`(?s)<think>.*?(</think>|$)`)
	aiFence = regexp.MustCompile("(?s)```[a-zA-Z]*\\n?(.*?)(```|$)")
)

// aiCleanCompletion keeps what goes at the cursor: up to the first blank
// line, which ends the statement, without what the editor has after it.
// Chat models also wrap it in fences or start again from the line's start.
func aiCleanCompletion(text, prefix, suffix string, chat bool) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")

	if chat {
		text = aiThink.ReplaceAllString(text, "")
		text = strings.TrimLeft(text, "\n")

		if m := aiFence.FindStringSubmatch(text); m != nil {
			text = m[1]
		}

		text = strings.ReplaceAll(text, aiCursor, "")

		// The model wrote the line again from its start.
		line := prefix[strings.LastIndex(prefix, "\n")+1:]
		if trimmed := strings.TrimLeft(line, " \t"); trimmed != "" {
			switch {
			case strings.HasPrefix(text, line):
				text = text[len(line):]
			case strings.HasPrefix(text, trimmed):
				text = text[len(trimmed):]
			}
		}

		// A space the line already ends with is not doubled.
		if strings.HasSuffix(prefix, " ") {
			text = strings.TrimLeft(text, " ")
		}
	}

	if i := strings.Index(text, "\n\n"); i >= 0 {
		text = text[:i]
	}

	if lines := strings.Split(text, "\n"); len(lines) > 12 {
		text = strings.Join(lines[:12], "\n")
	}

	text = strings.TrimRight(text, " \t\n")

	// What the editor has after the cursor on its line, such as a closing
	// bracket, is not written twice.
	if next := strings.TrimSpace(strings.SplitN(suffix, "\n", 2)[0]); next != "" {
		text = strings.TrimRight(strings.TrimSuffix(text, next), " \t")
	}

	if strings.TrimSpace(text) == "" {
		return ""
	}

	return text
}

// clipStart keeps the last n bytes of s, on a rune boundary.
func clipStart(s string, n int) string {
	if len(s) <= n {
		return s
	}

	s = s[len(s)-n:]
	for len(s) > 0 && !utf8RuneStart(s[0]) {
		s = s[1:]
	}

	return s
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// aiURL puts the endpoint path after the address, keeping the address's
// own path (a proxy's base path such as /llm/v1) and joining the query of
// both, for services that want one, as Azure's api-version.
func aiURL(base, path string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", errors.New("the API address is not a valid URL")
	}

	p, query, _ := strings.Cut(path, "?")
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(p, "/")
	u.RawPath = ""

	if query != "" {
		q := u.Query()

		extra, err := url.ParseQuery(query)
		if err != nil {
			return "", errors.New("the endpoint path has an invalid query")
		}

		for k, vs := range extra {
			q[k] = vs
		}

		u.RawQuery = q.Encode()
	}

	return u.String(), nil
}

func aiPathValid(p string) bool {
	if p == "" {
		return true
	}

	if len(p) > 200 || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, " #\\") {
		return false
	}

	_, err := url.Parse(p)

	return err == nil
}
