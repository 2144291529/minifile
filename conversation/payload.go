package conversation

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"minichat/util"
	"net/http"
	"strings"
)

type ChatPayload struct {
	Type      string `json:"type"`
	MessageID string `json:"message_id,omitempty"`
	Text      string `json:"text,omitempty"`
	DataURL   string `json:"data_url,omitempty"`
	SentAt    string `json:"sent_at,omitempty"`
}

type ValidatedPayload struct {
	Cmd       string
	MessageID string
	Payload   string
}

func ValidateIncomingPayload(raw []byte) (*ValidatedPayload, error) {
	if len(raw) == 0 || len(raw) > MaxPayloadBytes {
		return nil, errors.New("payload size is invalid")
	}

	var payload ChatPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		normalized, messageID, normErr := normalizeStructuredTextPayload(string(raw), "", "")
		if normErr != nil {
			return nil, normErr
		}
		return &ValidatedPayload{
			Cmd:       "chat",
			MessageID: messageID,
			Payload:   normalized,
		}, nil
	}

	switch payload.Type {
	case "text":
		normalized, messageID, err := normalizeStructuredTextPayload(payload.Text, payload.MessageID, payload.SentAt)
		if err != nil {
			return nil, err
		}
		return &ValidatedPayload{
			Cmd:       "chat",
			MessageID: messageID,
			Payload:   normalized,
		}, nil
	case "image":
		normalized, messageID, err := normalizeImagePayload(payload)
		if err != nil {
			return nil, err
		}
		return &ValidatedPayload{
			Cmd:       "chat",
			MessageID: messageID,
			Payload:   normalized,
		}, nil
	case "delete":
		normalized, messageID, err := normalizeDeletePayload(payload)
		if err != nil {
			return nil, err
		}
		return &ValidatedPayload{
			Cmd:       "delete",
			MessageID: messageID,
			Payload:   normalized,
		}, nil
	default:
		return nil, errors.New("payload type is invalid")
	}
}

func normalizeTextPayload(text string) (string, error) {
	normalized, _, err := normalizeStructuredTextPayload(text, "", "")
	return normalized, err
}

func normalizeStructuredTextPayload(text string, messageID string, sentAt string) (string, string, error) {
	if text == "" {
		return "", "", errors.New("text payload is empty")
	}
	if messageID == "" {
		messageID = util.RandomString(16)
	}
	normalized, err := json.Marshal(ChatPayload{
		Type:      "text",
		MessageID: messageID,
		Text:      text,
		SentAt:    sentAt,
	})
	if err != nil {
		return "", "", err
	}
	if len(normalized) > MaxPayloadBytes {
		return "", "", errors.New("payload size exceeds limit")
	}
	return string(normalized), messageID, nil
}

func normalizeImagePayload(payload ChatPayload) (string, string, error) {
	messageID := payload.MessageID
	if messageID == "" {
		messageID = util.RandomString(16)
	}

	dataURL, err := validateImageDataURL(payload.DataURL)
	if err != nil {
		return "", "", err
	}

	normalized, err := json.Marshal(ChatPayload{
		Type:      "image",
		MessageID: messageID,
		DataURL:   dataURL,
		SentAt:    payload.SentAt,
	})
	if err != nil {
		return "", "", err
	}
	if len(normalized) > MaxPayloadBytes {
		return "", "", errors.New("payload size exceeds limit")
	}

	return string(normalized), messageID, nil
}

func normalizeDeletePayload(payload ChatPayload) (string, string, error) {
	if payload.MessageID == "" {
		return "", "", errors.New("delete payload requires message id")
	}

	normalized, err := json.Marshal(ChatPayload{
		Type:      "delete",
		MessageID: payload.MessageID,
		SentAt:    payload.SentAt,
	})
	if err != nil {
		return "", "", err
	}
	if len(normalized) > MaxPayloadBytes {
		return "", "", errors.New("payload size exceeds limit")
	}

	return string(normalized), payload.MessageID, nil
}

func validateImageDataURL(dataURL string) (string, error) {
	if !strings.HasPrefix(dataURL, "data:") {
		return "", errors.New("data url is invalid")
	}

	parts := strings.SplitN(dataURL, ",", 2)
	if len(parts) != 2 {
		return "", errors.New("data url format is invalid")
	}

	header := parts[0]
	if !strings.HasSuffix(header, ";base64") {
		return "", errors.New("data url encoding is invalid")
	}

	declaredMime := strings.TrimPrefix(strings.TrimSuffix(header, ";base64"), "data:")
	declaredMime = normalizeAllowedMime(declaredMime)
	if declaredMime == "" {
		return "", errors.New("declared mime is not allowed")
	}

	decoded, err := base64.StdEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return "", errors.New("image base64 is invalid")
	}
	if len(decoded) == 0 || len(decoded) > MaxPayloadBytes {
		return "", errors.New("image bytes are invalid")
	}

	detectedMime := normalizeAllowedMime(http.DetectContentType(decoded))
	if detectedMime == "" {
		return "", errors.New("image mime is not allowed")
	}
	if detectedMime != declaredMime {
		return "", errors.New("declared mime does not match image bytes")
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil {
		return "", errors.New("image structure is invalid")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return "", errors.New("image dimensions are invalid")
	}

	return "data:" + detectedMime + ";base64," + parts[1], nil
}

func normalizeAllowedMime(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg", "image/jpg":
		return "image/jpeg"
	case "image/png":
		return "image/png"
	default:
		return ""
	}
}
