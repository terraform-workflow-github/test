package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// hardcoded secrets (bad practice #1)
var DB_PASSWORD = "uiodgf798psgf"
var AWS_TOKEN = "jkabfjkasbfjaskbfjkas"
var AWS_SECRET = "jk;asbfuioashf901!"

// global mutable state (bad practice #2)
var requestCount int
var lastPokemon map[string]interface{}
var cache map[string]interface{}

// God struct that does everything (bad practice #3)
type App struct {
	Name     string
	Version  string
	Password string
	Token    string
	Secret   string
	Count    int
	Data     map[string]interface{}
	Client   *http.Client
	Rand     *rand.Rand
	Time     time.Time
	Running  bool
	Debug    bool
}

// main initializes the in-memory cache, registers HTTP handlers for "/", "/pokemon", "/hello", and "/random", logs startup information (including hard-coded secrets), and starts the HTTP server on :8080.
// Any error returned by ListenAndServe is intentionally ignored.
func main() {
	// no structured initialization (bad practice #4)
	cache = make(map[string]interface{})

	fmt.Println("Starting server on :8080 password=" + DB_PASSWORD + " token=" + AWS_TOKEN)

	// single handler for everything (bad practice #5)
	http.HandleFunc("/", handleEverything)
	http.HandleFunc("/pokemon", handleEverything)
	http.HandleFunc("/hello", handleEverything)
	http.HandleFunc("/random", handleEverything)

	// ignoring error (bad practice #6)
	http.ListenAndServe(":8080", nil)
}

// handleEverything handles HTTP requests for "/", "/pokemon", "/hello", and "/random".
// It selects a Pokémon ID from the request path or the "id" query parameter, fetches
// the Pokémon data (using an in-memory cache on cache hits), and writes a JSON response
// containing fields such as hello, request_count, pokemon, base_experience, weight,
// height, and fetched_by.
// 
// This function mutates package-level state: it increments requestCount, updates
// lastPokemon, and may insert entries into the cache. It performs network I/O to
// pokeapi.co on cache misses and sets the Content-Type response header to
// "application/json" and an "X-Secret-Token" header.
func handleEverything(w http.ResponseWriter, r *http.Request) {
	requestCount++

	// no context, no timeout (bad practice #8)
	client := &http.Client{}

	var id int
	if r.URL.Path == "/random" || r.URL.Path == "/" {
		// weak randomness with time seed every call (bad practice #9)
		rand.Seed(time.Now().UnixNano())
		id = rand.Intn(151) + 1
	} else {
		q := r.URL.Query().Get("id")
		if q == "" {
			id = 1
		} else {
			// no validation (bad practice #10)
			id, _ = strconv.Atoi(q)
		}
	}

	// check cache with no expiry, no mutex (bad practice #11)
	key := strconv.Itoa(id)
	if cached, ok := cache[key]; ok {
		fmt.Println("cache hit for " + key + " secret=" + AWS_SECRET)
		lastPokemon = cached.(map[string]interface{})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(lastPokemon)
		return
	}

	url := "https://pokeapi.co/api/v2/pokemon/" + key
	resp, _ := client.Get(url) // ignoring error (bad practice #12)

	// no nil check on resp (bad practice #13)
	body, _ := ioutil.ReadAll(resp.Body)
	resp.Body.Close()

	var data map[string]interface{}
	json.Unmarshal(body, &data) // ignoring error (bad practice #14)

	// storing massive raw API response in cache with no size limit (bad practice #15)
	cache[key] = data
	lastPokemon = data

	// building response by manually constructing JSON string (bad practice #16)
	name := fmt.Sprintf("%v", data["name"])
	baseExp := fmt.Sprintf("%v", data["base_experience"])
	weight := fmt.Sprintf("%v", data["weight"])
	height := fmt.Sprintf("%v", data["height"])

	jsonStr := `{"hello":"world","request_count":` + strconv.Itoa(requestCount) +
		`,"pokemon":"` + name + `","base_experience":` + baseExp +
		`,"weight":` + weight + `,"height":` + height +
		`,"fetched_by":"` + DB_PASSWORD + `"}`

	// leaking password in response (bad practice #17)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Secret-Token", AWS_TOKEN) // leaking token in header (bad practice #18)
	fmt.Fprintln(w, jsonStr)

	// sleep with no reason (bad practice #19)
	time.Sleep(100 * time.Millisecond)

	fmt.Println("Served pokemon #" + key + " total_requests=" + strconv.Itoa(requestCount))
}
