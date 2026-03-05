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
	"sync"
	"sync/atomic"
	"time"
)

// Config holds application configuration loaded from environment variables.
type Config struct {
	DBPassword string
	AWSToken   string
	AWSSecret  string
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
}

func loadConfig() (Config, error) {
	cfg := Config{
		DBPassword: os.Getenv("DB_PASSWORD"),
		AWSToken:   os.Getenv("AWS_TOKEN"),
		AWSSecret:  os.Getenv("AWS_SECRET"),
	}
	if cfg.DBPassword == "" || cfg.AWSToken == "" || cfg.AWSSecret == "" {
		return Config{}, fmt.Errorf("missing required environment variables: DB_PASSWORD, AWS_TOKEN, AWS_SECRET")
	}
	return cfg, nil
}

func newServer(cfg Config) *Server {
	return &Server{
		config: cfg,
		cache:  make(map[string]Pokemon),
		client: &http.Client{Timeout: 10 * time.Second},
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
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

func (s *Server) handleHello(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"hello": "world"})
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

	fmt.Println("Starting server on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
