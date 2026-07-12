package rivalry

import "sync"

// UserState tracks a single user's team/rival setup and bragging-rights
// tallies. Keyed by Telegram user ID (not chat ID) so a person keeps the
// same team whether they're in a DM or any group — and multiple people in
// the same group can each root for their own team.
type UserState struct {
	Team           string
	Rival          string
	GoalCount      int   // goals celebrated for the user's own team via /goal
	RivalGoalCount int   // goals celebrated for the rival scoring via /rivalgoal
	LastChatID     int64 // most recent chat (DM or group) this user was seen in;
	// used so auto-hype (fired from a background poller, not a live message)
	// knows where to deliver the celebration.
}

// Store is a simple thread-safe in-memory store keyed by Telegram user ID.
// In-memory is fine for a hackathon demo; swap for a real DB if this becomes
// a long-lived product.
type Store struct {
	mu    sync.Mutex
	users map[int64]*UserState
}

func NewStore() *Store {
	return &Store{
		users: make(map[int64]*UserState),
	}
}

func (s *Store) Get(userID int64) *UserState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(userID)
}

func (s *Store) SetTeam(userID int64, team string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getLocked(userID).Team = team
}

func (s *Store) SetRival(userID int64, rival string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getLocked(userID).Rival = rival
}

func (s *Store) IncrementGoal(userID int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.getLocked(userID)
	u.GoalCount++
	return u.GoalCount
}

func (s *Store) IncrementRivalGoal(userID int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.getLocked(userID)
	u.RivalGoalCount++
	return u.RivalGoalCount
}

// SetLastChat records the most recent chat a user was seen sending a message
// in, so background processes (the auto-hype poller) know where to send
// celebrations for that user later.
func (s *Store) SetLastChat(userID, chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getLocked(userID).LastChatID = chatID
}

// getLocked assumes the caller already holds s.mu.
func (s *Store) getLocked(userID int64) *UserState {
	u, ok := s.users[userID]
	if !ok {
		u = &UserState{}
		s.users[userID] = u
	}
	return u
}
