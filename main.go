package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
)

const (
	MistralAPIKey = ""
	MistralURL    = "https://api.mistral.ai/v1/audio/transcriptions"
	MistralModel  = "voxtral-mini-transcribe-2507"
)

type MistralResponse struct {
	Text string `json:"text"`
}

type TranscribeResponse struct {
	Text string `json:"text"`
}

func transcribeWithMistral(audioData []byte, filename string) (string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add model field
	if err := writer.WriteField("model", MistralModel); err != nil {
		return "", err
	}

	// Add file field
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(audioData); err != nil {
		return "", err
	}

	if err := writer.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", MistralURL, body)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+MistralAPIKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("mistral api error: %s (status %d)", string(respBody), resp.StatusCode)
	}

	var mistralResp MistralResponse
	if err := json.NewDecoder(resp.Body).Decode(&mistralResp); err != nil {
		return "", err
	}

	return mistralResp.Text, nil
}

func transcribeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	u, err := url.Parse(r.URL.String())
	if err != nil {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}

	query := u.Query()
	itemID := query.Get("item_id")
	if itemID == "" {
		itemID = "0"
	}

	audioData, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	fmt.Printf("Received %d bytes for item %s, sending to Mistral...\n", len(audioData), itemID)

	text, err := transcribeWithMistral(audioData, fmt.Sprintf("voice_%s.ogg", itemID))
	if err != nil {
		fmt.Printf("Error during transcription: %v\n", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(TranscribeResponse{
			Text: fmt.Sprintf("Transcription error: %v", err),
		})
		return
	}

	fmt.Printf("Transcription result: %s\n", func() string {
		if len(text) > 200 {
			return text[:200]
		}
		return text
	}())

	if text == "" {
		text = "(empty transcription)"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TranscribeResponse{
		Text: text,
	})
}

func main() {
	port := "8988"
	http.HandleFunc("/transcribe", transcribeHandler)

	fmt.Printf("Starting transcription server on port %s...\n", port)
	fmt.Printf("Using Mistral model: %s\n", MistralModel)

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		fmt.Printf("Error starting server: %v\n", err)
		os.Exit(1)
	}
}
