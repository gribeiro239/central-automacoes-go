package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"central-automacoes/internal/domain"
)

const PreviewLimit = 4096

type HTTP struct{ client *http.Client }

func NewHTTP() *HTTP {
	return &HTTP{client: &http.Client{
		// Do not send configured credentials to a redirect destination or hide a 3xx.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (h *HTTP) Execute(ctx context.Context, a domain.Automation, runID string) domain.Result {
	req, err := http.NewRequestWithContext(ctx, a.Config.Method, a.Config.URL, strings.NewReader(a.Config.Body))
	if err != nil {
		return domain.Result{Error: "não foi possível montar a requisição"}
	}
	for k, v := range a.Config.Headers {
		req.Header.Set(k, v)
	}
	// Stable across all attempts, useful when the destination supports idempotency.
	req.Header.Set("Idempotency-Key", runID)
	resp, err := h.client.Do(req)
	if err != nil {
		message := "falha de transporte HTTP"
		if ctx.Err() != nil {
			message = ctx.Err().Error()
		}
		return domain.Result{Error: message, Retryable: true}
	}
	defer resp.Body.Close()
	preview, err := io.ReadAll(io.LimitReader(resp.Body, PreviewLimit+1))
	result := domain.Result{StatusCode: resp.StatusCode, Truncated: len(preview) > PreviewLimit}
	if result.Truncated {
		preview = preview[:PreviewLimit]
	}
	// JSONB cannot represent NUL. Normalize binary/invalid text before persistence,
	// then enforce the final byte bound without cutting a UTF-8 code point.
	result.BodyPreview = strings.ReplaceAll(strings.ToValidUTF8(string(preview), "\uFFFD"), "\x00", "\uFFFD")
	if len(result.BodyPreview) > PreviewLimit {
		result.Truncated = true
		result.BodyPreview = result.BodyPreview[:PreviewLimit]
		for !utf8.ValidString(result.BodyPreview) {
			result.BodyPreview = result.BodyPreview[:len(result.BodyPreview)-1]
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Error = fmt.Sprintf("destino retornou HTTP %d", resp.StatusCode)
		result.Retryable = resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500
	}
	if err != nil && result.Error == "" {
		result.Error = "falha na leitura da resposta"
		result.Retryable = true
	}
	return result
}
