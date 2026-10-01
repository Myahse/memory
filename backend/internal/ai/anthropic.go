package ai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Anthropic implements LLM (text, images and PDFs) with the Claude API.
type Anthropic struct {
	client anthropic.Client
	model  string
}

func NewAnthropic(apiKey, model string) *Anthropic {
	opts := []option.RequestOption{option.WithMaxRetries(2)}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	return &Anthropic{client: anthropic.NewClient(opts...), model: model}
}

func (a *Anthropic) Name() string { return "anthropic:" + a.model }

func (a *Anthropic) Supports(mime string) bool {
	return isImage(mime) || mime == "application/pdf"
}

func (a *Anthropic) Complete(ctx context.Context, req CompletionRequest) (string, error) {
	if len(req.Messages) == 0 {
		return "", errors.New("anthropic: no messages")
	}
	msgs := make([]anthropic.MessageParam, 0, len(req.Messages))
	for i, m := range req.Messages {
		var blocks []anthropic.ContentBlockParamUnion
		last := i == len(req.Messages)-1
		if last {
			for _, att := range req.Attachments {
				b64 := base64.StdEncoding.EncodeToString(att.Data)
				switch {
				case isImage(att.MIME):
					blocks = append(blocks, anthropic.NewImageBlockBase64(att.MIME, b64))
				case att.MIME == "application/pdf":
					blocks = append(blocks, anthropic.NewDocumentBlock(anthropic.Base64PDFSourceParam{Data: b64}))
				default:
					return "", fmt.Errorf("anthropic: unsupported attachment type %s", att.MIME)
				}
			}
		}
		blocks = append(blocks, anthropic.NewTextBlock(m.Text))
		if m.Role == "assistant" {
			msgs = append(msgs, anthropic.NewAssistantMessage(blocks...))
		} else {
			msgs = append(msgs, anthropic.NewUserMessage(blocks...))
		}
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: int64(maxTokens),
		Messages:  msgs,
	}
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}
	if req.Effort != "" {
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(req.Effort)}
	}

	// Server-side refusal fallback: if the primary model declines, the API
	// re-runs the request on a suitable fallback model in the same call.
	resp, err := a.client.Messages.New(ctx, params,
		option.WithHeaderAdd("anthropic-beta", "server-side-fallback-2026-07-01"),
		option.WithJSONSet("fallbacks", "default"),
	)
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			return "", &StatusError{Provider: "anthropic", Status: apiErr.StatusCode, Body: apiErr.Error()}
		}
		return "", err
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return "", &Error{Stage: "", Retryable: false, Err: errors.New("the AI model declined to process this content")}
	}
	var out strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			out.WriteString(t.Text)
		}
	}
	if resp.StopReason == anthropic.StopReasonMaxTokens && out.Len() == 0 {
		return "", &Error{Retryable: true, Err: errors.New("model output was truncated")}
	}
	return out.String(), nil
}
