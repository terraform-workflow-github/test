package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Config holds application configuration loaded from environment variables.
type Config struct {
	DBPassword  string
	AWSToken    string
	AWSSecret   string
	AdminToken  string
	EnableDB    bool
	EnableAWS   bool
}

// Pokemon holds the relevant fields from the PokeAPI response.
type Pokemon struct {
	Name           string `json:"name"`
	BaseExperience int    `json:"base_experience"`
	Weight         int    `json:"weight"`
	Height         int    `json:"height"`
}

// PokemonResponse is the JSON structure returned to clients.
type PokemonResponse struct {
	RequestCount   int64  `json:"request_count"`
	Pokemon        string `json:"pokemon"`
	BaseExperience int    `json:"base_experience"`
	Weight         int    `json:"weight"`
	Height         int    `json:"height"`
}

// Server holds all dependencies needed to serve requests.
type Server struct {
	config       Config
	cache        map[string]Pokemon
	cacheMu      sync.RWMutex
	client       *http.Client
	requestCount atomic.Int64
	rng          *rand.Rand
	rngMu        sync.Mutex
	favorites    map[string][]string
	favoritesMu  sync.RWMutex
}

func loadConfig() (Config, error) {
	cfg := Config{
		DBPassword: os.Getenv("DB_PASSWORD"),
		AWSToken:   os.Getenv("AWS_TOKEN"),
		AWSSecret:  os.Getenv("AWS_SECRET"),
		AdminToken: os.Getenv("ADMIN_TOKEN"),
		EnableDB:   os.Getenv("ENABLE_DB") == "true",
		EnableAWS:  os.Getenv("ENABLE_AWS") == "true",
	}
	if cfg.EnableDB && cfg.DBPassword == "" {
		return Config{}, fmt.Errorf("missing required environment variable: DB_PASSWORD")
	}
	if cfg.EnableAWS && (cfg.AWSToken == "" || cfg.AWSSecret == "") {
		return Config{}, fmt.Errorf("missing required environment variables: AWS_TOKEN, AWS_SECRET")
	}
	if cfg.AdminToken == "" {
		return Config{}, fmt.Errorf("missing required environment variable: ADMIN_TOKEN")
	}
	return cfg, nil
}

func newServer(cfg Config) *Server {
	return &Server{
		config:    cfg,
		cache:     make(map[string]Pokemon),
		client:    &http.Client{Timeout: 10 * time.Second},
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
		favorites: make(map[string][]string),
	}
}

func (s *Server) getPokemon(ctx context.Context, id int) (Pokemon, error) {
	key := strconv.Itoa(id)

	s.cacheMu.RLock()
	if p, ok := s.cache[key]; ok {
		s.cacheMu.RUnlock()
		return p, nil
	}
	s.cacheMu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://pokeapi.co/api/v2/pokemon/"+key, nil)
	if err != nil {
		return Pokemon{}, fmt.Errorf("creating request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return Pokemon{}, fmt.Errorf("fetching pokemon: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Pokemon{}, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Pokemon{}, fmt.Errorf("reading body: %w", err)
	}

	var pokemon Pokemon
	if err := json.Unmarshal(body, &pokemon); err != nil {
		return Pokemon{}, fmt.Errorf("parsing response: %w", err)
	}

	s.cacheMu.Lock()
	s.cache[key] = pokemon
	s.cacheMu.Unlock()

	return pokemon, nil
}

func (s *Server) servePokemon(w http.ResponseWriter, r *http.Request, id int) {
	count := s.requestCount.Add(1)

	pokemon, err := s.getPokemon(r.Context(), id)
	if err != nil {
		http.Error(w, "failed to fetch pokemon", http.StatusInternalServerError)
		fmt.Fprintf(os.Stderr, "error fetching pokemon %d: %v\n", id, err)
		return
	}

	resp := PokemonResponse{
		RequestCount:   count,
		Pokemon:        pokemon.Name,
		BaseExperience: pokemon.BaseExperience,
		Weight:         pokemon.Weight,
		Height:         pokemon.Height,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding response: %v\n", err)
	}

	fmt.Printf("Served pokemon #%d total_requests=%d\n", id, count)
}

func (s *Server) handlePokemon(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("id")
	var id int
	if q == "" {
		id = 1
	} else {
		var err error
		id, err = strconv.Atoi(q)
		if err != nil || id < 1 || id > 898 {
			http.Error(w, "invalid id: must be an integer between 1 and 898", http.StatusBadRequest)
			return
		}
	}
	s.servePokemon(w, r, id)
}

func (s *Server) handleRandom(w http.ResponseWriter, r *http.Request) {
	s.rngMu.Lock()
	id := s.rng.Intn(151) + 1
	s.rngMu.Unlock()
	s.servePokemon(w, r, id)
}

// handleAddFavorite lets authenticated users save a favorite pokemon by name.
func (s *Server) handleAddFavorite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := r.Header.Get("Authorization")
	if token != s.config.AdminToken {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	var body struct {
		User    string `json:"user"`
		Pokemon string `json:"pokemon"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.User == "" || body.Pokemon == "" {
		http.Error(w, "missing required parameters: user, pokemon", http.StatusBadRequest)
		return
	}

	s.favoritesMu.Lock()
	s.favorites[body.User] = append(s.favorites[body.User], body.Pokemon)
	favorites := make([]string, len(s.favorites[body.User]))
	copy(favorites, s.favorites[body.User])
	s.favoritesMu.Unlock()

	result, err := json.Marshal(map[string]interface{}{
		"user":      body.User,
		"favorites": favorites,
	})
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		fmt.Fprintf(os.Stderr, "error encoding favorites response: %v\n", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(result); err != nil {
		fmt.Fprintf(os.Stderr, "error writing favorites response: %v\n", err)
	}
}

// handleGetFavorites returns all favorites for a given user.
func (s *Server) handleGetFavorites(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token != s.config.AdminToken {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	user := r.URL.Query().Get("user")
	if user == "" {
		http.Error(w, "missing required parameter: user", http.StatusBadRequest)
		return
	}

	s.favoritesMu.RLock()
	favs := make([]string, len(s.favorites[user]))
	copy(favs, s.favorites[user])
	s.favoritesMu.RUnlock()

	result, err := json.Marshal(map[string]interface{}{
		"user":      user,
		"favorites": favs,
	})
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		fmt.Fprintf(os.Stderr, "error encoding favorites response: %v\n", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(result); err != nil {
		fmt.Fprintf(os.Stderr, "error writing favorites response: %v\n", err)
	}
}

func (s *Server) handleHello(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"hello": "world"}); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding response: %v\n", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	s := newServer(cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("/pokemon", s.handlePokemon)
	mux.HandleFunc("/random", s.handleRandom)
	mux.HandleFunc("/hello", s.handleHello)
	mux.HandleFunc("/favorites/add", s.handleAddFavorite)
	mux.HandleFunc("/favorites", s.handleGetFavorites)

	server := &http.Server{
		Addr:         ":8080",
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	fmt.Println("Starting server on :8080")
	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
