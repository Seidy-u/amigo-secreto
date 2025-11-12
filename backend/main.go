package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Pair struct {
	Giver     string `json:"giver"`
	Receiver  string `json:"receiver"`
	Password  string `json:"password,omitempty"` // bcrypt hash
	HasAccess bool   `json:"has_access"`
}

type State struct {
	Members []string `json:"members"`
	Pairs   []Pair   `json:"pairs"`
}

var (
	stateFile = "state.json"
	state     State
	mu        sync.Mutex
)

func init() {
	rand.Seed(time.Now().UnixNano())
	// state.json があれば読み込み
	if _, err := os.Stat(stateFile); err == nil {
		f, _ := os.Open(stateFile)
		defer f.Close()
		json.NewDecoder(f).Decode(&state)
	} else {
		state.Members = []string{} // 最初は空
		state.Pairs = []Pair{}
	}

	// メンバーがいる場合だけ抽選
	if len(state.Members) > 1 {
		drawAll()
	}
	saveState()
}

func saveState() error {
	f, err := os.Create(stateFile)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(&state)
}

// 全員のペアを作る
func drawAll() {
	if len(state.Members) < 2 {
		state.Pairs = nil
		return // 2人未満なら抽選しない
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

// 全員分の結果リスト
func listHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	type Entry struct {
		Name string `json:"name"`
		Link string `json:"link"`
	}
	var res []Entry
	for _, p := range state.Pairs {
		link := fmt.Sprintf("/result.html?giver=%s", p.Giver)
		res = append(res, Entry{Name: p.Giver, Link: link})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// パスワード設定 or 確認 + 結果取得
func resultHandler(w http.ResponseWriter, r *http.Request) {
	giver := r.URL.Query().Get("giver")
	if giver == "" {
		http.Error(w, "giverが必要です", 400)
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
					http.Error(w, "パスワードが違います", 403)
					return
				}
				json.NewEncoder(w).Encode(map[string]string{"receiver": p.Receiver})
				return
			}
			http.Error(w, "POSTメソッドのみ対応", 405)
			return
		}
	}
	http.Error(w, "該当者なし", 404)
}

// 参加者追加
func addHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POSTのみ対応", 405)
		return
	}
	var req struct{ Name string `json:"name"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, "名前が空です", 400)
		return
	}
	mu.Lock()
	defer mu.Unlock()
	for _, n := range state.Members {
		if n == req.Name {
			http.Error(w, "すでに存在します", 400)
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
	fmt.Fprint(w, "追加成功")
}

// リセット機能
func resetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POSTのみ対応", 405)
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
	fmt.Fprint(w, "状態をリセットしました")
}

func main() {
	fs := http.FileServer(http.Dir("../frontend"))
	http.Handle("/", fs)
	http.HandleFunc("/api/list", listHandler)
	http.HandleFunc("/api/result", resultHandler)
	http.HandleFunc("/api/add", addHandler)
	http.HandleFunc("/api/reset", resetHandler) // ←追加

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Server running on port %s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
