package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"

	"worldcup-rivalry-bot/ai"
	"worldcup-rivalry-bot/football"
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
	fb := football.NewClient()
	store := rivalry.NewStore()
	stages := make(map[int64]stage)

	// Render's free tier only offers Web Services (not Background Workers),
	// which require something listening on a port. This tiny HTTP server
	// exists purely to satisfy that requirement — the actual bot logic is
	// the polling loop below, running concurrently in the same process.
	go startKeepAliveServer()

	// Watches real World Cup 2026 results and auto-triggers hype
	// celebrations for any chat that has opted into /autohype.
	go startFootballPoller(fb, tg, gemini, eleven, store)

	log.Println("World Cup Rivalry Bot starting (long polling)...")

	offset := 0
	for {
		updates, err := tg.GetUpdates(offset)
		if err != nil {
			log.Printf("getUpdates error: %v", err)
			continue
		}

		if len(updates) > 0 {
			log.Printf("received %d update(s)", len(updates))
		}

		for _, u := range updates {
			offset = u.UpdateID + 1

			if u.Message == nil {
				log.Printf("update %d has no message, skipping", u.UpdateID)
				continue
			}
			chatID := u.Message.Chat.ID
			chatType := u.Message.Chat.Type
			text := strings.TrimSpace(u.Message.Text)
			log.Printf("handling message from chat %d (%s): %q", chatID, chatType, text)

			handleMessage(tg, gemini, eleven, fb, store, stages, chatID, chatType, text)
		}
	}
}

func handleMessage(
	tg *telegram.Client,
	gemini *ai.GeminiClient,
	eleven *ai.ElevenLabsClient,
	fb *football.Client,
	store *rivalry.Store,
	stages map[int64]stage,
	chatID int64,
	chatType string,
	text string,
) {
	lower := strings.ToLower(text)
	who := "You're" // adjusted below for groups
	whoPossessive := "your"
	if chatType == "group" || chatType == "supergroup" {
		who = "This group is"
		whoPossessive = "this group's"
	}

	switch {
	case lower == "/start":
		if chatType == "group" || chatType == "supergroup" {
			send(tg, chatID, "Welcome to the World Cup Rivalry Bot! ⚽🔥\n\nIn groups, use /team <name> and /rival <name> to set things up — e.g. /team Brazil")
			return
		}
		stages[chatID] = stageAwaitingTeam
		send(tg, chatID, "Welcome to the World Cup Rivalry Bot! ⚽🔥\n\nWhich team are you riding with? (e.g. ARG, BRA, FRA)")
		return

	case strings.HasPrefix(lower, "/team"):
		team := strings.TrimSpace(text[len("/team"):])
		if team == "" {
			send(tg, chatID, "Usage: /team <name>, e.g. /team Brazil")
			return
		}
		store.SetTeam(chatID, strings.Title(strings.ToLower(team)))
		send(tg, chatID, fmt.Sprintf("%s now riding with %s! Set a rival with /rival <name>.", who, strings.Title(strings.ToLower(team))))
		return

	case strings.HasPrefix(lower, "/rival"):
		rival := strings.TrimSpace(text[len("/rival"):])
		if rival == "" {
			send(tg, chatID, "Usage: /rival <name>, e.g. /rival Argentina")
			return
		}
		store.SetRival(chatID, strings.Title(strings.ToLower(rival)))
		u := store.Get(chatID)
		if u.Team == "" {
			send(tg, chatID, fmt.Sprintf("Rival set to %s. Now set %s team with /team <name>.", strings.Title(strings.ToLower(rival)), whoPossessive))
			return
		}
		send(tg, chatID, fmt.Sprintf("Locked in: %s vs %s 🔥\n\nSend /goal any time to celebrate with an AI hype voice note.", u.Team, u.Rival))
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
		celebrateGoal(tg, gemini, eleven, store, chatID)
		return

	case lower == "/autohype":
		u := store.Get(chatID)
		if u.Team == "" {
			send(tg, chatID, "Set your team first with /start, then try /autohype.")
			return
		}

		testModeMu.Lock()
		testModeChats[chatID] = true
		testModeMu.Unlock()

		// Seed the baseline immediately so we don't have to wait up to 60s
		// for the poller's first pass before /simulate has something to
		// rewind from.
		matches, err := fb.GetAllMatches()
		if err == nil {
			if match := football.LatestScoredMatchForTeam(matches, u.Team); match != nil {
				testModeMu.Lock()
				lastGoalTotals[chatID] = match.TotalGoals()
				testModeMu.Unlock()
			}
		} else {
			log.Printf("autohype baseline fetch error: %v", err)
		}

		send(tg, chatID, "Auto-hype mode on! 🔴 I'll check your team's real World Cup 2026 results and hype you up automatically when they score. Try /simulate to see it in action right now.")
		return

	case lower == "/simulate":
		u := store.Get(chatID)
		if u.Team == "" {
			send(tg, chatID, "Set your team first with /start, then try /autohype and /simulate.")
			return
		}
		testModeMu.Lock()
		_, on := testModeChats[chatID]
		if !on {
			testModeMu.Unlock()
			send(tg, chatID, "Turn on /autohype first, then run /simulate to demo the auto-detection live.")
			return
		}
		// Rewind our last-known goal count by 1, so the next poll cycle sees
		// an "increase" and fires the real detection path — same code as a
		// genuine new goal, just triggered on demand for demo purposes.
		if last, seen := lastGoalTotals[chatID]; seen && last > 0 {
			lastGoalTotals[chatID] = last - 1
		}
		testModeMu.Unlock()
		send(tg, chatID, "Simulating a new goal for your team... the auto-detection poller will pick it up within 60 seconds. ⏱️")
		return

	case lower == "/help":
		send(tg, chatID, "/start - get started (DMs) or see group instructions\n/team <name> - set your team (works in DMs & groups)\n/rival <name> - set your rival (works in DMs & groups)\n/goal - trigger a mock goal celebration\n/autohype - auto-hype when your real team scores in the World Cup\n/simulate - demo the auto-hype detection right now\n/status - see your current setup")
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

func celebrateGoal(tg *telegram.Client, gemini *ai.GeminiClient, eleven *ai.ElevenLabsClient, store *rivalry.Store, chatID int64) {
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
}

// testModeChats tracks which chats have opted into auto-goal-detection for
// their chosen team's real World Cup matches. lastGoalTotal remembers the
// last known total goals in that team's most recent scored match, so we can
// detect when it increases (a new goal, or a newly-finished match). Both
// protected by testModeMu since they're read/written from the main polling
// loop and the football poller goroutine.
var (
	testModeMu     sync.Mutex
	testModeChats  = make(map[int64]bool)
	lastGoalTotals = make(map[int64]int)
)

// startFootballPoller periodically checks each opted-in chat's team for
// real World Cup 2026 results (via the openfootball dataset) and
// auto-triggers a goal celebration when that team's latest match shows more
// total goals than last observed. Polls every 60s — the underlying data
// isn't second-by-second live, so faster polling wouldn't reveal anything
// new, and this stays comfortably polite to the free public dataset.
func startFootballPoller(fb *football.Client, tg *telegram.Client, gemini *ai.GeminiClient, eleven *ai.ElevenLabsClient, store *rivalry.Store) {
	for {
		time.Sleep(60 * time.Second)

		testModeMu.Lock()
		chats := make([]int64, 0, len(testModeChats))
		for chatID, on := range testModeChats {
			if on {
				chats = append(chats, chatID)
			}
		}
		testModeMu.Unlock()

		if len(chats) == 0 {
			continue
		}

		matches, err := fb.GetAllMatches()
		if err != nil {
			log.Printf("football poller error: %v", err)
			continue
		}

		for _, chatID := range chats {
			u := store.Get(chatID)
			if u.Team == "" {
				continue
			}

			match := football.LatestScoredMatchForTeam(matches, u.Team)
			if match == nil {
				continue
			}
			total := match.TotalGoals()

			testModeMu.Lock()
			last, seen := lastGoalTotals[chatID]
			lastGoalTotals[chatID] = total
			testModeMu.Unlock()

			if seen && total > last {
				log.Printf("goal detected for chat %d (%s): %s %d-%d %s",
					chatID, u.Team, match.Team1, match.Score.FT[0], match.Score.FT[1], match.Team2)
				celebrateGoal(tg, gemini, eleven, store, chatID)
			}
		}
	}
}

func send(tg *telegram.Client, chatID int64, text string) {
	if err := tg.SendMessage(chatID, text); err != nil {
		log.Printf("sendMessage error: %v", err)
	} else {
		log.Printf("sendMessage OK to chat %d", chatID)
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
