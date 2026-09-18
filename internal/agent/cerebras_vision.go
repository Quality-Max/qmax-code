package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/httpx"
	"github.com/qualitymax/qmax-code/internal/tui"
)

// Gemma 4 vision sidecar — reads image attachments for CLI-backend models
// that have no image recognition of their own (Z.AI GLM coding-plan models
// via opencode, or any backend in "always" mode), so the capable text model
// keeps the turn and the pixels arrive as a text description.
//
// This is deliberately a one-shot describe call, not a backend switch: the
// active model stays in charge of reasoning and tools, and Gemma 4 on
// Cerebras acts only as the eyes.

const visionSidecarTimeout = 2 * time.Minute

// ShouldUseVisionSidecar decides whether a CLI-backend turn with image
// attachments should run the sidecar. It is the single policy chokepoint:
//
//   - off           → never
//   - always        → every CLI backend (global image read by Cerebras)
//   - auto (unset)  → only when the active model is KNOWN text-only
//     (opencode on a Z.AI GLM coding-plan model)
//
// Every mode requires a Cerebras key; without one the sidecar silently
// degrades to off and callers fall back to the pre-sidecar behavior.
func ShouldUseVisionSidecar(cfg *api.Config, backend string) bool {
	if cfg == nil {
		return false
	}
	mode := cfg.VisionSidecarMode()
	if mode == api.VisionSidecarOff {
		return false
	}
	if cfg.CerebrasKey == "" {
		return false
	}
	if mode == api.VisionSidecarAlways {
		return true
	}
	if backend != "opencode" {
		return false
	}
	lacks, known := activeModelLacksVision(cfg)
	return known && lacks
}

// activeModelLacksVision resolves the opencode provider/model actually in
// play. ModelOverride ("provider/model") is authoritative; the implicit
// case — the Z.AI coding plan being the ONLY enabled provider — also means a
// GLM model is active even before an explicit picker choice. Anything else
// (multiple providers, known providers with live catalogues) is unknown.
func activeModelLacksVision(cfg *api.Config) (lacksVision, known bool) {
	override := strings.TrimSpace(cfg.ModelOverride)
	if i := strings.Index(override, "/"); i > 0 {
		return api.ProviderModelLacksVision(override[:i], override[i+1:])
	}
	if len(cfg.EnabledProviders) == 1 && cfg.EnabledProviders[0] == "zai-coding-plan" {
		return true, true
	}
	return false, false
}

const visionSidecarSystemPrompt = `You are the vision unit for a coding/QA agent that cannot see images.
Describe the attached image(s) so that agent can act on them accurately.
Rules:
- Use one section per image, headed exactly [n] <filename>, in attachment order.
- Transcribe ALL visible text verbatim: UI labels, button text, code, error
  messages, stack traces, URLs, terminal output. Preserve line breaks.
- Describe layout, element state, and colors only where they affect the request.
- Report only what is visible. If something is blurry or ambiguous, say so
  explicitly instead of guessing.
- No preamble, no closing remarks — start directly with [1].`

func sidecarDescribePrompt(userPrompt string, fileNames []string) string {
	promptContext := strings.TrimSpace(userPrompt)
	if promptContext == "" {
		promptContext = "Analyze these images."
	} else {
		runes := []rune(promptContext)
		if len(runes) > 1000 {
			promptContext = string(runes[:997]) + "..."
		}
	}
	var b strings.Builder
	b.WriteString("Describe the attached image(s) for the requesting agent.\n")
	fmt.Fprintf(&b, "Attachment filenames, in order: %s.\n", strings.Join(fileNames, ", "))
	fmt.Fprintf(&b, "The agent's task, for context (answer the description need it raises, do not perform the task): %s\n", promptContext)
	return b.String()
}

// DescribeImagesWithGemma makes one Cerebras call with gemma-4-31b (always
// the multimodal model, even when the user's active Cerebras model is a
// text-only one) and returns a per-image text description. All usable
// attachments go in a single request — one round trip, and cross-image
// context (e.g. before/after screenshots) stays available to the model.
func DescribeImagesWithGemma(ctx context.Context, cfg *api.Config, imgs []tui.ImageAttachment, userPrompt string) (string, error) {
	if cfg == nil || cfg.CerebrasKey == "" {
		return "", fmt.Errorf("no Cerebras API key configured")
	}
	if len(imgs) == 0 {
		return "", fmt.Errorf("no images to describe")
	}
	if len(imgs) > 10 {
		return "", fmt.Errorf("too many images (%d); vision sidecar supports up to 10", len(imgs))
	}

	base := cfg.CerebrasBaseURL
	if base == "" {
		base = api.CerebrasAPIBase
	}
	client := &CerebrasClient{
		BaseURL: strings.TrimRight(base, "/"),
		Model:   api.CerebrasGemma4Model,
		APIKey:  cfg.CerebrasKey,
		HTTP:    httpx.NewClient(visionSidecarTimeout),
	}

	ctx, cancel := context.WithTimeout(ctx, visionSidecarTimeout)
	defer cancel()

	parts := make([]oaiContentPart, 0, len(imgs)+1)
	names := make([]string, 0, len(imgs))
	for _, img := range imgs {
		if img.MediaType == "" || img.Data == "" {
			continue
		}
		parts = append(parts, oaiContentPart{
			Type:     "image_url",
			ImageURL: &oaiImageURL{URL: "data:" + img.MediaType + ";base64," + img.Data},
		})
		names = append(names, img.FileName)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("no usable image data in attachments")
	}
	parts = append(parts, oaiContentPart{
		Type: "text",
		Text: sidecarDescribePrompt(userPrompt, names),
	})

	resp, err := client.Chat(ctx, []oaiMessage{
		{Role: "system", Content: visionSidecarSystemPrompt},
		{Role: "user", Content: parts},
	}, nil)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("gemma-4 returned an empty description")
	}
	return resp.Choices[0].Message.Content, nil
}

// BuildSidecarAugmentedPrompt injects the sidecar description into the CLI
// turn's user prompt under explicit delimiters, so the text-only model knows
// exactly where the image content came from and what it covers.
func BuildSidecarAugmentedPrompt(userPrompt string, imgs []tui.ImageAttachment, description string) (string, error) {
	if len(imgs) == 0 {
		return "", fmt.Errorf("no images to augment the prompt with")
	}
	desc := strings.TrimSpace(description)
	if desc == "" {
		return "", fmt.Errorf("empty image description")
	}
	
	// Sanitize output to prevent indirect prompt injection breaking out of the tags.
	desc = strings.ReplaceAll(desc, "</image-descriptions>", `<\/image-descriptions>`)
	
	names := make([]string, len(imgs))
	for i, img := range imgs {
		names[i] = img.FileName
	}

	var b strings.Builder
	b.WriteString(strings.TrimSpace(userPrompt))
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "[gemma-4 vision sidecar read %d image(s): %s]\n", len(imgs), strings.Join(names, ", "))
	b.WriteString("<image-descriptions>\n")
	b.WriteString(desc)
	b.WriteString("\n</image-descriptions>")
	return b.String(), nil
}
