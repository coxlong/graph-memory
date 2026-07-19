package gmem

import (
	"errors"
	"fmt"
	"net/url"

	"resty.dev/v3"
)

type Embedder struct {
	client *resty.Client
	model  string
}

func NewEmbedder(base, key, model string) *Embedder {
	return &Embedder{
		client: resty.New().
			SetBaseURL(base).
			SetHeader("Authorization", "Bearer "+key),
		model: model,
	}
}

type embedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed calls OpenAI-compatible POST {base}/embeddings
func (e *Embedder) Embed(text string) ([]float32, error) {
	var out embedResponse
	resp, err := e.client.R().
		SetBody(embedRequest{Model: e.model, Input: text}).
		SetResult(&out).
		Post("/embeddings")
	if err != nil {
		// strip the request URL (configured endpoint) from transport errors
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("embedding request: %w", ue.Err)
		}
		return nil, fmt.Errorf("embedding request: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return nil, fmt.Errorf("embedding API %d: %s", resp.StatusCode(), resp.String())
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embedding API returned no data")
	}
	return out.Data[0].Embedding, nil
}
