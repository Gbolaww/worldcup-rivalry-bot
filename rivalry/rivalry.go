package rivalry

import "sync"

// UserState tracks a single user's team/rival setup and bragging-rights tally.
type UserState struct {
	Team      string
	Rival     string
	GoalCount int // goals "scored" for their team via /goal
}

// Store is a simple thread-safe in-memory store keyed by Telegram chat ID.
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

func (s *Store) Get(chatID int64) *UserState {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[chatID]
	if !ok {
		u = &UserState{}
		s.users[chatID] = u
	}
	return u
}

func (s *Store) SetTeam(chatID int64, team string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.getLocked(chatID)
	u.Team = team
}

func (s *Store) SetRival(chatID int64, rival string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.getLocked(chatID)
	u.Rival = rival
}

func (s *Store) IncrementGoal(chatID int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.getLocked(chatID)
	u.GoalCount++
	return u.GoalCount
}

// getLocked assumes the caller already holds s.mu.
func (s *Store) getLocked(chatID int64) *UserState {
	u, ok := s.users[chatID]
	if !ok {
		u = &UserState{}
		s.users[chatID] = u
	}
	return u
}
