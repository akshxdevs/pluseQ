package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"net/mail"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func (s *Server) RegisterRoutes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*", "http://*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/", s.HelloWorldHandler)
	r.Post("/api/v1/user/login", s.Login)
	return r
}

func (s *Server) HelloWorldHandler(w http.ResponseWriter, r *http.Request) {
	resp := make(map[string]string)
	resp["message"] = "Hello World"

	jsonResp, err := json.Marshal(resp)
	if err != nil {
		log.Fatalf("error handling JSON marshal. Err: %v", err)
	}

	_, _ = w.Write(jsonResp)
}

func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	type UserRequest struct {
		Id         string `json:"id"`
		Username   string `json:"username"`
		Email      string `json:"email"`
		Password   string `json:"password"`
		Created_at string `json:"created_at"`
	}
	type UserResponse struct {
		Id         string `json:"id"`
		Username   string `json:"username"`
		Email      string `json:"email"`
		Created_at string `json:"created_at"`
	}

	var req UserRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, "Invaild Body")
		return
	}
	num := rand.Intn(900000) + 100000
	generatedUsername := fmt.Sprintf("user%d", num)

	req.Username = generatedUsername
	// req.Id = uuid.NewString()

	req.Created_at = time.Now().UTC().String()

	if req.Email == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, "Invaild Credencials")
		return
	}

	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeJSON(w, http.StatusBadRequest, "Invalid email")
		return
	}

	var counter int

	id := "dfe1e2e3-50c7-4a06-b122-e439294955b1"
	req.Id = id
	if req.Password != "123random" {
		counter++
		inrCounter, err := s.redis.Incr(r.Context(), "mismatch_cnt:"+req.Id).Result()
		fmt.Printf("Counter Increment %s : %d", req.Id, inrCounter)
		if err != nil {
			log.Println(err)
			return
		}
		getCounter, err := s.redis.Get(r.Context(), "mismatch_cnt:"+req.Id).Result()
		checkCounter, err := strconv.Atoi(getCounter)
		log.Println("getCounter: ", getCounter)
		if err != nil {
			panic(err)
		}
		if checkCounter > 3 {
			go sendEmailJob(req.Id)
			return
		}
		writeJSON(w, http.StatusBadRequest, "Authentication failed")
		return
	}

	_, err := bcrypt.GenerateFromPassword([]byte(req.Password), 10)
	if err != nil {
		panic(err)
	}
	sessionId := uuid.NewString()
	s.redis.Set(r.Context(), "user_auth:"+sessionId, req.Id, time.Hour).Result()
	log.Println("session stored in redis")

	writeJSON(w, http.StatusAccepted, UserResponse{
		Id:         id,
		Username:   req.Username,
		Email:      req.Email,
		Created_at: req.Created_at,
	})

}

func sendEmailJob(id string) {
	log.Printf("Sending email to id %s to reset the password", id)
}

func decodeJSONStrict(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.New("invalid json payload")
	}
	if dec.More() {
		return errors.New("invalid json payload")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "Failed to marshal response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if _, err := w.Write(body); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}
