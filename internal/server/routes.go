package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
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
	r.Post("/api/v1/user/signup", s.Signup)
	r.Post("/api/v1/user/login", s.Login)
	r.Post("api/v1/user/send-email/{id}", s.SendEmail)
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

func (s *Server) Signup(w http.ResponseWriter, r *http.Request) {
	type User struct {
		Id       string `json:"-"`
		Username string `json:"-"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	var req User
	if err := decodeJSONStrict(r, &req); err != nil {

	}
	num := rand.Intn(900000) + 100000
	generatedUsername := fmt.Sprintf("user%d", num)

	req.Username = generatedUsername
	req.Id = uuid.NewString()

}

func (s *Server) Login(w http.ResponseWriter, r *http.Request) {

}

func (s *Server) SendEmail(w http.ResponseWriter, r *http.Request) {

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
