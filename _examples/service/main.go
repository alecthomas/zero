package main

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"

	"github.com/alecthomas/kong"
	"github.com/alecthomas/zero"
	"github.com/brianvoe/gofakeit"
)

type DAL struct {
	users map[int]User
}

//zero:provider
func NewDAL(db *sql.DB) (*DAL, error) {
	return &DAL{
		users: map[int]User{
			1: {Name: gofakeit.Name(), BirthYear: gofakeit.Date().Year()},
			2: {Name: gofakeit.Name(), BirthYear: gofakeit.Date().Year()},
		},
	}, nil
}

func (d *DAL) GetUsers() ([]User, error) {
	return slices.Collect(maps.Values(d.users)), nil
}

func (d *DAL) CreateUser(user User) error {
	nextId := slices.Max(slices.Collect(maps.Keys(d.users))) + 1
	d.users[nextId] = user
	return nil
}

//zero:middleware authenticated
func Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

//zero:config
type ServiceConfig struct {
}

type Service struct {
	dal *DAL
}

//zero:provider
func NewService(dal *DAL, config ServiceConfig) (*Service, error) {
	// Other initialisation
	return &Service{dal: dal}, nil
}

type User struct {
	Name      string `json:"name"`
	BirthYear int    `json:"birthYear"`
}

type UserCreatedEvent User

func (u UserCreatedEvent) ID() string { return u.Name }

//zero:api GET /users
func (s *Service) ListUsers() ([]User, error) {
	return s.dal.GetUsers()
}

//zero:api POST /users authenticated
func (s *Service) CreateUser(user User) error {
	return s.dal.CreateUser(user)
}

//zero:api GET /users/{id}
func (s *Service) GetUser(id string) (User, error) {
	panic("not implemented")
}

// zero:subscribe
func (s *Service) OnUserCreated(user zero.Event[UserCreatedEvent]) error {
	panic("not implemented")
}

// zero:cron 1h
func (s *Service) CheckUsers() error {
	panic("not implemented")
}

var cli struct {
	ZeroConfig
}

func main() {
	kctx := kong.Parse(&cli)
	ctx := context.Background()
	singletons := map[reflect.Type]any{}
	_, err := ZeroConstructSingletons[*Service](ctx, cli.ZeroConfig, singletons)
	kctx.FatalIfErrorf(err)
	mux, err := ZeroConstructSingletons[*http.ServeMux](ctx, cli.ZeroConfig, singletons)
	kctx.FatalIfErrorf(err)
	fmt.Println("Listening on http://127.0.0.1:8080")
	http.ListenAndServe("127.0.0.1:8080", mux)
}
