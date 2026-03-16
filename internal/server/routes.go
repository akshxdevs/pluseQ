package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
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
	r.With(s.RateLimitMiddleware).Post("/api/v1/user/login", s.Login)
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
	req.Id = "dfe1e2e3-50c7-4a06-b122-e439294955b1"
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

	if req.Password != "123random" {
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

	getId, err := s.redis.HGetAll(r.Context(), "user_auth").Result()
	if err != nil {
		panic(err)
	}

	log.Printf("user auth: %s", getId)

	writeJSON(w, http.StatusAccepted, UserResponse{
		Id:         req.Id,
		Username:   req.Username,
		Email:      req.Email,
		Created_at: req.Created_at,
	})

}

func (s *Server) RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const maxAttempts = 2
		ip := r.RemoteAddr
		key := "rate_limit:" + ip

		cnt, err := s.redis.Incr(r.Context(), key).Result()
		fmt.Printf("Counter Increment %d", cnt)
		if err != nil {
			log.Println(err)
			return
		}

		if cnt > int64(maxAttempts)+1 {
			if cnt == int64(maxAttempts)+2 {
				data := fmt.Sprintf("%s:%s", ip, "email_queue")
				h := sha256.Sum256([]byte(data))
				idempotencyKey := hex.EncodeToString(h[:])
				go func() {
					if err := s.enqueueEmailJob(context.Background(), EmailJob{IP: ip, Reason: "rate_limit"}, idempotencyKey); err != nil {
						log.Printf("failed to enqueue email job: %v", err)
					}
				}()
			}
			writeJSON(w, http.StatusTooManyRequests, "Too many request. try again later")
			return
		}

		if cnt == 1 {
			_, err := s.redis.Expire(r.Context(), key, time.Second*10).Result()
			if err != nil {
				panic(err)
			}
		}
		next.ServeHTTP(w, r)
	})
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
