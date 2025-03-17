package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/client"
	"golang.org/x/net/context"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// Config represents the JSON structure users can define in config.json.
type Config struct {
	Webhook string   `json:"webhook"`
	Error   []string `json:"error"`
	Warning []string `json:"warning"`
	Info    []string `json:"info"`
}

// Default configuration
var defaultConfig = Config{
	Webhook: "discord-webhook-url",
	Error:   []string{"die"},
	Warning: []string{"stop"},
	Info:    []string{"start"},
}

func main() {
	cfg, err := loadConfig("config.json")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create Docker client: %v", err)
	}
	log.Println("Docker client created")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msgs, errs := cli.Events(ctx, events.ListOptions{})

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	go handleDockerEvents(msgs, errs, signalChan, cfg)

	log.Println("Listening for Docker events and signals...")
	select {}
}

// handleDockerEvents processes Docker events and handles system signals.
func handleDockerEvents(msgs <-chan events.Message, errs <-chan error, signalChan <-chan os.Signal, cfg *Config) {
	for {
		select {
		case event := <-msgs:
			if event.Type == events.ContainerEventType {
				handleEvent(event, cfg)
			}
		case err := <-errs:
			if err != nil {
				log.Printf("Error receiving Docker event: %v", err)
			}
		case sig := <-signalChan:
			log.Printf("Received signal %v, shutting down", sig)
			return
		}
	}
}

// handleEvent processes Docker events
func handleEvent(event events.Message, cfg *Config) {
	level := getEventLevel(string(event.Action), cfg)
	if level == "" {
		return
	}

	log.Printf("Event: action=%s, level=%s", event.Action, level)
	notifyDiscord(event, level, cfg.Webhook)
}

// getEventLevel determines the event level based on the action and config.
func getEventLevel(action string, cfg *Config) string {
	switch {
	case inSlice(action, cfg.Error):
		return "error"
	case inSlice(action, cfg.Warning):
		return "warning"
	case inSlice(action, cfg.Info):
		return "info"
	default:
		return ""
	}
}

// notifyDiscord sends a notification to Discord
func notifyDiscord(event events.Message, level string, webhookURL string) {
	formattedTimeR := fmt.Sprintf("<t:%d:R>", event.Time)
	formattedTimeF := fmt.Sprintf("<t:%d:F>", event.Time)
	color := getColor(level)

	payload := map[string]interface{}{
		"username":   "DockaCord",
		"avatar_url": "https://i.imgur.com/zlREvEQ.png",
		"embeds": []map[string]interface{}{
			{
				"title":       fmt.Sprintf("Docker Event Notification - %s", strings.ToUpper(level)),
				"url":         "https://lyzev.dev/",
				"description": fmt.Sprintf("**Container**: `%s`\n**Action**: `%s`\n**At**: %s (%s)", event.Actor.Attributes["name"], event.Action, formattedTimeF, formattedTimeR),
				"color":       color,
				"footer": map[string]string{
					"text": "© 2025 Lyzev.",
				},
				"author": map[string]string{
					"name":     "Notification Bot",
					"icon_url": "https://i.imgur.com/zlREvEQ.png",
				},
			},
		},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Failed to marshal payload: %v", err)
		return
	}

	if webhookURL == "" {
		log.Println("Missing Discord webhook URL in config")
		return
	}

	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(payloadBytes))
	if err != nil {
		log.Printf("Failed to send webhook: %v", err)
		return
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Printf("Failed to close response body: %v", err)
		}
	}(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		log.Printf("Unexpected HTTP status: %d", resp.StatusCode)
	} else {
		log.Println("Successfully sent Discord notification")
	}
}

// getColor returns the color code for the given level.
func getColor(level string) int {
	switch level {
	case "warning":
		return 16776960
	case "error":
		return 16711680
	default:
		return 3066993
	}
}

// inSlice checks if an action is in the list of actions
func inSlice(action string, actions []string) bool {
	for _, a := range actions {
		if a == action {
			return true
		}
	}
	return false
}

// loadConfig loads configuration from a file
func loadConfig(filename string) (*Config, error) {
	_, err := os.Stat(filename)
	if os.IsNotExist(err) {
		log.Println("Config file not found, creating default config.json")
		defBytes, _ := json.MarshalIndent(defaultConfig, "", "  ")
		if writeErr := os.WriteFile(filename, defBytes, 0644); writeErr != nil {
			return nil, fmt.Errorf("failed to create default config: %v", writeErr)
		}
		return &defaultConfig, nil
	} else if err != nil {
		return nil, fmt.Errorf("cannot stat config file: %v", err)
	}

	configBytes, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("cannot read config file: %v", err)
	}

	var cfg Config
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		return nil, fmt.Errorf("invalid JSON in config file: %v", err)
	}
	return &cfg, nil
}
