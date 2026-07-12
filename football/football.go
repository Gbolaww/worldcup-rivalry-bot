package football

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client fetches real FIFA World Cup 2026 match data from the openfootball
// worldcup.json project (https://github.com/openfootball/worldcup.json) —
// a free, public-domain, no-API-key-required dataset. It's not second-by-
// second live (updated roughly daily by the maintainer), but it's real
// tournament data and doesn't depend on any paid API or rate-limited key.
type Client struct {
	http *http.Client
}

func NewClient() *Client {
	return &Client{http: &http.Client{}}
}

const dataURL = "https://raw.githubusercontent.com/openfootball/worldcup.json/master/2026/worldcup.json"

type tournamentData struct {
	Name    string  `json:"name"`
	Matches []Match `json:"matches"`
}

type Match struct {
	Round string `json:"round"`
	Date  string `json:"date"`
	Time  string `json:"time"`
	Team1 string `json:"team1"`
	Team2 string `json:"team2"`
	Score *Score `json:"score"` // nil if the match hasn't been played yet
	Group string `json:"group"`
}

type Score struct {
	FT [2]int `json:"ft"`
}

// GetAllMatches fetches the full current tournament dataset.
func (c *Client) GetAllMatches() ([]Match, error) {
	resp, err := c.http.Get(dataURL)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("worldcup.json returned status %d: %s", resp.StatusCode, string(body))
	}

	var data tournamentData
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("unmarshal tournament data: %w", err)
	}

	return data.Matches, nil
}

// LatestScoredMatchForTeam returns the most recent played match (i.e. one
// with a non-nil Score) involving the given team, matched case-insensitively
// against the team's name (e.g. "brazil" matches "Brazil"). Returns nil if
// no scored match is found for that team.
func LatestScoredMatchForTeam(matches []Match, team string) *Match {
	team = strings.ToLower(strings.TrimSpace(team))

	var latest *Match
	for i := range matches {
		m := &matches[i]
		if m.Score == nil {
			continue
		}
		t1 := strings.ToLower(m.Team1)
		t2 := strings.ToLower(m.Team2)
		if t1 == team || t2 == team || strings.Contains(t1, team) || strings.Contains(t2, team) {
			// Matches are in chronological order in the source data, so the
			// last matching entry is the most recent.
			latest = m
		}
	}
	return latest
}

// TotalGoals returns the combined goals scored in a match (0 if unplayed).
func (m *Match) TotalGoals() int {
	if m.Score == nil {
		return 0
	}
	return m.Score.FT[0] + m.Score.FT[1]
}
