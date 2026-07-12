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

// conversation stage per user, so we know whether we're expecting a team,
// a rival, or a command. Keyed by user ID (not chat ID) so onboarding state
// follows the person, not the chat they happened to type in.
type stage int

const (
	stageAwaitingTeam stage = iota
	stageAwaitingRival
	stageReady
)

// App bundles all the shared clients and in-memory state the bot needs, and
// hangs the message-handling logic off it as methods. This avoids threading
// half a dozen parameters through every function as the feature set grows.
type App struct {
	tg     *telegram.Client
	gemini *ai.GeminiClient
	eleven *ai.ElevenLabsClient
	fb     *football.Client
	store  *rivalry.Store

	// Separate ElevenLabs voice IDs so a team and its rival sound like two
	// distinct characters instead of both using the same voice.
	teamVoiceID  string
	rivalVoiceID string

	// stages is only ever touched from the single main polling-loop
	// goroutine, so it doesn't need its own lock.
	stages map[int64]stage

	// testModeUsers / lastGoalTotals / lastRivalGoalTotals are read and
	// written from both the main polling loop (via /autohype, /simulate,
	// /simulaterival) and the background football poller goroutine, so they
	// share testModeMu.
	testModeMu          sync.Mutex
	testModeUsers       map[int64]bool
	lastGoalTotals      map[int64]int // user's own team's last known goal total
	lastRivalGoalTotals map[int64]int // user's rival's last known goal total
}

func main() {
	// Load .env if present. Not an error if it's missing (e.g. in production
	// on Render, where env vars are set directly in the dashboard instead).
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, relying on already-set environment variables")
	}

	botToken := mustEnv("TELEGRAM_BOT_TOKEN")
	geminiKey := mustEnv("GEMINI_API_KEY")
	elevenKey := mustEnv("ELEVENLABS_API_KEY")

	// Team voice falls back to the old ELEVENLABS_VOICE_ID var (for anyone
	// upgrading from before rival voices existed), then to ElevenLabs'
	// default "Rachel" preset.
	teamVoiceID := os.Getenv("ELEVENLABS_TEAM_VOICE_ID")
	if teamVoiceID == "" {
		teamVoiceID = os.Getenv("ELEVENLABS_VOICE_ID")
	}
	if teamVoiceID == "" {
		teamVoiceID = "21m00Tcm4TlvDq8ikWAM" // "Rachel"
	}

	rivalVoiceID := os.Getenv("ELEVENLABS_RIVAL_VOICE_ID")
	if rivalVoiceID == "" {
		rivalVoiceID = "pNInz6attGhOFRcW1ivM" // "Adam" — deliberately different from the team default
	}

	app := &App{
		tg:                  telegram.NewClient(botToken),
		gemini:              ai.NewGeminiClient(geminiKey),
		eleven:              ai.NewElevenLabsClient(elevenKey),
		fb:                  football.NewClient(),
		store:               rivalry.NewStore(),
		teamVoiceID:         teamVoiceID,
		rivalVoiceID:        rivalVoiceID,
		stages:              make(map[int64]stage),
		testModeUsers:       make(map[int64]bool),
		lastGoalTotals:      make(map[int64]int),
		lastRivalGoalTotals: make(map[int64]int),
	}

	// Render's free tier only offers Web Services (not Background Workers),
	// which require something listening on a port. This tiny HTTP server
	// exists purely to satisfy that requirement — the actual bot logic is
	// the polling loop below, running concurrently in the same process.
	go startKeepAliveServer()

	// Watches real World Cup 2026 results — for both a user's team and
	// their rival — and auto-triggers celebrations for anyone opted into
	// /autohype.
	go app.startFootballPoller()

	log.Println("World Cup Rivalry Bot starting (long polling)...")

	offset := 0
	for {
		updates, err := app.tg.GetUpdates(offset)
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
			if u.Message.From == nil {
				// Channel posts and a few other update types have no sender.
				// We only support DM/group chats from real users.
				log.Printf("update %d has no sender, skipping", u.UpdateID)
				continue
			}

			chatID := u.Message.Chat.ID
			chatType := u.Message.Chat.Type
			userID := u.Message.From.ID
			text := strings.TrimSpace(u.Message.Text)
			log.Printf("handling message from user %d in chat %d (%s): %q", userID, chatID, chatType, text)

			// Always record where we last heard from this user, so the
			// auto-hype poller (which runs independent of any incoming
			// message) knows where to deliver celebrations for them.
			app.store.SetLastChat(userID, chatID)

			app.handleMessage(userID, chatID, chatType, text)
		}
	}
}

func (a *App) handleMessage(userID, chatID int64, chatType, text string) {
	lower := strings.ToLower(text)
	groupNote := ""
	if chatType == "group" || chatType == "supergroup" {
		groupNote = " Everyone in this group can set their own team — just DM me or use /team here."
	}

	switch {
	case lower == "/start":
		a.stages[userID] = stageAwaitingTeam
		a.send(chatID, "Welcome to the World Cup Rivalry Bot! ⚽🔥"+groupNote+"\n\nWhich team are you riding with? (e.g. ARG, BRA, FRA)")
		return

	case strings.HasPrefix(lower, "/team"):
		team := strings.TrimSpace(text[len("/team"):])
		if team == "" {
			a.send(chatID, "Usage: /team <name>, e.g. /team Brazil")
			return
		}
		a.store.SetTeam(userID, strings.Title(strings.ToLower(team)))
		a.send(chatID, fmt.Sprintf("You're now riding with %s! Set a rival with /rival <name>.", strings.Title(strings.ToLower(team))))
		return

	case strings.HasPrefix(lower, "/rival"):
		rival := strings.TrimSpace(text[len("/rival"):])
		if rival == "" {
			a.send(chatID, "Usage: /rival <name>, e.g. /rival Argentina")
			return
		}
		a.store.SetRival(userID, strings.Title(strings.ToLower(rival)))
		u := a.store.Get(userID)
		if u.Team == "" {
			a.send(chatID, fmt.Sprintf("Rival set to %s. Now set your team with /team <name>.", strings.Title(strings.ToLower(rival))))
			return
		}
		a.send(chatID, fmt.Sprintf("Locked in: %s vs %s 🔥\n\nSend /goal when your team scores, or /rivalgoal when %s scores on you.", u.Team, u.Rival, u.Rival))
		return

	case lower == "/status":
		u := a.store.Get(userID)
		if u.Team == "" {
			a.send(chatID, "You haven't set up your team yet. Send /start to begin.")
			return
		}
		a.send(chatID, fmt.Sprintf("Team: %s\nRival: %s\nGoals celebrated for %s: %d\nGoals conceded to %s: %d", u.Team, u.Rival, u.Team, u.GoalCount, u.Rival, u.RivalGoalCount))
		return

	case lower == "/goal":
		a.celebrateGoal(userID, chatID)
		return

	case lower == "/rivalgoal":
		a.celebrateRivalGoal(userID, chatID)
		return

	case lower == "/autohype":
		u := a.store.Get(userID)
		if u.Team == "" {
			a.send(chatID, "Set your team first with /start, then try /autohype.")
			return
		}

		a.testModeMu.Lock()
		a.testModeUsers[userID] = true
		a.testModeMu.Unlock()

		// Seed baselines immediately for both team and rival, so we don't
		// have to wait up to 60s for the poller's first pass before
		// /simulate or /simulaterival has something to rewind from.
		matches, err := a.fb.GetAllMatches()
		if err == nil {
			if match := football.LatestScoredMatchForTeam(matches, u.Team); match != nil {
				a.testModeMu.Lock()
				a.lastGoalTotals[userID] = match.TotalGoals()
				a.testModeMu.Unlock()
			}
			if u.Rival != "" {
				if match := football.LatestScoredMatchForTeam(matches, u.Rival); match != nil {
					a.testModeMu.Lock()
					a.lastRivalGoalTotals[userID] = match.TotalGoals()
					a.testModeMu.Unlock()
				}
			}
		} else {
			log.Printf("autohype baseline fetch error: %v", err)
		}

		a.send(chatID, "Auto-hype mode on! 🔴 I'll watch real World Cup 2026 results for both your team and your rival, and hype you up automatically either way, right here in this chat. Try /simulate or /simulaterival to see it in action right now.")
		return

	case lower == "/simulate":
		u := a.store.Get(userID)
		if u.Team == "" {
			a.send(chatID, "Set your team first with /start, then try /autohype and /simulate.")
			return
		}
		a.testModeMu.Lock()
		_, on := a.testModeUsers[userID]
		if !on {
			a.testModeMu.Unlock()
			a.send(chatID, "Turn on /autohype first, then run /simulate to demo the auto-detection live.")
			return
		}
		// Rewind our last-known goal count by 1, so the next poll cycle sees
		// an "increase" and fires the real detection path — same code as a
		// genuine new goal, just triggered on demand for demo purposes.
		if last, seen := a.lastGoalTotals[userID]; seen && last > 0 {
			a.lastGoalTotals[userID] = last - 1
		}
		a.testModeMu.Unlock()
		a.send(chatID, "Simulating a new goal for your team... the auto-detection poller will pick it up within 60 seconds. ⏱️")
		return

	case lower == "/simulaterival":
		u := a.store.Get(userID)
		if u.Team == "" || u.Rival == "" {
			a.send(chatID, "Set your team and rival first with /start, then try /autohype and /simulaterival.")
			return
		}
		a.testModeMu.Lock()
		_, on := a.testModeUsers[userID]
		if !on {
			a.testModeMu.Unlock()
			a.send(chatID, "Turn on /autohype first, then run /simulaterival to demo the auto-detection live.")
			return
		}
		if last, seen := a.lastRivalGoalTotals[userID]; seen && last > 0 {
			a.lastRivalGoalTotals[userID] = last - 1
		}
		a.testModeMu.Unlock()
		a.send(chatID, fmt.Sprintf("Simulating a new goal for %s... the auto-detection poller will pick it up within 60 seconds. ⏱️", u.Rival))
		return

	case lower == "/help":
		a.send(chatID, "/start - get started\n/team <name> - set your team (personal — works in DMs & groups)\n/rival <name> - set your rival\n/goal - trigger a mock goal celebration for your team\n/rivalgoal - trigger a mock goal celebration for your rival scoring on you\n/autohype - auto-hype either side when real goals happen in the World Cup\n/simulate - demo auto-detection for your team scoring\n/simulaterival - demo auto-detection for your rival scoring\n/status - see your current setup")
		return
	}

	// Not a recognized command — treat as conversational input based on stage.
	switch a.stages[userID] {
	case stageAwaitingTeam:
		a.store.SetTeam(userID, strings.ToUpper(text))
		a.stages[userID] = stageAwaitingRival
		a.send(chatID, fmt.Sprintf("%s it is! Now, who's your rival? (e.g. BRA)", strings.ToUpper(text)))

	case stageAwaitingRival:
		a.store.SetRival(userID, strings.ToUpper(text))
		a.stages[userID] = stageReady
		u := a.store.Get(userID)
		a.send(chatID, fmt.Sprintf(
			"Locked in: %s vs %s 🔥\n\nSend /goal when your team scores, or /rivalgoal when %s scores on you.",
			u.Team, u.Rival, u.Rival,
		))

	default:
		a.send(chatID, "Not sure what you mean — try /help for available commands.")
	}
}

// celebrateGoal handles the user's own team scoring: a hype line in the
// team's voice, followed by an imagined clapback from the rival's fans in
// the rival's voice.
func (a *App) celebrateGoal(userID, chatID int64) {
	u := a.store.Get(userID)
	if u.Team == "" || u.Rival == "" {
		a.send(chatID, "Set up your team and rival first — send /start.")
		return
	}
	a.send(chatID, fmt.Sprintf("GOAL for %s! 🎉 Cooking up some hype...", u.Team))
	a.celebrateScore(userID, chatID, u.Team, a.teamVoiceID, u.Rival, a.rivalVoiceID, a.store.IncrementGoal)
}

// celebrateRivalGoal handles the rival scoring on the user: a taunting line
// from the rival's fans in the rival's voice, followed by an imagined
// comeback from the user's own team's fans in the team's voice.
func (a *App) celebrateRivalGoal(userID, chatID int64) {
	u := a.store.Get(userID)
	if u.Team == "" || u.Rival == "" {
		a.send(chatID, "Set up your team and rival first — send /start.")
		return
	}
	a.send(chatID, fmt.Sprintf("%s just scored on you... 😬 let's hear what they've got to say.", u.Rival))
	a.celebrateScore(userID, chatID, u.Rival, a.rivalVoiceID, u.Team, a.teamVoiceID, a.store.IncrementRivalGoal)
}

// celebrateScore is the shared engine behind both /goal and /rivalgoal: it
// generates a hype line for whichever side just scored (spoken in that
// side's voice), then a clapback from the other side (spoken in the other
// side's voice). incrementCount lets the caller bump whichever tally
// (goals for vs. goals conceded) applies.
func (a *App) celebrateScore(userID, chatID int64, scorer, scorerVoiceID, opponent, opponentVoiceID string, incrementCount func(int64) int) {
	line, err := a.gemini.GenerateHypeLine(scorer, opponent)
	if err != nil {
		log.Printf("gemini error: %v", err)
		a.send(chatID, "Couldn't generate a hype line right now, but that's still a GOAL! ⚽")
		return
	}

	audio, err := a.eleven.GenerateSpeech(line, scorerVoiceID)
	if err != nil {
		log.Printf("elevenlabs error: %v", err)
		a.send(chatID, "Line: "+line+"\n(voice generation failed, but the trash talk stands!)")
		return
	}

	if err := a.tg.SendVoice(chatID, audio, line); err != nil {
		log.Printf("send voice error: %v", err)
		a.send(chatID, "Line: "+line+"\n(couldn't deliver the voice note, sorry!)")
		return
	}

	incrementCount(userID)

	// Follow up with the other side's imagined clapback as a second voice
	// note. Best-effort only — if this fails, the celebration above already
	// landed successfully, so we just log and stop rather than erroring out.
	comeback, err := a.gemini.GenerateComebackLine(scorer, opponent, line)
	if err != nil {
		log.Printf("gemini comeback error: %v", err)
		return
	}

	comebackAudio, err := a.eleven.GenerateSpeech(comeback, opponentVoiceID)
	if err != nil {
		log.Printf("elevenlabs comeback error: %v", err)
		a.send(chatID, fmt.Sprintf("%s fans, imagined comeback: %s", opponent, comeback))
		return
	}

	if err := a.tg.SendVoice(chatID, comebackAudio, comeback); err != nil {
		log.Printf("send comeback voice error: %v", err)
		a.send(chatID, fmt.Sprintf("%s fans, imagined comeback: %s", opponent, comeback))
	}
}

// startFootballPoller periodically checks each opted-in user's team AND
// rival for real World Cup 2026 results (via the openfootball dataset) and
// auto-triggers a celebration whenever either side's latest match shows
// more total goals than last observed. Celebrations are delivered to the
// user's LastChatID (wherever they last messaged the bot from), since this
// poller runs independent of any live message. Polls every 60s — the
// underlying data isn't second-by-second live, so faster polling wouldn't
// reveal anything new, and this stays comfortably polite to the free public
// dataset.
func (a *App) startFootballPoller() {
	for {
		time.Sleep(60 * time.Second)

		a.testModeMu.Lock()
		userIDs := make([]int64, 0, len(a.testModeUsers))
		for userID, on := range a.testModeUsers {
			if on {
				userIDs = append(userIDs, userID)
			}
		}
		a.testModeMu.Unlock()

		if len(userIDs) == 0 {
			continue
		}

		matches, err := a.fb.GetAllMatches()
		if err != nil {
			log.Printf("football poller error: %v", err)
			continue
		}

		for _, userID := range userIDs {
			u := a.store.Get(userID)
			if u.Team == "" || u.LastChatID == 0 {
				continue
			}

			if match := football.LatestScoredMatchForTeam(matches, u.Team); match != nil {
				total := match.TotalGoals()
				a.testModeMu.Lock()
				last, seen := a.lastGoalTotals[userID]
				a.lastGoalTotals[userID] = total
				a.testModeMu.Unlock()

				if seen && total > last {
					log.Printf("goal detected for user %d (%s): %s %d-%d %s",
						userID, u.Team, match.Team1, match.Score.FT[0], match.Score.FT[1], match.Team2)
					a.celebrateGoal(userID, u.LastChatID)
				}
			}

			if u.Rival == "" {
				continue
			}

			if match := football.LatestScoredMatchForTeam(matches, u.Rival); match != nil {
				total := match.TotalGoals()
				a.testModeMu.Lock()
				last, seen := a.lastRivalGoalTotals[userID]
				a.lastRivalGoalTotals[userID] = total
				a.testModeMu.Unlock()

				if seen && total > last {
					log.Printf("rival goal detected for user %d (%s): %s %d-%d %s",
						userID, u.Rival, match.Team1, match.Score.FT[0], match.Score.FT[1], match.Team2)
					a.celebrateRivalGoal(userID, u.LastChatID)
				}
			}
		}
	}
}

func (a *App) send(chatID int64, text string) {
	if err := a.tg.SendMessage(chatID, text); err != nil {
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
