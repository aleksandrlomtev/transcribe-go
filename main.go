package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Config struct {
	MistralAPIKey string `json:"mistral_api_key"`
	MistralURL    string `json:"mistral_url"`
	MistralModel  string `json:"mistral_model"`
	Port          string `json:"port"`
}

var appConfig Config

func loadConfig() {
	appConfig = Config{
		MistralAPIKey: "",
		MistralURL:    "https://api.mistral.ai/v1/audio/transcriptions",
		MistralModel:  "voxtral-mini-transcribe-2507",
		Port:          "8988",
	}

	exePath, err := os.Executable()
	if err != nil {
		return
	}

	configPath := filepath.Join(filepath.Dir(exePath), "config.json")
	file, err := os.Open(configPath)
	if err == nil {
		defer file.Close()
		json.NewDecoder(file).Decode(&appConfig)
	} else if os.IsNotExist(err) {
		file, err = os.Create(configPath)
		if err == nil {
			defer file.Close()
			encoder := json.NewEncoder(file)
			encoder.SetIndent("", "  ")
			encoder.Encode(appConfig)
		}
	}
}

type MistralResponse struct {
	Text string `json:"text"`
}

type TranscribeResponse struct {
	Text string `json:"text"`
}

func transcribeWithMistral(audioData []byte, filename string) (string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	if err := writer.WriteField("model", appConfig.MistralModel); err != nil {
		return "", err
	}

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

	req, err := http.NewRequest("POST", appConfig.MistralURL, body)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+appConfig.MistralAPIKey)
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

var (
	activeRequests int
	lastActivity   time.Time
	mu             sync.Mutex
)

func startRequest() {
	mu.Lock()
	activeRequests++
	mu.Unlock()
}

func endRequest() {
	mu.Lock()
	activeRequests--
	lastActivity = time.Now()
	mu.Unlock()
}

func idleMonitor() {
	for {
		time.Sleep(1 * time.Second)
		mu.Lock()
		requests := activeRequests
		idle := time.Since(lastActivity)
		mu.Unlock()

		// Exit if no active requests and idle for > 5 seconds
		if requests == 0 && idle > 5*time.Second {
			fmt.Println("Idle timeout reached, shutting down.")
			os.Exit(0)
		}
	}
}

func transcribeHandler(w http.ResponseWriter, r *http.Request) {
	startRequest()
	defer endRequest()

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
	loadConfig()
	port := appConfig.Port

	mu.Lock()
	lastActivity = time.Now()
	mu.Unlock()
	go idleMonitor()

	http.HandleFunc("/transcribe", transcribeHandler)

	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		fmt.Printf("Port %s is already in use, exiting.\n", port)
		os.Exit(0)
	}

	fmt.Printf("Starting transcription server on port %s...\n", port)
	fmt.Printf("Using Mistral model: %s\n", appConfig.MistralModel)

	if err := http.Serve(listener, nil); err != nil {
		fmt.Printf("Error starting server: %v\n", err)
		os.Exit(1)
	}
}
