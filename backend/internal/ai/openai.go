package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// OpenAI implements LLM, Speech and Embedder against any OpenAI-compatible API.
type OpenAI struct {
	baseURL    string
	apiKey     string
	chatModel  string
	sttModel   string
	embedModel string
	http       *http.Client
}

func NewOpenAI(baseURL, apiKey, chatModel, sttModel, embedModel string) *OpenAI {
	return &OpenAI{
		baseURL: baseURL, apiKey: apiKey,
		chatModel: chatModel, sttModel: sttModel, embedModel: embedModel,
		http: &http.Client{Timeout: 5 * time.Minute},
	}
}

func (o *OpenAI) Name() string { return "openai:" + o.chatModel }

func (o *OpenAI) Supports(mime string) bool { return isImage(mime) || mime == "application/pdf" }

func (o *OpenAI) post(ctx context.Context, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("Content-Type", contentType)
	resp, err := o.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return &StatusError{Provider: "openai", Status: resp.StatusCode, Body: string(data)}
	}
	return json.Unmarshal(data, out)
}

func (o *OpenAI) Complete(ctx context.Context, req CompletionRequest) (string, error) {
	type part = map[string]any
	var msgs []map[string]any
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for i, m := range req.Messages {
		if i == len(req.Messages)-1 && len(req.Attachments) > 0 {
			var parts []part
			for _, att := range req.Attachments {
				uri := "data:" + att.MIME + ";base64," + base64.StdEncoding.EncodeToString(att.Data)
				if isImage(att.MIME) {
					parts = append(parts, part{"type": "image_url", "image_url": part{"url": uri, "detail": "high"}})
				} else if att.MIME == "application/pdf" {
					parts = append(parts, part{"type": "file", "file": part{"filename": "document.pdf", "file_data": uri}})
				} else {
					return "", fmt.Errorf("openai: unsupported attachment type %s", att.MIME)
				}
			}
			parts = append(parts, part{"type": "text", "text": m.Text})
			msgs = append(msgs, map[string]any{"role": m.Role, "content": parts})
			continue
		}
		msgs = append(msgs, map[string]any{"role": m.Role, "content": m.Text})
	}
	body, _ := json.Marshal(map[string]any{
		"model":                 o.chatModel,
		"messages":              msgs,
		"max_completion_tokens": max(req.MaxTokens, 256),
	})
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
				Refusal string `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := o.post(ctx, "/chat/completions", "application/json", bytes.NewReader(body), &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", &Error{Retryable: true, Err: fmt.Errorf("openai: empty response")}
	}
	if out.Choices[0].Message.Refusal != "" {
		return "", &Error{Err: fmt.Errorf("the AI model declined to process this content")}
	}
	return out.Choices[0].Message.Content, nil
}

func (o *OpenAI) Transcribe(ctx context.Context, audio []byte, mime string) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("model", o.sttModel)
	_ = w.WriteField("response_format", "json")
	fw, err := w.CreateFormFile("file", "audio"+audioExt(mime))
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(audio); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := o.post(ctx, "/audio/transcriptions", w.FormDataContentType(), &buf, &out); err != nil {
		return "", err
	}
	return out.Text, nil
}

func (o *OpenAI) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": o.embedModel, "input": texts, "dimensions": Dimensions})
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := o.post(ctx, "/embeddings", "application/json", bytes.NewReader(body), &out); err != nil {
		return nil, err
	}
	vecs := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index >= 0 && d.Index < len(vecs) {
			vecs[d.Index] = d.Embedding
		}
	}
	return vecs, nil
}

func audioExt(mime string) string {
	switch mime {
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return ".wav"
	case "audio/webm":
		return ".webm"
	case "audio/ogg":
		return ".ogg"
	case "audio/flac":
		return ".flac"
	default: // audio/mp4, audio/m4a, audio/aac, audio/x-m4a
		return ".m4a"
	}
}
