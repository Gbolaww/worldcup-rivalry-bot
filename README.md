# World Cup Rivalry Bot 🏆

A Telegram bot for the truly devoted World Cup fan. Pick your team, name your rival,
and when the goals fly, the bot claps back with an AI-generated hype/trash-talk line —
delivered as a voice note.

Built for the "Build Something Inspired by Passion" hackathon.
- **Google AI (Gemini)** generates the trash-talk text.
- **ElevenLabs** turns it into a voice note.
- **Telegram Bot API** delivers it all in chat.

## How it works

1. `/start` — bot greets you and asks for your team
2. Send your team code (e.g. `ARG`) — bot asks for your rival
3. Send your rival code (e.g. `BRA`) — setup confirmed
4. `/goal` — mock-triggers a "goal" event for your team (demo-safe, no live API dependency)
   - Gemini generates a short, punchy hype/trash-talk line aimed at your rival
   - ElevenLabs converts it to speech
   - Bot sends you the voice note
5. `/status` — shows your current team/rival and bragging-rights tally

## Setup

1. Copy `.env.example` to `.env` and fill in your keys:
   ```
   TELEGRAM_BOT_TOKEN=...
   GEMINI_API_KEY=...
   ELEVENLABS_API_KEY=...
   ELEVENLABS_VOICE_ID=...   # optional, defaults to a preset voice
   ```
2. Install deps and run:
   ```
   go mod tidy
   go run main.go
   ```
   The bot uses long polling, so no public URL/webhook is needed for local dev.

## Deploying (Render)

- Push this repo to GitHub.
- Create a new **Background Worker** (not a Web Service, since this bot uses polling) on Render.
- Set the environment variables from `.env.example` in the Render dashboard.
- Build command: `go build -o bot .`
- Start command: `./bot`

## Project structure

```
main.go              entrypoint — loads env, starts Telegram polling loop
telegram/            Telegram Bot API client (long polling, send text/voice)
ai/                  Gemini text generation + ElevenLabs text-to-speech clients
rivalry/             in-memory user state: team, rival, goal tally
```

## Notes

- User state is in-memory only (resets on restart) — fine for a hackathon demo.
- `/goal` is a manually-triggered mock event, not tied to a live scores API, so the
  demo never depends on real-world match timing or third-party API flakiness.
