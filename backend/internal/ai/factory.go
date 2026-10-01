package ai

import (
	"fmt"

	"github.com/myahse/memory/backend/internal/config"
)

// FromConfig builds the AI service from configuration. Each capability can
// come from a different provider.
func FromConfig(c *config.Config) (*Service, error) {
	var openai *OpenAI
	getOpenAI := func() (*OpenAI, error) {
		if c.OpenAIAPIKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY is required for the openai provider")
		}
		if openai == nil {
			openai = NewOpenAI(c.OpenAIBaseURL, c.OpenAIAPIKey, c.OpenAIChatModel, c.OpenAISTTModel, c.OpenAIEmbedModel)
		}
		return openai, nil
	}
	var claude *Anthropic
	getAnthropic := func() (*Anthropic, error) {
		if c.AnthropicAPIKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY is required for the anthropic provider")
		}
		if claude == nil {
			claude = NewAnthropic(c.AnthropicAPIKey, c.AnthropicModel)
		}
		return claude, nil
	}

	llm := func(name string) (LLM, error) {
		switch name {
		case "anthropic":
			return getAnthropic()
		case "openai":
			return getOpenAI()
		case "mock":
			return Mock{}, nil
		}
		return nil, fmt.Errorf("unknown LLM provider %q", name)
	}

	s := &Service{Timeout: c.AITimeout}
	var err error
	if s.LLM, err = llm(c.LLMProvider); err != nil {
		return nil, fmt.Errorf("AI_LLM_PROVIDER: %w", err)
	}
	if s.Vision, err = llm(c.VisionProvider); err != nil {
		return nil, fmt.Errorf("AI_VISION_PROVIDER: %w", err)
	}
	switch c.SpeechProvider {
	case "openai":
		if s.Speech, err = getOpenAI(); err != nil {
			return nil, fmt.Errorf("AI_SPEECH_PROVIDER: %w", err)
		}
	case "mock":
		s.Speech = Mock{}
	default:
		return nil, fmt.Errorf("AI_SPEECH_PROVIDER: unknown provider %q", c.SpeechProvider)
	}
	switch c.EmbeddingProvider {
	case "openai":
		if s.Embedder, err = getOpenAI(); err != nil {
			return nil, fmt.Errorf("AI_EMBEDDING_PROVIDER: %w", err)
		}
	case "mock":
		s.Embedder = Mock{}
	default:
		return nil, fmt.Errorf("AI_EMBEDDING_PROVIDER: unknown provider %q", c.EmbeddingProvider)
	}
	return s, nil
}
