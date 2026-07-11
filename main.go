package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/joho/godotenv"

	"worldcup-rivalry-bot/ai"
	"worldcup-rivalry-bot/rivalry"
	"worldcup-rivalry-bot/telegram"
)

// conversation stage per chat, so we know whether we're expecting a team,
// a rival, or a command.
type stage int

const (
	stageAwaitingTeam stage = iota
	stageAwaitingRival
	stageReady
)

func main() {
	// Load .env if present. Not an error if it's missing (e.g. in production
	// on Render, where env vars are set directly in the dashboard instead).
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, relying on already-set environment variables")
	}

	botToken := mustEnv("TELEGRAM_BOT_TOKEN")
	geminiKey := mustEnv("GEMINI_API_KEY")
	elevenKey := mustEnv("ELEVENLABS_API_KEY")
	voiceID := os.Getenv("ELEVENLABS_VOICE_ID")
	if voiceID == "" {
		voiceID = "21m00Tcm4TlvDq8ikWAM" // default preset voice ("Rachel")
	}

	tg := telegram.NewClient(botToken)
	gemini := ai.NewGeminiClient(geminiKey)
	eleven := ai.NewElevenLabsClient(elevenKey, voiceID)
	store := rivalry.NewStore()
	stages := make(map[int64]stage)

	// Render's free tier only offers Web Services (not Background Workers),
	// which require something listening on a port. This tiny HTTP server
	// exists purely to satisfy that requirement — the actual bot logic is
	// the polling loop below, running concurrently in the same process.
	go startKeepAliveServer()

	log.Println("World Cup Rivalry Bot starting (long polling)...")

	offset := 0
	for {
		updates, err := tg.GetUpdates(offset)
		if err != nil {
			log.Printf("getUpdates error: %v", err)
			continue
		}

		for _, u := range updates {
			offset = u.UpdateID + 1

			if u.Message == nil {
				continue
			}
			chatID := u.Message.Chat.ID
			text := strings.TrimSpace(u.Message.Text)

			handleMessage(tg, gemini, eleven, store, stages, chatID, text)
		}
	}
}

func handleMessage(
	tg *telegram.Client,
	gemini *ai.GeminiClient,
	eleven *ai.ElevenLabsClient,
	store *rivalry.Store,
	stages map[int64]stage,
	chatID int64,
	text string,
) {
	lower := strings.ToLower(text)

	switch {
	case lower == "/start":
		stages[chatID] = stageAwaitingTeam
		send(tg, chatID, "Welcome to the World Cup Rivalry Bot! ⚽🔥\n\nWhich team are you riding with? (e.g. ARG, BRA, FRA)")
		return

	case lower == "/status":
		u := store.Get(chatID)
		if u.Team == "" {
			send(tg, chatID, "You haven't set up your team yet. Send /start to begin.")
			return
		}
		send(tg, chatID, fmt.Sprintf("Team: %s\nRival: %s\nGoals celebrated: %d", u.Team, u.Rival, u.GoalCount))
		return

	case lower == "/goal":
		u := store.Get(chatID)
		if u.Team == "" || u.Rival == "" {
			send(tg, chatID, "Set up your team and rival first — send /start.")
			return
		}

		send(tg, chatID, fmt.Sprintf("GOAL for %s! 🎉 Cooking up some hype...", u.Team))

		line, err := gemini.GenerateHypeLine(u.Team, u.Rival)
		if err != nil {
			log.Printf("gemini error: %v", err)
			send(tg, chatID, "Couldn't generate a hype line right now, but that's still a GOAL! ⚽")
			return
		}

		audio, err := eleven.GenerateSpeech(line)
		if err != nil {
			log.Printf("elevenlabs error: %v", err)
			send(tg, chatID, "Hype line: "+line+"\n(voice generation failed, but the trash talk stands!)")
			return
		}

		if err := tg.SendVoice(chatID, audio, line); err != nil {
			log.Printf("send voice error: %v", err)
			send(tg, chatID, "Hype line: "+line+"\n(couldn't deliver the voice note, sorry!)")
			return
		}

		store.IncrementGoal(chatID)
		return

	case lower == "/help":
		send(tg, chatID, "/start - set up your team & rival\n/goal - trigger a mock goal celebration\n/status - see your current setup")
		return
	}

	// Not a recognized command — treat as conversational input based on stage.
	switch stages[chatID] {
	case stageAwaitingTeam:
		store.SetTeam(chatID, strings.ToUpper(text))
		stages[chatID] = stageAwaitingRival
		send(tg, chatID, fmt.Sprintf("%s it is! Now, who's your rival? (e.g. BRA)", strings.ToUpper(text)))

	case stageAwaitingRival:
		store.SetRival(chatID, strings.ToUpper(text))
		stages[chatID] = stageReady
		u := store.Get(chatID)
		send(tg, chatID, fmt.Sprintf(
			"Locked in: %s vs %s 🔥\n\nSend /goal any time to celebrate a goal with an AI hype voice note.",
			u.Team, u.Rival,
		))

	default:
		send(tg, chatID, "Not sure what you mean — try /help for available commands.")
	}
}

func send(tg *telegram.Client, chatID int64, text string) {
	if err := tg.SendMessage(chatID, text); err != nil {
		log.Printf("sendMessage error: %v", err)
	}
}

func startKeepAliveServer() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080" // fallback for local runs; Render sets PORT automatically
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("World Cup Rivalry Bot is running."))
	})

	log.Printf("keep-alive HTTP server listening on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Printf("keep-alive server error: %v", err)
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("missing required environment variable: %s", key)
	}
	return v
}
