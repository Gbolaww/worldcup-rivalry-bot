package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const geminiEndpoint = "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.1-flash-lite:generateContent"

type GeminiClient struct {
	APIKey string
	http   *http.Client
}

func NewGeminiClient(apiKey string) *GeminiClient {
	return &GeminiClient{APIKey: apiKey, http: &http.Client{}}
}

type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
}

// GenerateHypeLine asks Gemini for a short, punchy hype/trash-talk line
// aimed at the rival team, celebrating a goal for the user's team.
func (c *GeminiClient) GenerateHypeLine(team, rival string) (string, error) {
	prompt := fmt.Sprintf(
		"You're a hype-man commentator for a World Cup fan. %s just scored against their rival %s. "+
			"Write ONE short, punchy, playful trash-talk / hype line (max 20 words) celebrating the goal "+
			"and ribbing the rival. Keep it fun and lighthearted, not genuinely mean or offensive. "+
			"Return ONLY the line, no quotes, no extra text.",
		team, rival,
	)

	reqBody := geminiRequest{
		Contents: []geminiContent{
			{Parts: []geminiPart{{Text: prompt}}},
		},
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal gemini request: %w", err)
	}

	url := fmt.Sprintf("%s?key=%s", geminiEndpoint, c.APIKey)
	resp, err := c.http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("gemini request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read gemini response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var gr geminiResponse
	if err := json.Unmarshal(respBytes, &gr); err != nil {
		return "", fmt.Errorf("unmarshal gemini response: %w", err)
	}

	if len(gr.Candidates) == 0 || len(gr.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini returned no candidates")
	}

	return gr.Candidates[0].Content.Parts[0].Text, nil
}

// GenerateComebackLine asks Gemini for a short imagined clapback from the
// rival's fans, responding to the hype line that was just sent after the
// user's team scored against them. Same request/response plumbing as
// GenerateHypeLine, just a different prompt.
func (c *GeminiClient) GenerateComebackLine(team, rival, hypeLine string) (string, error) {
	prompt := fmt.Sprintf(
		"You're a hype-man commentator for a World Cup fan base. %s just scored against %s, and %s fans "+
			"just heard this trash talk: \"%s\". Write ONE short, punchy, playful imagined clapback (max 20 words) "+
			"from %s fans — defiant and confident despite conceding the goal. Keep it fun and lighthearted, not "+
			"genuinely mean or offensive. Return ONLY the line, no quotes, no extra text.",
		team, rival, rival, hypeLine, rival,
	)

	reqBody := geminiRequest{
		Contents: []geminiContent{
			{Parts: []geminiPart{{Text: prompt}}},
		},
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal gemini request: %w", err)
	}

	url := fmt.Sprintf("%s?key=%s", geminiEndpoint, c.APIKey)
	resp, err := c.http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("gemini request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read gemini response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var gr geminiResponse
	if err := json.Unmarshal(respBytes, &gr); err != nil {
		return "", fmt.Errorf("unmarshal gemini response: %w", err)
	}

	if len(gr.Candidates) == 0 || len(gr.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini returned no candidates")
	}

	return gr.Candidates[0].Content.Parts[0].Text, nil
}
