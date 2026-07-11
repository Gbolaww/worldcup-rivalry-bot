package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const elevenLabsEndpoint = "https://api.elevenlabs.io/v1/text-to-speech/%s"

type ElevenLabsClient struct {
	APIKey  string
	VoiceID string
	http    *http.Client
}

func NewElevenLabsClient(apiKey, voiceID string) *ElevenLabsClient {
	return &ElevenLabsClient{APIKey: apiKey, VoiceID: voiceID, http: &http.Client{}}
}

type ttsRequest struct {
	Text          string        `json:"text"`
	ModelID       string        `json:"model_id"`
	VoiceSettings voiceSettings `json:"voice_settings"`
}

type voiceSettings struct {
	Stability       float64 `json:"stability"`
	SimilarityBoost float64 `json:"similarity_boost"`
}

// GenerateSpeech converts text to speech and returns the raw MP3 audio bytes.
func (c *ElevenLabsClient) GenerateSpeech(text string) ([]byte, error) {
	reqBody := ttsRequest{
		Text:    text,
		ModelID: "eleven_flash_v2_5",
		VoiceSettings: voiceSettings{
			Stability:       0.4,
			SimilarityBoost: 0.75,
		},
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal elevenlabs request: %w", err)
	}

	url := fmt.Sprintf(elevenLabsEndpoint, c.VoiceID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("build elevenlabs request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("xi-api-key", c.APIKey)
	req.Header.Set("Accept", "audio/mpeg")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs request failed: %w", err)
	}
	defer resp.Body.Close()

	audioBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read elevenlabs response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("elevenlabs returned status %d: %s", resp.StatusCode, string(audioBytes))
	}

	return audioBytes, nil
}
