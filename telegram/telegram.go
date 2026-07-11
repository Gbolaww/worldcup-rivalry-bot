package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

type Client struct {
	Token string
	http  *http.Client
}

func NewClient(token string) *Client {
	return &Client{
		Token: token,
		http:  &http.Client{Timeout: 65 * time.Second},
	}
}

func (c *Client) apiURL(method string) string {
	return fmt.Sprintf("https://api.telegram.org/bot%s/%s", c.Token, method)
}

// Update represents a single incoming Telegram update from getUpdates.
type Update struct {
	UpdateID int `json:"update_id"`
	Message  *struct {
		MessageID int `json:"message_id"`
		Chat      struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

type getUpdatesResponse struct {
	OK     bool     `json:"ok"`
	Result []Update `json:"result"`
}

// GetUpdates long-polls Telegram for new messages since offset.
func (c *Client) GetUpdates(offset int) ([]Update, error) {
	url := fmt.Sprintf("%s?offset=%d&timeout=60", c.apiURL("getUpdates"), offset)
	resp, err := c.http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("getUpdates request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read getUpdates response: %w", err)
	}

	var gur getUpdatesResponse
	if err := json.Unmarshal(body, &gur); err != nil {
		return nil, fmt.Errorf("unmarshal getUpdates response: %w", err)
	}
	if !gur.OK {
		return nil, fmt.Errorf("getUpdates returned not-ok: %s", string(body))
	}

	return gur.Result, nil
}

type sendMessageRequest struct {
	ChatID int64  `json:"chat_id"`
	Text   string `json:"text"`
}

// SendMessage sends a plain text message to the given chat.
func (c *Client) SendMessage(chatID int64, text string) error {
	reqBody := sendMessageRequest{ChatID: chatID, Text: text}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal sendMessage request: %w", err)
	}

	resp, err := c.http.Post(c.apiURL("sendMessage"), "application/json", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("sendMessage request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sendMessage returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	return nil
}

// SendVoice sends an audio clip (raw MP3/OGG bytes) as a voice note.
// Telegram's sendVoice expects OGG/Opus for the "voice note" UI treatment,
// but will generally accept MP3 uploads too (delivered as a playable audio file).
func (c *Client) SendVoice(chatID int64, audio []byte, caption string) error {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	if err := writer.WriteField("chat_id", fmt.Sprintf("%d", chatID)); err != nil {
		return fmt.Errorf("write chat_id field: %w", err)
	}
	if caption != "" {
		if err := writer.WriteField("caption", caption); err != nil {
			return fmt.Errorf("write caption field: %w", err)
		}
	}

	part, err := writer.CreateFormFile("voice", "hype.mp3")
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(audio); err != nil {
		return fmt.Errorf("write audio bytes: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.apiURL("sendVoice"), &buf)
	if err != nil {
		return fmt.Errorf("build sendVoice request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sendVoice request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sendVoice returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	return nil
}
