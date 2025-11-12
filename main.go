package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

type Pair struct {
	Giver     string `json:"giver"`
	Receiver  string `json:"receiver"`
	Password  string `json:"password,omitempty"`
	HasAccess bool   `json:"has_access"`
}

type State struct {
	Members []string `json:"members"`
	Pairs   []Pair   `json:"pairs"`
}

var (
	mu    sync.Mutex
	state State

	ctx = context.Background()
	rdb *redis.Client
)

// Redis 初期化
func initRedis() {
	redisURL := "redis://default:MmLZyOinKRsWTKU3AT9CztBFa9YFWQtk@redis-14606.c294.ap-northeast-1-2.ec2.cloud.redislabs.com:14606"
	if redisURL == "" {
		// ローカル Redis
		rdb = redis.NewClient(&redis.Options{
			Addr: "localhost:6379",
		})
	} else {
		opt, err := redis.ParseURL(redisURL)
		if err != nil {
			panic(err)
		}
		rdb = redis.NewClient(opt)
	}
	fmt.Println(redisURL)
}

// Redis から状態読み込み
func loadState() error {
	data, err := rdb.Get(ctx, "amigo_state").Bytes()
	if err != nil {
		if err == redis.Nil {
			state = State{Members: []string{}, Pairs: []Pair{}}
			return nil
		}
		return err
	}
	return json.Unmarshal(data, &state)
}

// Redis に状態保存
func saveState() error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, "amigo_state", data, 0).Err()
}

// メンバー抽選
func drawAll() {
	if len(state.Members) < 2 {
		state.Pairs = nil
		return
	}
	for {
		shuffled := append([]string{}, state.Members...)
		rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

		valid := true
		state.Pairs = nil
		for i := range state.Members {
			giver := state.Members[i]
			receiver := shuffled[i]
			if giver == receiver {
				valid = false
				break
			}
			state.Pairs = append(state.Pairs, Pair{Giver: giver, Receiver: receiver})
		}

		if valid {
			break
		}
	}
	saveState()
}

// 一覧取得
func listHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	type Entry struct {
		Name string `json:"name"`
		Link string `json:"link"`
	}
	var res []Entry
	for _, p := range state.Pairs {
		link := fmt.Sprintf("./result.html?giver=%s", p.Giver)
		res = append(res, Entry{Name: p.Giver, Link: link})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// 結果取得 / パスワード設定
func resultHandler(w http.ResponseWriter, r *http.Request) {
	giver := r.URL.Query().Get("giver")
	if giver == "" {
		http.Error(w, "O campo giver é obrigatório", 400)
		return
	}
	mu.Lock()
	defer mu.Unlock()
	for i, p := range state.Pairs {
		if p.Giver == giver {
			if r.Method == "POST" {
				var req struct{ Password string `json:"password"` }
				json.NewDecoder(r.Body).Decode(&req)
				if p.Password == "" {
					hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
					state.Pairs[i].Password = string(hash)
					state.Pairs[i].HasAccess = true
					saveState()
					json.NewEncoder(w).Encode(map[string]string{"receiver": p.Receiver})
					return
				}
				if err := bcrypt.CompareHashAndPassword([]byte(p.Password), []byte(req.Password)); err != nil {
					http.Error(w, "Senha incorreta", 403)
					return
				}
				json.NewEncoder(w).Encode(map[string]string{"receiver": p.Receiver})
				return
			}
			http.Error(w, "Apenas POST é suportado", 405)
			return
		}
	}
	http.Error(w, "Participante não encontrado", 404)
}

// メンバー追加
func addHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Apenas POST é suportado", 405)
		return
	}
	var req struct{ Name string `json:"name"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, "O nome não pode estar vazio", 400)
		return
	}
	mu.Lock()
	defer mu.Unlock()
	for _, n := range state.Members {
		if n == req.Name {
			http.Error(w, "Já existe", 400)
			return
		}
	}
	state.Members = append(state.Members, req.Name)
	state.Pairs = nil
	drawAll()
	if err := saveState(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(200)
	fmt.Fprint(w, "Adicionado com sucesso")
}

// リセット
func resetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Apenas POST é suportado", 405)
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Erro no request", 400)
		return
	}

	// 固定パスワード
	if req.Password != "avadakedavra" {
		http.Error(w, "Senha incorreta", 403)
		return
	}

	mu.Lock()
	defer mu.Unlock()
	state.Members = []string{}
	state.Pairs = []Pair{}
	if err := saveState(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(200)
	fmt.Fprint(w, "Estado reiniciado com sucesso")
}

func main() {
	rand.Seed(time.Now().UnixNano())
	initRedis()
	if err := loadState(); err != nil {
		log.Println("Erro ao carregar estado:", err)
		state = State{Members: []string{}, Pairs: []Pair{}}
	}
	if len(state.Members) > 1 {
		drawAll()
	}

	fs := http.FileServer(http.Dir("./frontend"))
	http.Handle("/", fs)
	http.HandleFunc("/api/list", listHandler)
	http.HandleFunc("/api/result", resultHandler)
	http.HandleFunc("/api/add", addHandler)
	http.HandleFunc("/api/reset", resetHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Servidor rodando na porta %s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
